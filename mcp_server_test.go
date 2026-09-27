package main

import (
	"testing"
)

func TestBuildMCPServer(t *testing.T) {
	s := BuildMCPServer()
	if s == nil {
		t.Fatalf("expected non-nil MCP server")
	}

	tools := s.ListTools()
	readOnly := []string{"list_accounts", "get_unread_emails", "search_emails", "read_email", "list_folders"}
	for _, name := range readOnly {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		if h := tool.Tool.Annotations.ReadOnlyHint; h == nil || !*h {
			t.Errorf("expected %s to be annotated read-only", name)
		}
		if h := tool.Tool.Annotations.DestructiveHint; h == nil || *h {
			t.Errorf("expected %s to be annotated non-destructive", name)
		}
	}

	for _, name := range []string{"move_emails", "create_folder"} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		a := tool.Tool.Annotations
		if a.ReadOnlyHint == nil || *a.ReadOnlyHint {
			t.Errorf("expected %s not to be read-only", name)
		}
		if a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("expected %s to be annotated non-destructive (it is reversible)", name)
		}
	}

	send, ok := tools["send_email"]
	if !ok {
		t.Fatalf("tool send_email not registered")
	}
	if h := send.Tool.Annotations.DestructiveHint; h == nil || !*h {
		t.Errorf("expected send_email to be annotated destructive")
	}
	if h := send.Tool.Annotations.ReadOnlyHint; h == nil || *h {
		t.Errorf("expected send_email not to be read-only")
	}
	for _, param := range []string{"cc", "bcc", "in_reply_to"} {
		if _, ok := send.Tool.InputSchema.Properties[param]; !ok {
			t.Errorf("expected send_email to accept %s", param)
		}
	}
}
