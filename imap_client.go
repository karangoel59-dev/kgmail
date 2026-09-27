package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const defaultTimeout = 10 * time.Second

// DialIMAP connects and logs into an IMAP server according to AccountConfig.
func DialIMAP(cfg AccountConfig, timeout time.Duration) (*client.Client, error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}

	host := strings.TrimSpace(cfg.Host)
	port := cfg.Port
	if port == 0 {
		port = 993
	}

	if host == "" || cfg.Username == "" {
		return nil, fmt.Errorf("account configuration missing required host or username")
	}
	if !cfg.IsOAuth2() && cfg.Password == "" {
		return nil, fmt.Errorf("account has no password and is not configured for OAuth2 (set tenant_id + client_id)")
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	tlsConfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	isSSL := cfg.SSL == nil || *cfg.SSL

	var c *client.Client
	var err error

	dialer := &net.Dialer{Timeout: timeout}

	if isSSL {
		conn, dialErr := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if dialErr != nil {
			return nil, fmt.Errorf("TLS connection to %s failed: %w", addr, dialErr)
		}
		c, err = client.New(conn)
	} else {
		conn, dialErr := dialer.Dial("tcp", addr)
		if dialErr != nil {
			return nil, fmt.Errorf("TCP connection to %s failed: %w", addr, dialErr)
		}
		c, err = client.New(conn)
		if err == nil && cfg.StartTLS {
			if err = c.StartTLS(tlsConfig); err != nil {
				c.Close()
				return nil, fmt.Errorf("StartTLS handshake with %s failed: %w", addr, err)
			}
		}
	}

	if err != nil {
		return nil, fmt.Errorf("IMAP client init error: %w", err)
	}

	// Set overall command timeout
	c.Timeout = timeout

	if cfg.IsOAuth2() {
		// Get (or refresh) the Microsoft access token, then authenticate via XOAUTH2
		token, tokenErr := GetOrRefreshMicrosoftToken(&cfg)
		if tokenErr != nil {
			c.Close()
			return nil, fmt.Errorf("OAuth2 token error for %s: %w", cfg.Username, tokenErr)
		}
		saslClient := newXOAuth2Client(cfg.Username, token)
		if authErr := c.Authenticate(saslClient); authErr != nil {
			c.Close()
			return nil, fmt.Errorf("IMAP XOAUTH2 auth failed for %s on %s: %w", cfg.Username, host, authErr)
		}
	} else {
		// Standard username / password login
		if err := c.Login(cfg.Username, cfg.Password); err != nil {
			c.Close()
			return nil, fmt.Errorf("IMAP login failed for %s on %s: %w", cfg.Username, host, err)
		}
	}

	return c, nil
}

