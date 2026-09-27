package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

const smtpTimeout = 60 * time.Second

// OutgoingEmail describes a message to send.
type OutgoingEmail struct {
	To      []string
	Cc      []string
	Bcc     []string
	Subject string
	Body    string
	IsHTML  bool
	// InReplyTo is the Message-ID of the email being replied to (angle brackets optional).
	// It sets the In-Reply-To and References headers so the reply is threaded.
	InReplyTo string
}

// parseAddresses parses each entry as an RFC 5322 address ("a@b.com" or "Name <a@b.com>").
// Parsing rejects CR/LF and other characters that could inject extra headers.
func parseAddresses(list []string) ([]*mail.Address, error) {
	var addrs []*mail.Address
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		a, err := mail.ParseAddress(s)
		if err != nil {
			return nil, fmt.Errorf("invalid email address %q: %w", s, err)
		}
		addrs = append(addrs, a)
	}
	return addrs, nil
}

// normalizeMsgID strips surrounding angle brackets and rejects values that aren't a single message ID.
func normalizeMsgID(id string) (string, error) {
	id = strings.TrimSpace(id)
	id = strings.TrimSuffix(strings.TrimPrefix(id, "<"), ">")
	if id == "" || strings.ContainsAny(id, "<> \t\r\n") || !strings.Contains(id, "@") {
		return "", fmt.Errorf("invalid Message-ID %q", id)
	}
	return id, nil
}

// buildMessage renders msg as an RFC 5322 message from the given sender.
// It returns the message bytes and the envelope recipients (To + Cc + Bcc).
func buildMessage(from string, msg OutgoingEmail) ([]byte, []string, error) {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid sender address %q: %w", from, err)
	}
	to, err := parseAddresses(msg.To)
	if err != nil {
		return nil, nil, err
	}
	cc, err := parseAddresses(msg.Cc)
	if err != nil {
		return nil, nil, err
	}
	bcc, err := parseAddresses(msg.Bcc)
	if err != nil {
		return nil, nil, err
	}
	if len(to)+len(cc)+len(bcc) == 0 {
		return nil, nil, fmt.Errorf("no recipients specified")
	}

	var h mail.Header
	h.SetDate(time.Now())
	h.SetAddressList("From", []*mail.Address{fromAddr})
	if len(to) > 0 {
		h.SetAddressList("To", to)
	}
	if len(cc) > 0 {
		h.SetAddressList("Cc", cc)
	}
	// Bcc recipients only go in the envelope, never in the headers.
	h.SetSubject(msg.Subject)
	if err := h.GenerateMessageIDWithHostname(fromAddr.Address[strings.LastIndex(fromAddr.Address, "@")+1:]); err != nil {
		return nil, nil, fmt.Errorf("failed to generate Message-ID: %w", err)
	}
	if msg.InReplyTo != "" {
		id, err := normalizeMsgID(msg.InReplyTo)
		if err != nil {
			return nil, nil, err
		}
		h.SetMsgIDList("In-Reply-To", []string{id})
		h.SetMsgIDList("References", []string{id})
	}

	contentType := "text/plain"
	if msg.IsHTML {
		contentType = "text/html"
	}
	h.SetContentType(contentType, map[string]string{"charset": "utf-8"})
	h.Set("Content-Transfer-Encoding", "quoted-printable")

	var buf bytes.Buffer
	w, err := mail.CreateSingleInlineWriter(&buf, h)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build message: %w", err)
	}
	if _, err := w.Write([]byte(msg.Body)); err != nil {
		return nil, nil, fmt.Errorf("failed to build message: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, nil, fmt.Errorf("failed to build message: %w", err)
	}

	var rcpts []string
	for _, list := range [][]*mail.Address{to, cc, bcc} {
		for _, a := range list {
			rcpts = append(rcpts, a.Address)
		}
	}
	return buf.Bytes(), rcpts, nil
}

func isLocalhost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// SendEmail transmits an email message via SMTP or Microsoft Graph API.
func SendEmail(cfg AccountConfig, msg OutgoingEmail) error {
	if cfg.IsGraph() {
		return SendEmailGraph(cfg, msg)
	}

	host := cfg.SMTPHost
	port := cfg.SMTPPort
	user := cfg.SMTPUser
	pass := cfg.SMTPPass

	if host == "" {
		host = cfg.Host
	}
	if port == 0 {
		port = 587
	}
	if user == "" {
		user = cfg.Username
	}
	if pass == "" {
		pass = cfg.Password
	}

	if host == "" || user == "" {
		return fmt.Errorf("SMTP configuration incomplete for user %s: missing host or username", cfg.Username)
	}

	// The From address is the mailbox itself; the SMTP login may be a different identifier.
	from := cfg.Username
	if !strings.Contains(from, "@") {
		from = user
	}

	data, rcpts, err := buildMessage(from, msg)
	if err != nil {
		return err
	}
	fromAddr, _ := mail.ParseAddress(from)

	var auth smtp.Auth
	if cfg.IsOAuth2() {
		token, tokenErr := GetOrRefreshMicrosoftToken(&cfg)
		if tokenErr != nil {
			return fmt.Errorf("SMTP OAuth2 token error for %s: %w", cfg.Username, tokenErr)
		}
		auth = newSMTPXOAuth2Auth(user, token)
	} else {
		if pass == "" {
			return fmt.Errorf("SMTP configuration incomplete for user %s: password required", cfg.Username)
		}
		auth = smtp.PlainAuth("", user, pass, host)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	tlsConfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	// Port 465 uses implicit TLS; other ports use STARTTLS
	var conn net.Conn
	if port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("SMTP dial %s error: %w", addr, err)
	}
	defer conn.Close()
	// Bound the whole exchange so an unresponsive server can't hang the caller
	if err := conn.SetDeadline(time.Now().Add(smtpTimeout)); err != nil {
		return fmt.Errorf("SMTP set deadline error: %w", err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP client error: %w", err)
	}
	defer c.Close()

	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err = c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("SMTP STARTTLS error: %w", err)
			}
		}
	}

	// Never send credentials or OAuth tokens over an unencrypted connection
	if _, ok := c.TLSConnectionState(); !ok && !isLocalhost(host) {
		return fmt.Errorf("SMTP server %s does not support TLS; refusing to send credentials in plaintext", addr)
	}

	if err = c.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth error: %w", err)
	}

	if err = c.Mail(fromAddr.Address); err != nil {
		return fmt.Errorf("SMTP MAIL FROM error: %w", err)
	}
	for _, rcpt := range rcpts {
		if err = c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s error: %w", rcpt, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA error: %w", err)
	}
	if _, err = w.Write(data); err != nil {
		return fmt.Errorf("SMTP body write error: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("SMTP data close error: %w", err)
	}

	return c.Quit()
}
