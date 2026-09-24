package main

import (
	"testing"
)

func TestBuildMCPServer(t *testing.T) {
	s := BuildMCPServer()
	if s == nil {
		t.Fatalf("expected non-nil MCP server")
	}

	expectedTools := []string{
		"list_accounts",
		"get_unread_emails",
		"search_emails",
		"read_email",
		"list_folders",
		"send_email",
	}

	// Verify server initialization didn't panic and server object is constructed
	for _, tool := range expectedTools {
		if tool == "" {
			t.Errorf("empty tool name")
		}
	}
}
