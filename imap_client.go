package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
)

const (
	defaultTimeout   = 10 * time.Second
	defaultMaxBodyLen = 10000
)

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

	addr := fmt.Sprintf("%s:%d", host, port)
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

// GetUnreadEmails retrieves recent unread emails without marking them as read.
func GetUnreadEmails(accountName string, cfg AccountConfig, folder string, limit int) ([]EmailSummary, error) {
	if cfg.IsGraph() {
		return GetUnreadEmailsGraph(accountName, cfg, folder, limit)
	}

	if folder == "" {
		folder = "INBOX"
	}
	if limit <= 0 {
		limit = 10
	}

	c, err := DialIMAP(cfg, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// Select folder in ReadOnly mode so server doesn't mutate message flags
	_, err = c.Select(folder, true)
	if err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}

	ids, err := c.Search(criteria)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if len(ids) == 0 {
		return []EmailSummary{}, nil
	}

	// Slice most recent IDs
	startIdx := 0
	if len(ids) > limit {
		startIdx = len(ids) - limit
	}
	targetIDs := ids[startIdx:]

	// Reverse so newest appears first
	for i, j := 0, len(targetIDs)-1; i < j; i, j = i+1, j-1 {
		targetIDs[i], targetIDs[j] = targetIDs[j], targetIDs[i]
	}

	return fetchSummaries(c, accountName, targetIDs)
}

