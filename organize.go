package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// maxMoveIDs bounds how many messages a single move request may touch.
const maxMoveIDs = 500

// MovedEmail records one moved message. NewID is its ID in the destination folder
// when the server reports it (Microsoft Graph does; IMAP MOVE does not).
type MovedEmail struct {
	ID    string `json:"id"`
	NewID string `json:"new_id,omitempty"`
}

// MoveResult summarizes a move request.
type MoveResult struct {
	Moved    []MovedEmail `json:"moved"`
	NotFound []string     `json:"not_found,omitempty"`
}

// parseUIDs validates IMAP message IDs (UIDs), dropping duplicates while keeping order.
func parseUIDs(ids []string) ([]uint32, error) {
	seen := make(map[uint32]bool)
	var uids []uint32
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		uid, err := strconv.ParseUint(id, 10, 32)
		if err != nil || uid == 0 {
			return nil, fmt.Errorf("invalid message ID '%s' for IMAP: must be a numeric UID", id)
		}
		if !seen[uint32(uid)] {
			seen[uint32(uid)] = true
			uids = append(uids, uint32(uid))
		}
	}
	return uids, nil
}

// cleanIDs trims IDs, drops empties and duplicates, and enforces maxMoveIDs.
func cleanIDs(ids []string) ([]string, error) {
	seen := make(map[string]bool)
	var out []string
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no message IDs given")
	}
	if len(out) > maxMoveIDs {
		return nil, fmt.Errorf("too many message IDs (%d); move at most %d at a time", len(out), maxMoveIDs)
	}
	return out, nil
}

// imapMailboxExists reports whether a mailbox with exactly this name exists.
func imapMailboxExists(c *client.Client, name string) (bool, error) {
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", name, mailboxes)
	}()

	found := false
	for m := range mailboxes {
		// INBOX is case-insensitive per RFC 3501; other names are exact
		if m.Name == name || (strings.EqualFold(name, "INBOX") && strings.EqualFold(m.Name, "INBOX")) {
			found = true
		}
	}
	if err := <-done; err != nil {
		return false, fmt.Errorf("failed to list mailboxes: %w", err)
	}
	return found, nil
}

// CreateFolder creates a mail folder. Nested folders use "/" (e.g. "Receipts/2026").
// It reports false when the folder already existed.
func CreateFolder(cfg AccountConfig, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("folder name is required")
	}
	if cfg.IsGraph() {
		return CreateFolderGraph(cfg, name)
	}

	c, err := DialIMAP(cfg, 20*time.Second)
	if err != nil {
		return false, err
	}
	defer c.Logout()

	exists, err := imapMailboxExists(c, name)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	if err := c.Create(name); err != nil {
		return false, fmt.Errorf("failed to create folder %s: %w", name, err)
	}
	// Some clients only show subscribed folders; failure here is harmless.
	_ = c.Subscribe(name)
	return true, nil
}

// MoveEmails moves messages (by the IDs returned from unread/search) from folder to dest.
// Flags such as unread are preserved. With createIfMissing, dest is created first if needed.
func MoveEmails(cfg AccountConfig, ids []string, folder, dest string, createIfMissing bool) (*MoveResult, error) {
	ids, err := cleanIDs(ids)
	if err != nil {
		return nil, err
	}
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return nil, fmt.Errorf("destination folder is required")
	}
	if cfg.IsGraph() {
		return MoveEmailsGraph(cfg, ids, dest, createIfMissing)
	}

	if folder == "" {
		folder = "INBOX"
	}
	if folder == dest {
		return nil, fmt.Errorf("source and destination folder are the same (%s)", dest)
	}
	uids, err := parseUIDs(ids)
	if err != nil {
		return nil, err
	}

	c, err := DialIMAP(cfg, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	// Without MOVE, go-imap falls back to COPY + \Deleted + EXPUNGE, and a plain EXPUNGE
	// would also permanently remove any other message already flagged \Deleted.
	if ok, err := c.Support("MOVE"); err != nil || !ok {
		return nil, fmt.Errorf("server does not support IMAP MOVE; refusing the copy+expunge fallback because it could permanently delete other messages")
	}

	exists, err := imapMailboxExists(c, dest)
	if err != nil {
		return nil, err
	}
	if !exists {
		if !createIfMissing {
			return nil, fmt.Errorf("folder %q does not exist (use list_folders to see existing folders, or create it first)", dest)
		}
		if err := c.Create(dest); err != nil {
			return nil, fmt.Errorf("failed to create folder %s: %w", dest, err)
		}
		_ = c.Subscribe(dest)
	}

	if _, err := c.Select(folder, false); err != nil {
		return nil, fmt.Errorf("failed to select folder %s: %w", folder, err)
	}

	// UID MOVE silently skips unknown UIDs, so look them up first to report what wasn't found
	requested := new(imap.SeqSet)
	requested.AddNum(uids...)
	criteria := imap.NewSearchCriteria()
	criteria.Uid = requested
	found, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("failed to look up messages: %w", err)
	}
	present := make(map[uint32]bool, len(found))
	for _, uid := range found {
		present[uid] = true
	}

	result := &MoveResult{}
	toMove := new(imap.SeqSet)
	for _, uid := range uids {
		id := strconv.FormatUint(uint64(uid), 10)
		if present[uid] {
			toMove.AddNum(uid)
			result.Moved = append(result.Moved, MovedEmail{ID: id})
		} else {
			result.NotFound = append(result.NotFound, id)
		}
	}

	if len(result.Moved) > 0 {
		if err := c.UidMove(toMove, dest); err != nil {
			return nil, fmt.Errorf("failed to move messages to %s: %w", dest, err)
		}
	}
	return result, nil
}
