package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"
)

func writeFile(t *testing.T, path string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAttachments_AllowedDirs(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "Documents")
	outside := filepath.Join(base, "secret.txt")
	writeFile(t, filepath.Join(allowed, "report.pdf"), "%PDF-1.4")
	writeFile(t, filepath.Join(allowed, "sub", "notes.txt"), "hello")
	writeFile(t, filepath.Join(allowed, ".env"), "TOKEN=x")
	writeFile(t, filepath.Join(allowed, ".hidden", "key.pem"), "key")
	writeFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(allowed, "link.txt")); err != nil {
		t.Fatal(err)
	}
	dirs := []string{allowed}

	got, err := loadAttachments([]string{filepath.Join(allowed, "report.pdf"), filepath.Join(allowed, "sub", "notes.txt")}, dirs)
	if err != nil {
		t.Fatalf("allowed files rejected: %v", err)
	}
	if len(got) != 2 || got[0].Name != "report.pdf" || got[0].ContentType != "application/pdf" || string(got[1].Data) != "hello" {
		t.Errorf("unexpected attachments: %+v", got)
	}

	rejected := map[string]string{
		"outside folder":       outside,
		"hidden file":          filepath.Join(allowed, ".env"),
		"hidden folder":        filepath.Join(allowed, ".hidden", "key.pem"),
		"symlink escape":       filepath.Join(allowed, "link.txt"),
		"directory":            filepath.Join(allowed, "sub"),
		"missing file":         filepath.Join(allowed, "nope.pdf"),
		"traversal out of dir": filepath.Join(allowed, "..", "secret.txt"),
	}
	for name, path := range rejected {
		if _, err := loadAttachments([]string{path}, dirs); err == nil {
			t.Errorf("%s: expected %s to be rejected", name, path)
		}
	}

	// The CLI passes nil: any readable regular file is allowed
	if _, err := loadAttachments([]string{outside}, nil); err != nil {
		t.Errorf("CLI attachment rejected: %v", err)
	}
}

func TestLoadAttachments_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file: the size check happens before the file is read
	if err := f.Truncate(maxAttachmentBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := loadAttachments([]string{big}, nil); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Errorf("expected size limit error, got %v", err)
	}
}

func TestBuildMessage_WithAttachments(t *testing.T) {
	pdf := []byte("%PDF-1.4 binary \x00\x01\x02 data")
	data, rcpts, err := buildMessage("me@example.com", OutgoingEmail{
		To:      []string{"you@example.com"},
		Subject: "Report",
		Body:    "See attached.\nThanks",
		Attachments: []Attachment{
			{Name: "report.pdf", ContentType: "application/pdf", Data: pdf},
			{Name: "résumé notes.txt", ContentType: "text/plain; charset=utf-8", Data: []byte("héllo")},
		},
	})
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if len(rcpts) != 1 {
		t.Errorf("rcpts = %v", rcpts)
	}

	mr, err := mail.CreateReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse built message: %v", err)
	}
	if ct, _, _ := mr.Header.ContentType(); ct != "multipart/mixed" {
		t.Errorf("top-level Content-Type = %q, want multipart/mixed", ct)
	}
	if cte := mr.Header.Get("Content-Transfer-Encoding"); cte != "" {
		t.Errorf("multipart message must not have a top-level Content-Transfer-Encoding, got %q", cte)
	}

	var body string
	files := map[string][]byte{}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		b, _ := io.ReadAll(p.Body)
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			body = string(b)
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			files[name] = b
		}
	}
	if strings.ReplaceAll(body, "\r\n", "\n") != "See attached.\nThanks" {
		t.Errorf("body = %q", body)
	}
	if !bytes.Equal(files["report.pdf"], pdf) {
		t.Errorf("report.pdf round-trip mismatch: %q", files["report.pdf"])
	}
	if string(files["résumé notes.txt"]) != "héllo" {
		t.Errorf("non-ASCII filename attachment missing or wrong: %v", files)
	}
}

func TestGraphFileAttachments(t *testing.T) {
	files, err := graphFileAttachments([]Attachment{{Name: "a.txt", ContentType: "text/plain", Data: []byte("hi")}})
	if err != nil {
		t.Fatalf("graphFileAttachments: %v", err)
	}
	if files[0]["@odata.type"] != "#microsoft.graph.fileAttachment" || files[0]["contentBytes"] != base64.StdEncoding.EncodeToString([]byte("hi")) {
		t.Errorf("unexpected payload: %v", files[0])
	}
	big := Attachment{Name: "big.bin", ContentType: "application/octet-stream", Data: make([]byte, maxGraphAttachmentBytes+1)}
	if _, err := graphFileAttachments([]Attachment{big}); err == nil {
		t.Errorf("expected Graph size limit error")
	}
}
