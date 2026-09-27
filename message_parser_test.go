package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

const multipartBase64Msg = "From: a@example.com\r\n" +
	"To: b@example.com\r\n" +
	"Subject: test\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=XYZ\r\n" +
	"\r\n" +
	"--XYZ\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"SGVsbG8gZnJvbSBiYXNlNjQh\r\n" + // "Hello from base64!"
	"--XYZ\r\n" +
	"Content-Type: application/pdf\r\n" +
	"Content-Disposition: attachment; filename=\"invoice.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQK\r\n" +
	"--XYZ--\r\n"

func TestParseMIME_Base64AndAttachment(t *testing.T) {
	pm, err := parseMIME(strings.NewReader(multipartBase64Msg))
	if err != nil {
		t.Fatalf("parseMIME: %v", err)
	}
	if pm.Text() != "Hello from base64!" {
		t.Errorf("Text() = %q", pm.Text())
	}
	if len(pm.Attachments) != 1 || pm.Attachments[0] != "invoice.pdf" {
		t.Errorf("Attachments = %v", pm.Attachments)
	}
}

func TestParseMIME_Latin1Charset(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nCaf=E9\r\n"
	pm, err := parseMIME(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parseMIME: %v", err)
	}
	if pm.Text() != "Café" {
		t.Errorf("Text() = %q, want Café", pm.Text())
	}
}

func TestExtractSnippet_TruncatedMessage(t *testing.T) {
	// Simulate a partial fetch that cuts the message off mid-attachment
	cut := multipartBase64Msg[:strings.Index(multipartBase64Msg, "JVBER")+4]
	if got := extractSnippet(strings.NewReader(cut), 180); got != "Hello from base64!" {
		t.Errorf("extractSnippet = %q", got)
	}
}

func TestExtractSnippet_HTMLOnly(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<html><body><p>Hi <b>there</b></p></body></html>\r\n"
	if got := extractSnippet(strings.NewReader(raw), 180); got != "Hi there" {
		t.Errorf("extractSnippet = %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	s := "héllo wörld 👋"
	cut, ok := truncateRunes(s, 12)
	if !ok || cut != "héllo wörld " {
		t.Errorf("truncateRunes = %q, %v", cut, ok)
	}
	for n := 0; n <= utf8.RuneCountInString(s); n++ {
		if cut, _ := truncateRunes(s, n); !utf8.ValidString(cut) {
			t.Errorf("truncateRunes(%d) produced invalid UTF-8", n)
		}
	}
	if cut, ok := truncateRunes("short", 10); ok || cut != "short" {
		t.Errorf("expected no truncation, got %q, %v", cut, ok)
	}
}

func TestClampLimit(t *testing.T) {
	cases := map[int]int{0: defaultLimit, -5: defaultLimit, 7: 7, 5000: maxLimit}
	for in, want := range cases {
		if got := clampLimit(in); got != want {
			t.Errorf("clampLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestNewestUIDs(t *testing.T) {
	got := newestUIDs([]uint32{5, 1, 9, 3, 7}, 3)
	want := []uint32{9, 7, 5}
	if len(got) != len(want) {
		t.Fatalf("newestUIDs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("newestUIDs = %v, want %v", got, want)
		}
	}
}

func TestExtractSnippet_DropsLinksAndInvisibleChars(t *testing.T) {
	raw := "Subject: x\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>Sale ͏‌ ͏‌ ends <a href=\"https://track.example.com/very/long\">today</a></p>\r\n"
	if got := extractSnippet(strings.NewReader(raw), 180); got != "Sale ends today" {
		t.Errorf("extractSnippet = %q", got)
	}
}
