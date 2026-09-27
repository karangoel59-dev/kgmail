package main

import (
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	// Registers decoders for non-UTF-8 charsets (ISO-8859-*, Windows-125x, GBK, ...)
	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
)

const (
	defaultMaxBodyLen = 10000
	maxBodyLenLimit   = 200000
	defaultLimit      = 10
	maxLimit          = 50
	snippetLen        = 180
	// snippetFetchBytes is how much of each message is fetched to build a snippet;
	// snippetRetryBytes is used for messages whose text starts later than that.
	snippetFetchBytes = 16384
	snippetRetryBytes = 131072
)

// clampLimit applies the default and upper bound to a per-account result limit.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// truncateRunes cuts s to at most max characters without splitting a UTF-8 sequence.
func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s, false
	}
	i := 0
	for n := 0; n < max; n++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return s[:i], true
}

// truncateBody truncates an email body to maxLen characters, marking the cut.
func truncateBody(body string, maxLen int) string {
	if maxLen > maxBodyLenLimit {
		maxLen = maxBodyLenLimit
	}
	if cut, ok := truncateRunes(body, maxLen); ok {
		return cut + "\n...[truncated by kgmail]"
	}
	return body
}

// parsedMessage holds the readable parts of a MIME message.
type parsedMessage struct {
	Plain       string
	HTML        string // raw HTML, converted to text on demand
	Attachments []string
	Headers     map[string]string
}

// Text returns the plain-text body, falling back to the HTML body (with link targets).
func (p *parsedMessage) Text() string {
	return p.text(true)
}

func (p *parsedMessage) text(withLinks bool) string {
	if strings.TrimSpace(p.Plain) != "" {
		return strings.TrimSpace(p.Plain)
	}
	return strings.TrimSpace(stripHTML(p.HTML, withLinks))
}

// parseMIME decodes a raw RFC 5322 message (transfer encodings and charsets included).
// It tolerates truncated input, returning whatever it could decode, so it also
// works on the partial fetches used for snippets.
func parseMIME(r io.Reader) (*parsedMessage, error) {
	mr, err := mail.CreateReader(r)
	if mr == nil {
		return nil, err
	}

	pm := &parsedMessage{Headers: make(map[string]string)}
	for key := range mr.Header.Map() {
		pm.Headers[key] = mr.Header.Get(key)
	}

	var firstErr error
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Truncated input and unknown charsets are expected; keep what we have.
			if firstErr == nil {
				firstErr = err
			}
			break
		}

		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()
			// ReadAll returns the data it decoded even when the part is cut short.
			b, _ := io.ReadAll(p.Body)

			if contentType == "text/plain" || contentType == "" {
				if pm.Plain == "" {
					pm.Plain = string(b)
				}
			} else if contentType == "text/html" && pm.HTML == "" {
				pm.HTML = string(b)
			}
		case *mail.AttachmentHeader:
			if filename, _ := h.Filename(); filename != "" {
				pm.Attachments = append(pm.Attachments, filename)
			}
		}
	}

	if pm.Plain == "" && pm.HTML == "" && len(pm.Attachments) == 0 && firstErr != nil {
		return pm, firstErr
	}
	return pm, nil
}

// extractSnippet returns a short single-line preview of a (possibly truncated) raw message.
func extractSnippet(r io.Reader, maxLen int) string {
	pm, _ := parseMIME(r)
	if pm == nil {
		return ""
	}
	// Links would crowd out the text, and invisible formatting characters are
	// often used as preheader padding in marketing mail.
	text := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) || r == '\u034f' {
			return -1
		}
		return r
	}, pm.text(false))
	text = strings.Join(strings.Fields(text), " ")
	if cut, ok := truncateRunes(text, maxLen); ok {
		return cut + "..."
	}
	return text
}
