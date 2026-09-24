package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

const version = "2.1.0"

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
  send                          Send an email via SMTP
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
		for name, acc := range cfg.Accounts {
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

	for name, acc := range cfg.Accounts {
		enabled := acc.Enabled == nil || *acc.Enabled
		if !enabled {
			fmt.Printf("%-15s %-10s %-30s %-25s %s\n", name, acc.Provider, acc.Username, fmt.Sprintf("%s:%d", acc.Host, acc.Port), "⏸️ Disabled")
			continue
		}

		testErr := TestConnection(acc)
		status := "✅ Connected"
		if testErr != nil {
			status = fmt.Sprintf("❌ Error: %v", testErr)
		}
		fmt.Printf("%-15s %-10s %-30s %-25s %s\n", name, acc.Provider, acc.Username, fmt.Sprintf("%s:%d", acc.Host, acc.Port), status)
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

	targets := make(map[string]AccountConfig)
	if strings.ToLower(accountName) == "all" {
		for name, acc := range cfg.Accounts {
			if acc.Enabled == nil || *acc.Enabled {
				targets[name] = acc
			}
		}
	} else {
		acc, ok := cfg.Accounts[accountName]
		if !ok {
			fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", accountName)
			os.Exit(1)
		}
		targets[accountName] = acc
	}

	total := 0
	for name, acc := range targets {
		fmt.Printf("📬 Checking unread for [%s] (%s)...\n", name, *folder)
		summaries, err := GetUnreadEmails(name, acc, *folder, *limit)
		if err != nil {
			fmt.Printf("  ❌ Error: %v\n\n", err)
			continue
		}

		if len(summaries) == 0 {
			fmt.Printf("  No unread emails in %s.\n\n", *folder)
			continue
		}

		total += len(summaries)
		for _, s := range summaries {
			fmt.Printf("  • [ID: %d] %s | %s\n", s.ID, s.Date.Format("02 Jan 15:04"), s.From)
			fmt.Printf("    Subject: %s\n", s.Subject)
			if s.Snippet != "" {
				fmt.Printf("    Snippet: %s\n", s.Snippet)
			}
			fmt.Println()
		}
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

	targets := make(map[string]AccountConfig)
	if strings.ToLower(*account) == "all" {
		for name, acc := range cfg.Accounts {
			if acc.Enabled == nil || *acc.Enabled {
				targets[name] = acc
			}
		}
	} else {
		acc, ok := cfg.Accounts[*account]
		if !ok {
			fmt.Fprintf(os.Stderr, "Account '%s' not found.\n", *account)
			os.Exit(1)
		}
		targets[*account] = acc
	}

	total := 0
	for name, acc := range targets {
		fmt.Printf("🔍 Searching [%s] for '%s'...\n", name, query)
		summaries, err := SearchEmails(name, acc, query, *folder, *limit)
		if err != nil {
			fmt.Printf("  ❌ Search error: %v\n\n", err)
			continue
		}

		if len(summaries) == 0 {
			fmt.Printf("  No matching emails in %s.\n\n", *folder)
			continue
		}

		total += len(summaries)
		for _, s := range summaries {
			fmt.Printf("  • [ID: %d] %s | %s\n", s.ID, s.Date.Format("02 Jan 15:04"), s.From)
			fmt.Printf("    Subject: %s\n", s.Subject)
			if s.Snippet != "" {
				fmt.Printf("    Snippet: %s\n", s.Snippet)
			}
			fmt.Println()
		}
	}

	if total == 0 {
		fmt.Println("No matches found.")
	}
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

	id, err := strconv.ParseUint(msgIDStr, 10, 32)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid message ID '%s': must be numeric\n", msgIDStr)
		os.Exit(1)
	}

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

	detail, err := ReadEmail(account, acc, uint32(id), *folder, *maxLen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read email: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("Account: %s | Message ID: %d\n", detail.Account, detail.ID)
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

func runSend() {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	account := fs.String("account", "", "Account name to send from")
	to := fs.String("to", "", "Recipient email address")
	subject := fs.String("subject", "", "Email subject")
	body := fs.String("body", "", "Email body text")
	isHTML := fs.Bool("html", false, "Body is HTML")
	fs.Parse(os.Args[2:])

	if *account == "" || *to == "" || *subject == "" || *body == "" {
		fmt.Println("Usage: kgmail send --account <acc> --to <recipient> --subject <subj> --body <body> [--html]")
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

	recipients := strings.Split(*to, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	if err := SendEmail(acc, recipients, *subject, *body, *isHTML); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to send email: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Email successfully sent to %s via %s.\n", *to, *account)
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
