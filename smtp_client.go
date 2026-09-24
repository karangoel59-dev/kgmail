package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SendEmail transmits an email message via SMTP or Microsoft Graph API.
func SendEmail(cfg AccountConfig, to []string, subject, body string, isHTML bool) error {
	if cfg.IsGraph() {
		return SendEmailGraph(cfg, to, subject, body, isHTML)
	}

	if len(to) == 0 {
		return fmt.Errorf("no recipients specified")
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

	addr := fmt.Sprintf("%s:%d", host, port)
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

	contentType := "text/plain; charset=UTF-8"
	if isHTML {
		contentType = "text/html; charset=UTF-8"
	}

	header := make(map[string]string)
	header["From"] = user
	header["To"] = strings.Join(to, ", ")
	header["Subject"] = subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = contentType
	header["Date"] = time.Now().Format(time.RFC1123Z)

	message := ""
	for k, v := range header {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n" + body

	// Port 465 uses direct SSL/TLS; Port 587 uses STARTTLS
	if port == 465 {
		tlsConfig := &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("SSL dial error: %w", err)
		}
		defer conn.Close()

		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return fmt.Errorf("SMTP client error: %w", err)
		}
		defer c.Close()

		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth error: %w", err)
		}

		if err = c.Mail(user); err != nil {
			return fmt.Errorf("SMTP MAIL FROM error: %w", err)
		}
		for _, addr := range to {
			if err = c.Rcpt(addr); err != nil {
				return fmt.Errorf("SMTP RCPT TO %s error: %w", addr, err)
			}
		}

		w, err := c.Data()
		if err != nil {
			return fmt.Errorf("SMTP DATA error: %w", err)
		}
		_, err = w.Write([]byte(message))
		if err != nil {
			return fmt.Errorf("SMTP write error: %w", err)
		}
		if err = w.Close(); err != nil {
			return fmt.Errorf("SMTP data close error: %w", err)
		}

		return c.Quit()
	}

	// Standard STARTTLS connection
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("SMTP dial error: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP client error: %w", err)
	}
	defer c.Close()

	tlsConfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err = c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("SMTP STARTTLS error: %w", err)
		}
	}

	if err = c.Auth(auth); err != nil {
		return fmt.Errorf("SMTP auth error: %w", err)
	}

	if err = c.Mail(user); err != nil {
		return fmt.Errorf("SMTP MAIL error: %w", err)
	}
	for _, addr := range to {
		if err = c.Rcpt(addr); err != nil {
			return fmt.Errorf("SMTP RCPT %s error: %w", addr, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA error: %w", err)
	}
	_, err = w.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("SMTP body write error: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("SMTP data close error: %w", err)
	}

	return c.Quit()
}
