package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

const version = "2.4.0"

func printUsage() {
	fmt.Printf(`kgmail %s - Multi-Account Email Manager & MCP Server

USAGE:
  kgmail <command> [arguments...]

COMMANDS:
  mcp                           Run the native MCP stdio server for AI agents
  list, accounts                List configured accounts and test connection status
  unread [account]              Fetch recent unread emails (safe read-only PEEK mode)
  search <query>                Search emails across accounts by keyword
  read <account> <id>           Read full email content and headers by ID
  folders <account>             List all mailboxes/folders for an account
  move <account> <ids> <folder> Move emails (comma-separated IDs) into a folder
  mkdir <account> <folder>      Create a mail folder ("Parent/Child" for nested)
  send                          Send an email (SMTP or Microsoft Graph)
  add-account <name>            Add or update an email account
  remove-account <name>         Remove an email account
  auth-microsoft <name>         Authorize Microsoft 365 account via browser (device-code)
  config                        Show resolved configuration file path and accounts
  version                       Show kgmail version

EXAMPLES:
  # Run as MCP Server for Claude Desktop, Gemini Antigravity, or Claude Code:
  kgmail mcp

  # Check all configured accounts:
  kgmail list

  # View recent unread emails:
  kgmail unread
  kgmail unread google --limit 5

  # Search emails:
  kgmail search "invoice" --account zoho
  kgmail search "security alert"

  # Read an email:
  kgmail read google 1234

  # Organize: file emails into a folder (create it if needed)
  kgmail move google 1234,1235 Receipts --create
  kgmail move google 88 INBOX --folder Receipts
  kgmail mkdir work "Projects/2026"

  # Send an email (optionally as a threaded reply):
  kgmail send --account google --to a@x.com --cc b@x.com --subject "Hi" --body "Hello"
  kgmail send --account google --to a@x.com --subject "Re: Hi" --body "Thanks" --in-reply-to "<id@x.com>"
  kgmail send --account google --to a@x.com --subject "Report" --body "Attached" --attach ~/Documents/report.pdf

  # Add an account with password / app password:
  kgmail add-account personal --provider gmail --user myemail@gmail.com --password <token>

  # Add a Microsoft 365 organization account with OAuth2:
  kgmail add-account work --provider office365 --user karan.goel@chat360.io --client-id <CLIENT_ID> --tenant-id <TENANT_ID>

  # Re-authorize Microsoft 365 account:
  kgmail auth-microsoft work
`, version)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	cmd := strings.ToLower(os.Args[1])

	switch cmd {
	case "mcp":
		s := BuildMCPServer()
		if err := server.ServeStdio(s); err != nil {
			fmt.Fprintf(os.Stderr, "MCP server exited with error: %v\n", err)
			os.Exit(1)
		}

	case "list", "accounts":
		runListAccounts()

	case "unread":
		runUnread()

	case "search":
		runSearch()

	case "read":
		runRead()

	case "folders":
		runFolders()

	case "move", "mv":
		runMove()

	case "mkdir", "create-folder":
		runMkdir()

	case "send":
		runSend()

	case "add-account", "add":
		runAddAccount()

	case "remove-account", "rm":
		runRemoveAccount()

	case "auth-microsoft", "auth-m365", "login-microsoft":
		runAuthMicrosoft()

	case "config":
		cfg, path, err := LoadConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Config File: %s\n", path)
		fmt.Printf("Configured Accounts (%d):\n", len(cfg.Accounts))
		for _, name := range SortedAccountNames(cfg) {
			acc := cfg.Accounts[name]
			en := "enabled"
			if acc.Enabled != nil && !*acc.Enabled {
				en = "disabled"
			}
			authMode := "password"
			if acc.IsOAuth2() {
				authMode = fmt.Sprintf("OAuth2 (tenant: %s, client_id: %s)", acc.TenantID, acc.ClientID)
			}
			fmt.Printf("  • %s (%s, %s, %s:%d, %s) [%s]\n", name, acc.Provider, acc.Username, acc.Host, acc.Port, authMode, en)
		}

	case "version", "-v", "--version":
		fmt.Printf("kgmail v%s\n", version)

	case "help", "-h", "--help":
		printUsage()

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func runListAccounts() {
	cfg, cfgPath, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if len(cfg.Accounts) == 0 {
		fmt.Printf("No accounts configured yet in %s.\n", cfgPath)
		fmt.Println("Run 'kgmail add-account <name> --provider <provider> --user <user> --password <token>' to add one.")
		return
	}

	fmt.Printf("Config file: %s\n\n", cfgPath)
	fmt.Printf("%-15s %-10s %-30s %-25s %s\n", "ACCOUNT", "PROVIDER", "USER", "IMAP HOST", "STATUS")
	fmt.Println(strings.Repeat("-", 95))

	results := forEachAccount(cfg, SortedAccountNames(cfg), func(name string, acc AccountConfig) (bool, error) {
		if acc.Enabled != nil && !*acc.Enabled {
			return false, nil
		}
		return true, TestConnection(acc)
	})

	for _, res := range results {
		acc := cfg.Accounts[res.Name]
		status := "✅ Connected"
		switch {
		case !res.Value:
			status = "⏸️ Disabled"
		case res.Err != nil:
			status = fmt.Sprintf("❌ Error: %v", res.Err)
		}
		fmt.Printf("%-15s %-10s %-30s %-25s %s\n", res.Name, acc.Provider, acc.Username, fmt.Sprintf("%s:%d", acc.Host, acc.Port), status)
	}
}

func runUnread() {
	fs := flag.NewFlagSet("unread", flag.ExitOnError)
	limit := fs.Int("limit", 10, "Max emails to fetch")
	folder := fs.String("folder", "INBOX", "Folder to fetch from")

	args := os.Args[2:]
	accountName := "all"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		accountName = args[0]
		args = args[1:]
	}
	fs.Parse(args)

	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	names, err := ResolveTargets(cfg, accountName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	results := forEachAccount(cfg, names, func(name string, acc AccountConfig) ([]EmailSummary, error) {
		return GetUnreadEmails(name, acc, *folder, *limit)
	})

	total := 0
	for _, res := range results {
		fmt.Printf("📬 Unread for [%s] (%s):\n", res.Name, *folder)
		if res.Err != nil {
			fmt.Printf("  ❌ Error: %v\n\n", res.Err)
			continue
		}
		if len(res.Value) == 0 {
			fmt.Printf("  No unread emails in %s.\n\n", *folder)
			continue
		}
		total += len(res.Value)
		printSummaries(res.Value)
	}

	if total == 0 {
		fmt.Println("No unread emails found.")
	}
}

func runSearch() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: kgmail search <query> [--account <name>] [--limit N] [--folder FOLDER]")
		os.Exit(1)
	}

	query := os.Args[2]
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	account := fs.String("account", "all", "Account to search")
	limit := fs.Int("limit", 10, "Max results")
	folder := fs.String("folder", "INBOX", "Folder to search")
	fs.Parse(os.Args[3:])

	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	names, err := ResolveTargets(cfg, *account)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	results := forEachAccount(cfg, names, func(name string, acc AccountConfig) ([]EmailSummary, error) {
		return SearchEmails(name, acc, query, *folder, *limit)
	})

	total := 0
	for _, res := range results {
		fmt.Printf("🔍 Results in [%s] for '%s':\n", res.Name, query)
		if res.Err != nil {
			fmt.Printf("  ❌ Search error: %v\n\n", res.Err)
			continue
		}
		if len(res.Value) == 0 {
			fmt.Printf("  No matching emails in %s.\n\n", *folder)
			continue
		}
		total += len(res.Value)
		printSummaries(res.Value)
	}

	if total == 0 {
		fmt.Println("No matches found.")
	}
}