// SearchEmails searches for emails matching query in ReadOnly mode.
func SearchEmails(accountName string, cfg AccountConfig, query string, folder string, limit int) ([]EmailSummary, error) {
	if cfg.IsGraph() {
		return SearchEmailsGraph(accountName, cfg, query, folder, limit)
	}

	if folder == "" {
		folder = "INBOX"
	}
	if limit <= 0 {
		limit = 10
	}

	c, err := DialIMAP(cfg, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	_, err = c.Select(folder, true)
	if err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	// 1. Try search with TEXT
	criteria := imap.NewSearchCriteria()
	criteria.Text = []string{query}
	ids, err := c.Search(criteria)

	// Fallback to searching header fields if TEXT search fails or returns nothing
	if err != nil || len(ids) == 0 {
		fallback1 := imap.NewSearchCriteria()
		fallback1.Header.Add("Subject", query)
		ids1, err1 := c.Search(fallback1)

		fallback2 := imap.NewSearchCriteria()
		fallback2.Header.Add("From", query)
		ids2, err2 := c.Search(fallback2)

		if err1 == nil && len(ids1) > 0 {
			ids = ids1
		} else if err2 == nil && len(ids2) > 0 {
			ids = ids2
		}
	}

	if len(ids) == 0 {
		return []EmailSummary{}, nil
	}

	// Slice most recent IDs
	startIdx := 0
	if len(ids) > limit {
		startIdx = len(ids) - limit
	}
	targetIDs := ids[startIdx:]

	// Reverse for newest first
	for i, j := 0, len(targetIDs)-1; i < j; i, j = i+1, j-1 {
		targetIDs[i], targetIDs[j] = targetIDs[j], targetIDs[i]
	}

	return fetchSummaries(c, accountName, targetIDs)
}

func fetchSummaries(c *client.Client, accountName string, ids []uint32) ([]EmailSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(ids...)

	// Fetch envelope, UID, internal date, and body snippet using BODY.PEEK[TEXT]
	section := &imap.BodySectionName{
		BodyPartName: imap.BodyPartName{
			Specifier: imap.TextSpecifier,
		},
		Peek: true,
	}

	items := []imap.FetchItem{
		imap.FetchEnvelope,
		imap.FetchUid,
		imap.FetchInternalDate,
		section.FetchItem(),
	}

	messages := make(chan *imap.Message, len(ids))
	done := make(chan error, 1)
	go func() {
		done <- c.Fetch(seqset, items, messages)
	}()

	var summaries []EmailSummary
	for msg := range messages {
		summary := EmailSummary{
			Account: accountName,
			ID:      fmt.Sprintf("%d", msg.SeqNum),
		}

		if msg.Envelope != nil {
			summary.Subject = msg.Envelope.Subject
			summary.Date = msg.Envelope.Date
			summary.MessageID = msg.Envelope.MessageId

			if len(msg.Envelope.From) > 0 {
				from := msg.Envelope.From[0]
				if from.PersonalName != "" {
					summary.From = fmt.Sprintf("%s <%s@%s>", from.PersonalName, from.MailboxName, from.HostName)
				} else {
					summary.From = fmt.Sprintf("%s@%s", from.MailboxName, from.HostName)
				}
			}
		}

		if summary.Date.IsZero() {
			summary.Date = msg.InternalDate
		}

		// Extract snippet from body
		for _, literal := range msg.Body {
			if literal != nil {
				snippet := extractSnippet(literal, 180)
				if snippet != "" {
					summary.Snippet = snippet
					break
				}
			}
		}

		summaries = append(summaries, summary)
	}

	if err := <-done; err != nil {
		return nil, fmt.Errorf("failed to fetch messages: %w", err)
	}

	return summaries, nil
}

// ReadEmail fetches and parses the full email message for a given ID.
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

	numID, parseErr := strconv.ParseUint(id, 10, 32)
	if parseErr != nil {
		return nil, fmt.Errorf("invalid message ID '%s' for IMAP: must be numeric sequence number", id)
	}

	c, err := DialIMAP(cfg, 20*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// Select folder in ReadOnly mode
	_, err = c.Select(folder, true)
	if err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uint32(numID))

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
		done <- c.Fetch(seqset, items, messages)
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
		ID:      fmt.Sprintf("%d", msg.SeqNum),
		Headers: make(map[string]string),
	}

	if msg.Envelope != nil {
		detail.Subject = msg.Envelope.Subject
		detail.Date = msg.Envelope.Date
		detail.MessageID = msg.Envelope.MessageId

		if len(msg.Envelope.From) > 0 {
			f := msg.Envelope.From[0]
			detail.From = formatAddress(f)
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

	// Parse full RFC822 literal
	r := msg.GetBody(section)
	if r != nil {
		body, attachments, headers, parseErr := parseRFC822Message(r, maxBodyLen)
		if parseErr == nil {
			detail.Body = body
			detail.Attachments = attachments
			if len(headers) > 0 {
				detail.Headers = headers
			}
		} else {
			// Fallback: raw read with HTML stripping
			raw, _ := io.ReadAll(r)
			rawStr := string(raw)
			if strings.Contains(strings.ToLower(rawStr), "<html") {
				detail.Body = StripHTML(rawStr)
			} else {
				detail.Body = rawStr
			}
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

func parseRFC822Message(r io.Reader, maxLen int) (string, []string, map[string]string, error) {
	mr, err := mail.CreateReader(r)
	if err != nil {
		return "", nil, nil, err
	}

	headers := make(map[string]string)
	header := mr.Header
	for key := range header.Map() {
		headers[key] = header.Get(key)
	}

	var plainBody, htmlBody string
	var attachments []string

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			break
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			b, _ := io.ReadAll(p.Body)
			content := string(b)

			if strings.HasPrefix(contentType, "text/plain") && plainBody == "" {
				plainBody = content
			} else if strings.HasPrefix(contentType, "text/html") && htmlBody == "" {
				htmlBody = StripHTML(content)
			}
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			if filename != "" {
				attachments = append(attachments, filename)
			}
		}
	}

	finalBody := plainBody
	if finalBody == "" {
		finalBody = htmlBody
	}

	finalBody = strings.TrimSpace(finalBody)
	if len(finalBody) > maxLen {
		finalBody = finalBody[:maxLen] + "\n...[truncated by kgmail]"
	}

	return finalBody, attachments, headers, nil
}

func extractSnippet(r io.Reader, maxLen int) string {
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	if n == 0 {
		return ""
	}
	raw := string(buf[:n])
	lines := strings.Split(raw, "\n")
	var contentLines []string
	inHeaders := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			inHeaders = true
			continue
		}
		if inHeaders {
			if trimmed == "" {
				inHeaders = false
			}
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "content-type:") ||
			strings.HasPrefix(lower, "content-transfer-encoding:") ||
			strings.HasPrefix(lower, "content-disposition:") {
			continue
		}
		if trimmed != "" {
			contentLines = append(contentLines, trimmed)
		}
	}

	text := strings.Join(contentLines, " ")
	if strings.Contains(strings.ToLower(text), "<html") || strings.Contains(text, "<div") || strings.Contains(text, "<p") {
		text = StripHTML(text)
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > maxLen {
		return text[:maxLen] + "..."
	}
	return text
}
