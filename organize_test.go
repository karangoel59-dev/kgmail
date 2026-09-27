package main

import (
	"strings"
	"testing"
)

func TestParseUIDs(t *testing.T) {
	uids, err := parseUIDs([]string{" 5", "9", "5", ""})
	if err != nil {
		t.Fatalf("parseUIDs: %v", err)
	}
	if len(uids) != 2 || uids[0] != 5 || uids[1] != 9 {
		t.Errorf("parseUIDs = %v, want [5 9]", uids)
	}
	for _, bad := range []string{"0", "abc", "-3", "99999999999"} {
		if _, err := parseUIDs([]string{bad}); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestCleanIDs(t *testing.T) {
	ids, err := cleanIDs([]string{"a", " a ", "b", ""})
	if err != nil || strings.Join(ids, ",") != "a,b" {
		t.Errorf("cleanIDs = %v, %v", ids, err)
	}
	if _, err := cleanIDs([]string{" ", ""}); err == nil {
		t.Errorf("expected error for no IDs")
	}
	many := make([]string, maxMoveIDs+1)
	for i := range many {
		many[i] = strings.Repeat("x", i+1)
	}
	if _, err := cleanIDs(many); err == nil {
		t.Errorf("expected error above maxMoveIDs")
	}
}

func TestSplitFolderPath(t *testing.T) {
	got := splitFolderPath(" /Receipts// 2026 /")
	if strings.Join(got, "|") != "Receipts|2026" {
		t.Errorf("splitFolderPath = %q", got)
	}
	if len(splitFolderPath("  ")) != 0 {
		t.Errorf("expected no segments for blank path")
	}
}

func TestMoveEmails_Validation(t *testing.T) {
	acc := AccountConfig{Host: "imap.example.com", Username: "u", Password: "p"}
	if _, err := MoveEmails(acc, nil, "INBOX", "Archive", false); err == nil {
		t.Errorf("expected error for no IDs")
	}
	if _, err := MoveEmails(acc, []string{"1"}, "INBOX", " ", false); err == nil {
		t.Errorf("expected error for blank destination")
	}
	if _, err := MoveEmails(acc, []string{"1"}, "Archive", "Archive", false); err == nil {
		t.Errorf("expected error when source equals destination")
	}
	if _, err := MoveEmails(acc, []string{"abc"}, "INBOX", "Archive", false); err == nil {
		t.Errorf("expected error for non-numeric IMAP ID")
	}
}

func TestFormatMoveResult(t *testing.T) {
	out := formatMoveResult("work", "INBOX", "Receipts", true, &MoveResult{
		Moved:    []MovedEmail{{ID: "old1", NewID: "new1"}},
		NotFound: []string{"gone"},
	})
	for _, want := range []string{"Moved 1 email(s)", "`old1` → `new1`", "Not found", "gone"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out = formatMoveResult("google", "INBOX", "Receipts", false, &MoveResult{Moved: []MovedEmail{{ID: "5"}}})
	if !strings.Contains(out, "new IDs in `Receipts`") {
		t.Errorf("expected hint about new IDs for IMAP:\n%s", out)
	}
}