func printSummaries(summaries []EmailSummary) {
	for _, s := range summaries {
		fmt.Printf("  • [ID: %s] %s | %s\n", s.ID, s.Date.Format("02 Jan 15:04"), s.From)
		fmt.Printf("    Subject: %s\n", s.Subject)
		if s.Snippet != "" {
			fmt.Printf("    Snippet: %s\n", s.Snippet)
		}
		fmt.Println()
	}
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ", ") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func splitAddressList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func runRead() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: kgmail read <account> <message_id> [--folder FOLDER] [--max-len N]")
		os.Exit(1)
	}

	account := os.Args[2]
	msgIDStr := os.Args[3]

	fs := flag.NewFlagSet("read", flag.ExitOnError)
	folder := fs.String("folder", "INBOX", "Folder to read from")
	maxLen := fs.Int("max-len", defaultMaxBodyLen, "Max body length")
	fs.Parse(os.Args[4:])

	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	acc, ok := cfg.Accounts[account]
	if !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", account)
		os.Exit(1)
	}

	detail, err := ReadEmail(account, acc, msgIDStr, *folder, *maxLen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read email: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("Account: %s | Message ID: %s\n", detail.Account, detail.ID)
	fmt.Printf("From:    %s\n", detail.From)
	fmt.Printf("To:      %s\n", strings.Join(detail.To, ", "))
	if len(detail.Cc) > 0 {
		fmt.Printf("Cc:      %s\n", strings.Join(detail.Cc, ", "))
	}
	fmt.Printf("Date:    %s\n", detail.Date.Format(time.RFC1123Z))
	fmt.Printf("Subject: %s\n", detail.Subject)
	if len(detail.Attachments) > 0 {
		fmt.Printf("Attachments: %s\n", strings.Join(detail.Attachments, ", "))
	}
	fmt.Println(strings.Repeat("-", 70))
	fmt.Println(detail.Body)
	fmt.Println(strings.Repeat("=", 70))
}

