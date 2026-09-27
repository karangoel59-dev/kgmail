package main

import (
	"strings"
	"testing"
)

func TestStripHTML(t *testing.T) {
	input := `
		<html>
		<head><title>Test</title><style>.hidden { display: none; }</style></head>
		<body>
			<script>alert("bad");</script>
			<h1>Welcome</h1>
			<p>This is a <b>test</b> email with a <a href="https://example.com">link</a>.</p>
			<div>Second line of content.</div>
		</body>
		</html>
	`

	output := StripHTML(input)

	if strings.Contains(output, "alert") {
		t.Errorf("expected script content to be removed, got: %s", output)
	}
	if strings.Contains(output, "display: none") {
		t.Errorf("expected style content to be removed, got: %s", output)
	}
	if !strings.Contains(output, "Welcome") {
		t.Errorf("expected 'Welcome' to be present, got: %s", output)
	}
	if !strings.Contains(output, "This is a test email") {
		t.Errorf("expected paragraph content, got: %s", output)
	}
	if !strings.Contains(output, "Second line of content.") {
		t.Errorf("expected div content, got: %s", output)
	}
}

func TestStripHTML_EmptyAndPlain(t *testing.T) {
	if StripHTML("") != "" {
		t.Errorf("expected empty string")
	}

	plain := "Hello world, this is plain text."
	if StripHTML(plain) != plain {
		t.Errorf("expected plain text to be preserved, got: %s", StripHTML(plain))
	}
}

func TestStripHTML_LinksAndEntities(t *testing.T) {
	out := StripHTML(`<p>Click <a href="https://example.com/verify">here</a> or visit <a href="https://example.com">https://example.com</a>. Literal: &amp;lt;b&amp;gt;</p>`)

	if !strings.Contains(out, "here (https://example.com/verify)") {
		t.Errorf("expected link target after link text, got: %s", out)
	}
	if strings.Count(out, "https://example.com ") > 1 || strings.Contains(out, "(https://example.com)") {
		t.Errorf("expected bare-URL link not to be duplicated, got: %s", out)
	}
	if !strings.Contains(out, "&lt;b&gt;") {
		t.Errorf("expected entities to be decoded exactly once, got: %s", out)
	}
}
