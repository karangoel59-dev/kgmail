package main

import (
	"bytes"
	"html"
	"strings"

	golangHTML "golang.org/x/net/html"
)

// StripHTML converts HTML content into plain text with structure and whitespace preserved.
// Link targets are kept as "text (url)".
func StripHTML(htmlContent string) string {
	return stripHTML(htmlContent, true)
}

func stripHTML(htmlContent string, withLinks bool) string {
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
			if tag == "a" && withLinks {
				writeLinkTarget(&buf, n)
			}
			switch tag {
			case "p", "div", "br", "tr", "li", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "blockquote":
				buf.WriteString("\n")
			}
		}
	}

	extract(doc)

	// Clean up whitespace: normalize multiple blank lines to at most two.
	// Text nodes are already entity-decoded by the parser, so no further unescaping.
	raw := buf.String()
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

// writeLinkTarget appends " (url)" after a link's text so readers (and agents)
// can see where it points. It is skipped when the text already is the URL.
func writeLinkTarget(buf *bytes.Buffer, n *golangHTML.Node) {
	var href string
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "href") {
			href = strings.TrimSpace(a.Val)
			break
		}
	}
	lower := strings.ToLower(href)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return
	}
	if text := strings.TrimSpace(nodeText(n)); text == href || text == "" {
		return
	}
	buf.WriteString("(")
	buf.WriteString(href)
	buf.WriteString(") ")
}

func nodeText(n *golangHTML.Node) string {
	if n.Type == golangHTML.TextNode {
		return n.Data
	}
	var sb strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		sb.WriteString(nodeText(c))
	}
	return sb.String()
}