func runFolders() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: kgmail folders <account>")
		os.Exit(1)
	}

	account := os.Args[2]
	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	acc, ok := cfg.Accounts[account]
	if !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", account)
		os.Exit(1)
	}

	folders, err := ListFolders(acc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing folders for %s: %v\n", account, err)
		os.Exit(1)
	}

	fmt.Printf("Available folders for [%s] (%d):\n", account, len(folders))
	for _, f := range folders {
		fmt.Printf("  • %s\n", f)
	}
}

func runMove() {
	if len(os.Args) < 5 {
		fmt.Println("Usage: kgmail move <account> <id[,id...]> <destination> [--folder SOURCE] [--create]")
		os.Exit(1)
	}

	account := os.Args[2]
	ids := splitAddressList(os.Args[3])
	dest := os.Args[4]

	fs := flag.NewFlagSet("move", flag.ExitOnError)
	folder := fs.String("folder", "INBOX", "Folder the emails are currently in")
	create := fs.Bool("create", false, "Create the destination folder if it doesn't exist")
	fs.Parse(os.Args[5:])

	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	acc, ok := cfg.Accounts[account]
	if !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", account)
		os.Exit(1)
	}

	result, err := MoveEmails(acc, ids, *folder, dest, *create)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to move emails: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Moved %d email(s) to %s.\n", len(result.Moved), dest)
	for _, m := range result.Moved {
		if m.NewID != "" {
			fmt.Printf("  %s -> %s\n", m.ID, m.NewID)
		}
	}
	if len(result.NotFound) > 0 {
		fmt.Printf("⚠️ Not found: %s\n", strings.Join(result.NotFound, ", "))
	}
}

func runMkdir() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: kgmail mkdir <account> <folder>")
		os.Exit(1)
	}

	account, name := os.Args[2], os.Args[3]
	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	acc, ok := cfg.Accounts[account]
	if !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", account)
		os.Exit(1)
	}

	created, err := CreateFolder(acc, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create folder: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Printf("✅ Created folder %s in [%s].\n", name, account)
	} else {
		fmt.Printf("Folder %s already exists in [%s].\n", name, account)
	}
}

func runSend() {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	account := fs.String("account", "", "Account name to send from")
	to := fs.String("to", "", "Recipient email address(es), comma-separated")
	cc := fs.String("cc", "", "Cc recipient(s), comma-separated")
	bcc := fs.String("bcc", "", "Bcc recipient(s), comma-separated")
	subject := fs.String("subject", "", "Email subject")
	body := fs.String("body", "", "Email body text")
	isHTML := fs.Bool("html", false, "Body is HTML")
	inReplyTo := fs.String("in-reply-to", "", "Message-ID of the email being replied to (threads the reply)")
	var attach stringList
	fs.Var(&attach, "attach", "File to attach (repeat for several files)")
	fs.Parse(os.Args[2:])

	if *account == "" || *to == "" || *subject == "" || *body == "" {
		fmt.Println("Usage: kgmail send --account <acc> --to <recipient> --subject <subj> --body <body> [--cc <addrs>] [--bcc <addrs>] [--in-reply-to <message-id>] [--attach <file>]... [--html]")
		os.Exit(1)
	}

	cfg, _, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	acc, ok := cfg.Accounts[*account]
	if !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", *account)
		os.Exit(1)
	}

	// The CLI is driven by the user directly, so any readable file may be attached
	attachments, err := loadAttachments(attach, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to attach files: %v\n", err)
		os.Exit(1)
	}

	msg := OutgoingEmail{
		Attachments: attachments,
		To:          splitAddressList(*to),
		Cc:          splitAddressList(*cc),
		Bcc:         splitAddressList(*bcc),
		Subject:     *subject,
		Body:        *body,
		IsHTML:      *isHTML,
		InReplyTo:   *inReplyTo,
	}

	if err := SendEmail(acc, msg); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to send email: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Email successfully sent to %s via %s.\n", *to, *account)
	if len(attachments) > 0 {
		fmt.Printf("   Attached: %s\n", describeAttachments(attachments))
	}
}

