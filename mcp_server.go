package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func argString(r mcp.CallToolRequest, key, def string) string {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return strings.TrimSpace(fmt.Sprintf("%v", v))
	}
	return def
}

func argInt(r mcp.CallToolRequest, key string, def int) int {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case int64:
			return int(n)
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return i
			}
		}
	}
	return def
}

func argBool(r mcp.CallToolRequest, key string, def bool) bool {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		if b, ok := v.(bool); ok {
			return b
		}
		if s, ok := v.(string); ok {
			return strings.ToLower(strings.TrimSpace(s)) == "true"
		}
	}
	return def
}

func argStringSlice(r mcp.CallToolRequest, key string) []string {
	args := r.GetArguments()
	if v, ok := args[key]; ok && v != nil {
		if sl, ok := v.([]any); ok {
			var res []string
			for _, item := range sl {
				if item != nil {
					res = append(res, fmt.Sprintf("%v", item))
				}
			}
			return res
		}
		if sl, ok := v.([]string); ok {
			return sl
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			parts := strings.Split(s, ",")
			var res []string
			for _, p := range parts {
				if trimmed := strings.TrimSpace(p); trimmed != "" {
					res = append(res, trimmed)
				}
			}
			return res
		}
	}
	return nil
}

// BuildMCPServer creates and configures the kgmail MCP server.
func BuildMCPServer() *server.MCPServer {
	s := server.NewMCPServer(
		"kgmail",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// 1. list_accounts
	s.AddTool(mcp.NewTool("list_accounts",
		mcp.WithDescription("Lists all configured email accounts and tests their IMAP connection status."),
	), handleListAccounts)

	// 2. get_unread_emails
	s.AddTool(mcp.NewTool("get_unread_emails",
		mcp.WithDescription("Fetches recent unread emails across one or all configured accounts using safe read-only mode (PEEK) so emails remain marked as unread."),
		mcp.WithString("account", mcp.Description("Account name or 'all' to check all accounts (default: 'all')")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of emails to retrieve per account (default: 10)")),
		mcp.WithString("folder", mcp.Description("Folder to check (default: 'INBOX')")),
	), handleGetUnreadEmails)

	// 3. search_emails
	s.AddTool(mcp.NewTool("search_emails",
		mcp.WithDescription("Searches emails by keyword, sender, or subject across one or all accounts in safe read-only mode."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Search keyword, sender email, or subject text")),
		mcp.WithString("account", mcp.Description("Account name or 'all' to search across all accounts (default: 'all')")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of emails to retrieve (default: 10)")),
		mcp.WithString("folder", mcp.Description("Folder to search (default: 'INBOX')")),
	), handleSearchEmails)

	// 4. read_email
	s.AddTool(mcp.NewTool("read_email",
		mcp.WithDescription("Retrieves full content, headers, and body of a specific email by its sequence ID in safe read-only mode (PEEK)."),
		mcp.WithString("account", mcp.Required(), mcp.Description("The account name containing the email")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("The numeric sequence ID of the email")),
		mcp.WithString("folder", mcp.Description("Folder containing the email (default: 'INBOX')")),
		mcp.WithNumber("max_length", mcp.Description("Maximum body character length (default: 10000)")),
	), handleReadEmail)

	// 5. list_folders
	s.AddTool(mcp.NewTool("list_folders",
		mcp.WithDescription("Lists all available mailboxes and folders for an email account (e.g. INBOX, Sent, Archive, Spam)."),
		mcp.WithString("account", mcp.Required(), mcp.Description("The account name")),
	), handleListFolders)

	// 6. send_email
	s.AddTool(mcp.NewTool("send_email",
		mcp.WithDescription("Sends an email from a configured account via SMTP."),
		mcp.WithString("account", mcp.Required(), mcp.Description("The account name to send from")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Recipient email address or comma-separated list")),
		mcp.WithString("subject", mcp.Required(), mcp.Description("Email subject line")),
		mcp.WithString("body", mcp.Required(), mcp.Description("Email body content")),
		mcp.WithBoolean("is_html", mcp.Description("Set to true if body contains HTML (default: false)")),
	), handleSendEmail)

	return s
}

func handleListAccounts(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, cfgPath, err := LoadConfig()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	if len(cfg.Accounts) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No accounts configured yet.\nConfig file: %s\n\nAdd accounts with:\n  kgmail add-account <name> --provider gmail --user user@gmail.com --password <token>", cfgPath)), nil
	}

	var names []string
	for name := range cfg.Accounts {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Configured Email Accounts (%d)\nConfig: `%s`\n\n", len(names), cfgPath))
	sb.WriteString("| Account | Provider | User | IMAP Host | Status |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")

	for _, name := range names {
		acc := cfg.Accounts[name]
		enabled := acc.Enabled == nil || *acc.Enabled
		if !enabled {
			sb.WriteString(fmt.Sprintf("| **%s** | %s | %s | %s:%d | ⏸️ Disabled |\n", name, acc.Provider, acc.Username, acc.Host, acc.Port))
			continue
		}

		testErr := TestConnection(acc)
		status := "✅ Connected"
		if testErr != nil {
			status = fmt.Sprintf("❌ Error (%v)", testErr)
		}
		sb.WriteString(fmt.Sprintf("| **%s** | %s | %s | %s:%d | %s |\n", name, acc.Provider, acc.Username, acc.Host, acc.Port, status))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleGetUnreadEmails(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, _, err := LoadConfig()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	account := argString(r, "account", "all")
	limit := argInt(r, "limit", 10)
	folder := argString(r, "folder", "INBOX")

	targets := make(map[string]AccountConfig)
	if strings.ToLower(account) == "all" {
		for name, acc := range cfg.Accounts {
			if acc.Enabled == nil || *acc.Enabled {
				targets[name] = acc
			}
		}
	} else {
		acc, ok := cfg.Accounts[account]
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("Account '%s' not found", account)), nil
		}
		targets[account] = acc
	}

	if len(targets) == 0 {
		return mcp.NewToolResultText("No enabled accounts configured."), nil
	}

	var sb strings.Builder
	totalFound := 0

	for name, acc := range targets {
		summaries, err := GetUnreadEmails(name, acc, folder, limit)
		if err != nil {
			sb.WriteString(fmt.Sprintf("### [%s] Error reading %s: %v\n\n", name, folder, err))
			continue
		}

		if len(summaries) == 0 {
			sb.WriteString(fmt.Sprintf("### [%s] No unread emails in %s\n\n", name, folder))
			continue
		}

		totalFound += len(summaries)
		sb.WriteString(fmt.Sprintf("### [%s] %d Unread Email(s) in %s:\n\n", name, len(summaries), folder))

		for _, s := range summaries {
			dateStr := s.Date.Format("02 Jan 15:04")
			sb.WriteString(fmt.Sprintf("- **ID: `%s`** | **From:** `%s` | **Date:** %s\n", s.ID, s.From, dateStr))
			sb.WriteString(fmt.Sprintf("  **Subject:** %s\n", s.Subject))
			if s.Snippet != "" {
				sb.WriteString(fmt.Sprintf("  *Preview:* %s\n", s.Snippet))
			}
			sb.WriteString("\n")
		}
	}

	if totalFound == 0 && sb.Len() == 0 {
		return mcp.NewToolResultText("No unread emails found across configured accounts."), nil
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleSearchEmails(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, _, err := LoadConfig()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	query := argString(r, "query", "")
	if query == "" {
		return mcp.NewToolResultError("Query string is required"), nil
	}

	account := argString(r, "account", "all")
	limit := argInt(r, "limit", 10)
	folder := argString(r, "folder", "INBOX")

	targets := make(map[string]AccountConfig)
	if strings.ToLower(account) == "all" {
		for name, acc := range cfg.Accounts {
			if acc.Enabled == nil || *acc.Enabled {
				targets[name] = acc
			}
		}
	} else {
		acc, ok := cfg.Accounts[account]
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("Account '%s' not found", account)), nil
		}
		targets[account] = acc
	}

	var sb strings.Builder
	totalFound := 0

	for name, acc := range targets {
		summaries, err := SearchEmails(name, acc, query, folder, limit)
		if err != nil {
			sb.WriteString(fmt.Sprintf("### [%s] Search error: %v\n\n", name, err))
			continue
		}

		if len(summaries) == 0 {
			sb.WriteString(fmt.Sprintf("### [%s] No emails matched '%s' in %s\n\n", name, query, folder))
			continue
		}

		totalFound += len(summaries)
		sb.WriteString(fmt.Sprintf("### [%s] Found %d match(es) for '%s':\n\n", name, len(summaries), query))

		for _, s := range summaries {
			dateStr := s.Date.Format("02 Jan 15:04")
			sb.WriteString(fmt.Sprintf("- **ID: `%s`** | **From:** `%s` | **Date:** %s\n", s.ID, s.From, dateStr))
			sb.WriteString(fmt.Sprintf("  **Subject:** %s\n", s.Subject))
			if s.Snippet != "" {
				sb.WriteString(fmt.Sprintf("  *Preview:* %s\n", s.Snippet))
			}
			sb.WriteString("\n")
		}
	}

	if totalFound == 0 && sb.Len() == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No emails matching '%s' found.", query)), nil
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleReadEmail(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, _, err := LoadConfig()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	account := argString(r, "account", "")
	msgIDStr := argString(r, "message_id", "")
	folder := argString(r, "folder", "INBOX")
	maxLen := argInt(r, "max_length", defaultMaxBodyLen)

	if account == "" || msgIDStr == "" {
		return mcp.NewToolResultError("Both 'account' and 'message_id' are required"), nil
	}

	acc, ok := cfg.Accounts[account]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Account '%s' not found", account)), nil
	}

	detail, err := ReadEmail(account, acc, msgIDStr, folder, maxLen)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Error reading email: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Email Details [%s #%s]\n\n", detail.Account, detail.ID))
	sb.WriteString(fmt.Sprintf("- **Subject:** %s\n", detail.Subject))
	sb.WriteString(fmt.Sprintf("- **From:** %s\n", detail.From))
	sb.WriteString(fmt.Sprintf("- **To:** %s\n", strings.Join(detail.To, ", ")))
	if len(detail.Cc) > 0 {
		sb.WriteString(fmt.Sprintf("- **Cc:** %s\n", strings.Join(detail.Cc, ", ")))
	}
	sb.WriteString(fmt.Sprintf("- **Date:** %s\n", detail.Date.Format(time.RFC1123Z)))
	if detail.MessageID != "" {
		sb.WriteString(fmt.Sprintf("- **Message-ID:** `%s`\n", detail.MessageID))
	}
	if len(detail.Attachments) > 0 {
		sb.WriteString(fmt.Sprintf("- **Attachments:** %s\n", strings.Join(detail.Attachments, ", ")))
	}
	sb.WriteString("\n---\n### Body:\n\n")
	sb.WriteString(detail.Body)
	sb.WriteString("\n")

	return mcp.NewToolResultText(sb.String()), nil
}

func handleListFolders(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, _, err := LoadConfig()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	account := argString(r, "account", "")
	if account == "" {
		return mcp.NewToolResultError("Account name is required"), nil
	}

	acc, ok := cfg.Accounts[account]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Account '%s' not found", account)), nil
	}

	folders, err := ListFolders(acc)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list folders for %s: %v", account, err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Available Folders for [%s] (%d):\n\n", account, len(folders)))
	for _, f := range folders {
		sb.WriteString(fmt.Sprintf("- `%s`\n", f))
	}

	return mcp.NewToolResultText(sb.String()), nil
}

func handleSendEmail(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	cfg, _, err := LoadConfig()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to load config: %v", err)), nil
	}

	account := argString(r, "account", "")
	toAddrs := argStringSlice(r, "to")
	subject := argString(r, "subject", "")
	body := argString(r, "body", "")
	isHTML := argBool(r, "is_html", false)

	if account == "" || len(toAddrs) == 0 || subject == "" || body == "" {
		return mcp.NewToolResultError("Fields 'account', 'to', 'subject', and 'body' are all required"), nil
	}

	acc, ok := cfg.Accounts[account]
	if !ok {
		return mcp.NewToolResultError(fmt.Sprintf("Account '%s' not found", account)), nil
	}

	if err := SendEmail(acc, toAddrs, subject, body, isHTML); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to send email: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("✅ Email successfully sent to %s via %s.", strings.Join(toAddrs, ", "), account)), nil
}