// TestConnection verifies that the account can successfully connect and authenticate.
func TestConnection(cfg AccountConfig) error {
	if cfg.IsGraph() {
		return TestConnectionGraph(cfg)
	}
	c, err := DialIMAP(cfg, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Logout()
	return nil
}

// ListFolders returns all mailboxes/folders for the account.
func ListFolders(cfg AccountConfig) ([]string, error) {
	if cfg.IsGraph() {
		return ListFoldersGraph(cfg)
	}
	c, err := DialIMAP(cfg, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	mailboxes := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	var folders []string
	for m := range mailboxes {
		folders = append(folders, m.Name)
	}

	if err := <-done; err != nil {
		return nil, fmt.Errorf("failed to list mailboxes: %w", err)
	}

	return folders, nil
}

// newestUIDs sorts uids ascending and returns the newest limit of them, newest first.
func newestUIDs(uids []uint32, limit int) []uint32 {
	sort.Slice(uids, func(i, j int) bool { return uids[i] < uids[j] })
	if len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	out := make([]uint32, len(uids))
	for i, uid := range uids {
		out[len(uids)-1-i] = uid
	}
	return out
}

// GetUnreadEmails retrieves recent unread emails without marking them as read.
func GetUnreadEmails(accountName string, cfg AccountConfig, folder string, limit int) ([]EmailSummary, error) {
	if cfg.IsGraph() {
		return GetUnreadEmailsGraph(accountName, cfg, folder, limit)
	}

	if folder == "" {
		folder = "INBOX"
	}
	limit = clampLimit(limit)

	c, err := DialIMAP(cfg, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// Select folder in ReadOnly mode so server doesn't mutate message flags
	if _, err = c.Select(folder, true); err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}

	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	return fetchSummaries(c, accountName, newestUIDs(uids, limit))
}

// SearchEmails searches for emails matching query in ReadOnly mode.
func SearchEmails(accountName string, cfg AccountConfig, query string, folder string, limit int) ([]EmailSummary, error) {
	if cfg.IsGraph() {
		return SearchEmailsGraph(accountName, cfg, query, folder, limit)
	}

	if folder == "" {
		folder = "INBOX"
	}
	limit = clampLimit(limit)

	c, err := DialIMAP(cfg, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	if _, err = c.Select(folder, true); err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	// 1. Try search with TEXT
	criteria := imap.NewSearchCriteria()
	criteria.Text = []string{query}
	uids, err := c.UidSearch(criteria)

	// Fall back to the union of Subject and From matches if TEXT search fails or finds nothing
	if err != nil || len(uids) == 0 {
		seen := make(map[uint32]bool)
		uids = nil
		for _, field := range []string{"Subject", "From"} {
			fallback := imap.NewSearchCriteria()
			fallback.Header.Add(field, query)
			found, ferr := c.UidSearch(fallback)
			if ferr != nil {
				continue
			}
			for _, uid := range found {
				if !seen[uid] {
					seen[uid] = true
					uids = append(uids, uid)
				}
			}
		}
	}

	return fetchSummaries(c, accountName, newestUIDs(uids, limit))
}

// fetchSummaries fetches envelopes and snippets for uids, returned in the same order as uids.
func fetchSummaries(c *client.Client, accountName string, uids []uint32) ([]EmailSummary, error) {
	if len(uids) == 0 {
		return []EmailSummary{}, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	// Fetch only the first few KB of each message (BODY.PEEK[]<0.N>): enough to
	// decode the start of the text part without downloading attachments.
	section := &imap.BodySectionName{
		Peek:    true,
		Partial: []int{0, snippetFetchBytes},
	}

	items := []imap.FetchItem{
		imap.FetchEnvelope,
		imap.FetchUid,
		imap.FetchInternalDate,
		section.FetchItem(),
	}

	messages := make(chan *imap.Message, len(uids))
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqset, items, messages)
	}()

	byUID := make(map[uint32]EmailSummary, len(uids))
	var retry []uint32
	for msg := range messages {
		summary := EmailSummary{
			Account: accountName,
			ID:      strconv.FormatUint(uint64(msg.Uid), 10),
		}

		if msg.Envelope != nil {
			summary.Subject = msg.Envelope.Subject
			summary.Date = msg.Envelope.Date
			summary.MessageID = msg.Envelope.MessageId
			if len(msg.Envelope.From) > 0 {
				summary.From = formatAddress(msg.Envelope.From[0])
			}
		}

		if summary.Date.IsZero() {
			summary.Date = msg.InternalDate
		}

		for _, literal := range msg.Body {
			if literal != nil {
				raw, _ := io.ReadAll(literal)
				summary.Snippet = extractSnippet(bytes.NewReader(raw), snippetLen)
				// Cut off before any readable text (e.g. a large CSS block): retry with more bytes
				if summary.Snippet == "" && len(raw) >= snippetFetchBytes {
					retry = append(retry, msg.Uid)
				}
				break
			}
		}

		byUID[msg.Uid] = summary
	}

	if err := <-done; err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	if len(retry) > 0 {
		// Snippets are best-effort; keep the summaries even if the retry fails
		if snippets, err := fetchSnippets(c, retry, snippetRetryBytes); err == nil {
			for uid, snippet := range snippets {
				if s, ok := byUID[uid]; ok {
					s.Snippet = snippet
					byUID[uid] = s
				}
			}
		}
	}

	// The server returns messages in mailbox order; restore the requested (newest-first) order
	summaries := make([]EmailSummary, 0, len(byUID))
	for _, uid := range uids {
		if s, ok := byUID[uid]; ok {
			summaries = append(summaries, s)
		}
	}
	return summaries, nil
}

// fetchSnippets fetches the first n bytes of each message and returns their decoded snippets by UID.
func fetchSnippets(c *client.Client, uids []uint32, n int) (map[uint32]string, error) {
	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true, Partial: []int{0, n}}

	messages := make(chan *imap.Message, len(uids))
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqset, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, messages)
	}()

	snippets := make(map[uint32]string, len(uids))
	for msg := range messages {
		for _, literal := range msg.Body {
			if literal != nil {
				snippets[msg.Uid] = extractSnippet(literal, snippetLen)
				break
			}
		}
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return snippets, nil
}

