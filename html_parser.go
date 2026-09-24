package main

import (
	"bytes"
	"html"
	"strings"

	golangHTML "golang.org/x/net/html"
)

// StripHTML converts HTML content into plain text with structure and whitespace preserved.
func StripHTML(htmlContent string) string {
	doc, err := golangHTML.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// Fallback to basic entity unescaping if parsing fails
		return html.UnescapeString(htmlContent)
	}

	var buf bytes.Buffer
	var extract func(*golangHTML.Node)
	extract = func(n *golangHTML.Node) {
		if n.Type == golangHTML.ElementNode {
			tag := strings.ToLower(n.Data)
			// Skip invisible elements
			if tag == "script" || tag == "style" || tag == "head" || tag == "svg" || tag == "noscript" {
				return
			}
		}

		if n.Type == golangHTML.TextNode {
			txt := strings.TrimSpace(n.Data)
			if txt != "" {
				buf.WriteString(n.Data)
				buf.WriteString(" ")
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}

		if n.Type == golangHTML.ElementNode {
			tag := strings.ToLower(n.Data)
			switch tag {
			case "p", "div", "br", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "blockquote":
				buf.WriteString("\n")
			}
		}
	}

	extract(doc)

	// Clean up whitespace: normalize multiple blank lines to at most two
	raw := html.UnescapeString(buf.String())
	lines := strings.Split(raw, "\n")
	var cleaned []string
	consecutiveEmpty := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			consecutiveEmpty++
			if consecutiveEmpty <= 1 {
				cleaned = append(cleaned, "")
			}
		} else {
			consecutiveEmpty = 0
			cleaned = append(cleaned, strings.Join(strings.Fields(trimmed), " "))
		}
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
