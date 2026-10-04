package main

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

const (
	// maxAttachmentBytes bounds the combined size of all attachments. Base64 encoding
	// adds a third, which keeps the message under the common 25 MB provider limit.
	maxAttachmentBytes = 18 << 20
	// maxGraphAttachmentBytes is the combined limit for Microsoft Graph accounts: sendMail
	// takes attachments inline in a request that must stay under 4 MB.
	maxGraphAttachmentBytes = 3 << 20
)

// Attachment is a file attached to an outgoing email.
type Attachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// defaultAttachmentDirs are the folders MCP clients may attach files from when the
// config doesn't set attachment_dirs. The last one is where the Google Drive MCP
// server saves downloads.
func defaultAttachmentDirs() []string {
	return []string{"~/Downloads", "~/Documents", "~/Desktop", "~/.workspace-mcp/attachments"}
}

// expandHome replaces a leading "~" with the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

// checkAttachmentPath verifies that path (already symlink-resolved) lies inside one of
// allowedDirs and doesn't pass through a hidden file or folder below it, so an agent
// can't be steered into attaching secrets such as ~/.ssh keys or kgmail's own config.
func checkAttachmentPath(path string, allowedDirs []string) error {
	for _, dir := range allowedDirs {
		root, err := filepath.EvalSymlinks(expandHome(dir))
		if err != nil {
			continue // folder doesn't exist
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				return fmt.Errorf("attachment %s is a hidden file or inside a hidden folder", path)
			}
		}
		return nil
	}
	return fmt.Errorf("attachment %s is outside the allowed folders (%s); move it there or add its folder to attachment_dirs in the kgmail config",
		path, strings.Join(allowedDirs, ", "))
}

// loadAttachments reads the files at paths. When allowedDirs is non-nil, each file must
// resolve (after following symlinks) to a location inside one of those folders.
func loadAttachments(paths []string, allowedDirs []string) ([]Attachment, error) {
	var attachments []Attachment
	total := 0
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = expandHome(p)
		if !filepath.IsAbs(p) {
			abs, err := filepath.Abs(p)
			if err != nil {
				return nil, fmt.Errorf("invalid attachment path %q: %w", p, err)
			}
			p = abs
		}

		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return nil, fmt.Errorf("cannot read attachment %s: %w", p, err)
		}
		if allowedDirs != nil {
			if err := checkAttachmentPath(real, allowedDirs); err != nil {
				return nil, err
			}
		}

		info, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("cannot read attachment %s: %w", p, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("attachment %s is not a regular file", p)
		}
		total += int(info.Size())
		if total > maxAttachmentBytes {
			return nil, fmt.Errorf("attachments exceed %d MB in total", maxAttachmentBytes>>20)
		}

		data, err := os.ReadFile(real)
		if err != nil {
			return nil, fmt.Errorf("cannot read attachment %s: %w", p, err)
		}

		name := filepath.Base(p)
		contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		attachments = append(attachments, Attachment{Name: name, ContentType: contentType, Data: data})
	}
	return attachments, nil
}

// attachmentDirs returns the folders MCP clients may attach files from.
func (c *Config) attachmentDirs() []string {
	if len(c.AttachmentDirs) > 0 {
		return c.AttachmentDirs
	}
	return defaultAttachmentDirs()
}

// formatSize renders a byte count as a short human-readable string.
func formatSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// describeAttachments lists attachment names and sizes, e.g. "a.pdf (120 KB), b.png (2.1 MB)".
func describeAttachments(attachments []Attachment) string {
	parts := make([]string, len(attachments))
	for i, a := range attachments {
		parts[i] = fmt.Sprintf("%s (%s)", a.Name, formatSize(len(a.Data)))
	}
	return strings.Join(parts, ", ")
}