func runAddAccount() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: kgmail add-account <name> --provider <gmail|zoho|office365|imap> --user <user> [--password <pass>] [--client-id <id>] [--tenant-id <id>] [--client-secret <sec>]")
		os.Exit(1)
	}

	name := os.Args[2]
	fs := flag.NewFlagSet("add-account", flag.ExitOnError)
	provider := fs.String("provider", "imap", "Provider (gmail, zoho, office365, outlook, imap)")
	user := fs.String("user", "", "Username / email address")
	pass := fs.String("password", "", "Password or App Password")
	host := fs.String("host", "", "IMAP host (optional if provider specified)")
	port := fs.Int("port", 993, "IMAP port")
	tenantID := fs.String("tenant-id", "", "Azure AD Tenant ID (or 'common'/'organizations')")
	clientID := fs.String("client-id", "", "Azure AD Application (client) ID")
	clientSecret := fs.String("client-secret", "", "Azure AD Client Secret (optional)")
	fs.Parse(os.Args[3:])

	if *user == "" {
		fmt.Fprintln(os.Stderr, "Error: --user is required.")
		os.Exit(1)
	}

	if *clientID == "" && *pass == "" {
		fmt.Fprintln(os.Stderr, "Error: either --password or --client-id is required.")
		os.Exit(1)
	}

	cfg, cfgPath, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	ssl := true
	enabled := true
	acc := AccountConfig{
		Provider:     *provider,
		Username:     *user,
		Password:     *pass,
		Host:         *host,
		Port:         *port,
		SSL:          &ssl,
		Enabled:      &enabled,
		TenantID:     *tenantID,
		ClientID:     *clientID,
		ClientSecret: *clientSecret,
	}

	acc = normalizeAccount(acc)
	acc.Name = name

	if acc.IsOAuth2() && acc.AccessToken == "" && acc.RefreshToken == "" && acc.ClientSecret == "" {
		// Save the account configuration skeleton first
		cfg.Accounts[name] = acc
		if err := SaveConfig(cfg, cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save initial config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Account '%s' saved. Starting Microsoft 365 OAuth 2.0 device code authorization...\n", name)
		if err := MicrosoftDeviceCodeFlow(name, &acc); err != nil {
			fmt.Fprintf(os.Stderr, "Authentication error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("Testing connection to %s for %s (%s)...\n", acc.Host, acc.Username, acc.Provider)
	if err := TestConnection(acc); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ Connection test failed: %v\nDo you still want to save? Run again with verified credentials if needed.\n", err)
	} else {
		fmt.Println("✅ Connection successful!")
	}

	cfg.Accounts[name] = acc
	if err := SaveConfig(cfg, cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Account '%s' saved to %s.\n", name, cfgPath)
}

func runAuthMicrosoft() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: kgmail auth-microsoft <account>")
		os.Exit(1)
	}

	name := os.Args[2]
	cfg, cfgPath, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	acc, ok := cfg.Accounts[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' not found in %s.\n", name, cfgPath)
		os.Exit(1)
	}

	if acc.ClientID == "" {
		fmt.Fprintf(os.Stderr, "Account '%s' does not have a client_id configured.\nUpdate your config with client_id and tenant_id first.\n", name)
		os.Exit(1)
	}

	if err := MicrosoftDeviceCodeFlow(name, &acc); err != nil {
		fmt.Fprintf(os.Stderr, "OAuth2 authorization failed: %v\n", err)
		os.Exit(1)
	}
}

func runRemoveAccount() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: kgmail remove-account <name>")
		os.Exit(1)
	}

	name := os.Args[2]
	cfg, cfgPath, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	if _, ok := cfg.Accounts[name]; !ok {
		fmt.Fprintf(os.Stderr, "Account '%s' does not exist in %s.\n", name, cfgPath)
		os.Exit(1)
	}

	delete(cfg.Accounts, name)
	if err := SaveConfig(cfg, cfgPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Account '%s' removed from %s.\n", name, cfgPath)
}