// ReadEmail fetches and parses the full email message for a given ID (an IMAP UID).
func ReadEmail(accountName string, cfg AccountConfig, id string, folder string, maxBodyLen int) (*EmailDetail, error) {
	if cfg.IsGraph() {
		return ReadEmailGraph(accountName, cfg, id, maxBodyLen)
	}

	if folder == "" {
		folder = "INBOX"
	}
	if maxBodyLen <= 0 {
		maxBodyLen = defaultMaxBodyLen
	}

	uid, parseErr := strconv.ParseUint(id, 10, 32)
	if parseErr != nil || uid == 0 {
		return nil, fmt.Errorf("invalid message ID '%s' for IMAP: must be a numeric UID", id)
	}

	c, err := DialIMAP(cfg, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// Select folder in ReadOnly mode
	if _, err = c.Select(folder, true); err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uint32(uid))

	// Request entire RFC822 message via BODY.PEEK[] so unread flag is preserved
	section := &imap.BodySectionName{Peek: true}
	items := []imap.FetchItem{
		imap.FetchEnvelope,
		imap.FetchInternalDate,
		imap.FetchUid,
		section.FetchItem(),
	}

	messages := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seqset, items, messages)
	}()

	msg := <-messages
	if err := <-done; err != nil {
		return nil, fmt.Errorf("fetch failed for message ID %s: %w", id, err)
	}

	if msg == nil {
		return nil, fmt.Errorf("message %s not found in %s", id, folder)
	}

	detail := &EmailDetail{
		Account: accountName,
		ID:      strconv.FormatUint(uint64(msg.Uid), 10),
		Headers: make(map[string]string),
	}

	if msg.Envelope != nil {
		detail.Subject = msg.Envelope.Subject
		detail.Date = msg.Envelope.Date
		detail.MessageID = msg.Envelope.MessageId

		if len(msg.Envelope.From) > 0 {
			detail.From = formatAddress(msg.Envelope.From[0])
		}
		for _, to := range msg.Envelope.To {
			detail.To = append(detail.To, formatAddress(to))
		}
		for _, cc := range msg.Envelope.Cc {
			detail.Cc = append(detail.Cc, formatAddress(cc))
		}
	}

	if detail.Date.IsZero() {
		detail.Date = msg.InternalDate
	}

	if r := msg.GetBody(section); r != nil {
		raw, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("failed to read message %s: %w", id, err)
		}

		pm, parseErr := parseMIME(bytes.NewReader(raw))
		if parseErr == nil {
			detail.Body = truncateBody(pm.Text(), maxBodyLen)
			detail.Attachments = pm.Attachments
			if len(pm.Headers) > 0 {
				detail.Headers = pm.Headers
			}
		} else {
			// Fallback: raw message with HTML stripping
			rawStr := string(raw)
			if strings.Contains(strings.ToLower(rawStr), "<html") {
				rawStr = StripHTML(rawStr)
			}
			detail.Body = truncateBody(rawStr, maxBodyLen)
		}
	}

	return detail, nil
}

func formatAddress(a *imap.Address) string {
	if a == nil {
		return ""
	}
	if a.PersonalName != "" {
		return fmt.Sprintf("%s <%s@%s>", a.PersonalName, a.MailboxName, a.HostName)
	}
	return fmt.Sprintf("%s@%s", a.MailboxName, a.HostName)
}
