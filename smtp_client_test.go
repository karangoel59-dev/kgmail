package main

import (
	"strings"
	"testing"
)

func TestBuildMessage_HeaderInjection(t *testing.T) {
	data, _, err := buildMessage("me@example.com", OutgoingEmail{
		To:      []string{"you@example.com"},
		Subject: "Hello\r\nBcc: attacker@evil.com",
		Body:    "hi",
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	header := string(data[:strings.Index(string(data), "\r\n\r\n")])
	for _, line := range strings.Split(header, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("subject injected a Bcc header:\n%s", header)
		}
	}
}

func TestBuildMessage_RejectsInjectedAddress(t *testing.T) {
	_, _, err := buildMessage("me@example.com", OutgoingEmail{
		To:      []string{"you@example.com\r\nBcc: attacker@evil.com"},
		Subject: "x",
		Body:    "hi",
	})
	if err == nil {
		t.Fatalf("expected an error for an address containing CRLF")
	}
}

func TestBuildMessage_RecipientsAndThreading(t *testing.T) {
	data, rcpts, err := buildMessage("me@example.com", OutgoingEmail{
		To:        []string{"Alice <alice@example.com>"},
		Cc:        []string{"bob@example.com"},
		Bcc:       []string{"secret@example.com"},
		Subject:   "Re: Café ☕",
		Body:      "line one\nline two",
		InReplyTo: "<orig-123@example.com>",
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	msg := string(data)

	want := []string{"alice@example.com", "bob@example.com", "secret@example.com"}
	if strings.Join(rcpts, ",") != strings.Join(want, ",") {
		t.Errorf("envelope recipients = %v, want %v", rcpts, want)
	}
	if strings.Contains(msg, "secret@example.com") {
		t.Errorf("Bcc address leaked into message headers:\n%s", msg)
	}
	if !strings.Contains(msg, "In-Reply-To: <orig-123@example.com>") || !strings.Contains(msg, "References: <orig-123@example.com>") {
		t.Errorf("missing threading headers:\n%s", msg)
	}
	if !strings.Contains(msg, "Subject: =?utf-8?") {
		t.Errorf("expected non-ASCII subject to be RFC 2047 encoded:\n%s", msg)
	}
	if !strings.Contains(msg, "line one\r\nline two") {
		t.Errorf("expected CRLF line endings in body:\n%q", msg)
	}
}

func TestBuildMessage_RequiresRecipients(t *testing.T) {
	if _, _, err := buildMessage("me@example.com", OutgoingEmail{Subject: "x", Body: "y"}); err == nil {
		t.Fatalf("expected error with no recipients")
	}
}

func TestNormalizeMsgID(t *testing.T) {
	if id, err := normalizeMsgID(" <abc@host> "); err != nil || id != "abc@host" {
		t.Errorf("normalizeMsgID = %q, %v", id, err)
	}
	for _, bad := range []string{"", "abc", "<a@b>\r\nBcc: x@y", "a@b> <c@d"} {
		if _, err := normalizeMsgID(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}
