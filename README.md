# kgmail 📬

A lightweight, high-performance Multi-Account Email Manager and native **Model Context Protocol (MCP)** server written in Go.

`kgmail` provides a unified command-line tool and an ultra-fast MCP stdio server for AI coding assistants (Claude Desktop, Google Antigravity / Gemini CLI, Claude Code, VS Code, Cursor).

---

## ✨ Features

- **⚡ Blazing Fast**: Single native Go binary, instant startup (<10ms), zero Python / runtime dependencies.
- **🤖 Native MCP Server**: Exposes 6 comprehensive tools over standard input/output (`stdio`) using the official Go MCP SDK (`github.com/mark3labs/mcp-go`).
- **🛡️ Safe Read-Only (PEEK)**: Always inspects emails using `BODY.PEEK` and opens mailboxes in `ReadOnly` mode. Messages are **never** accidentally marked as read.
- **🌐 Multi-Account Support**: Manage multiple accounts simultaneously across **Gmail**, **Zoho**, **Outlook / Office 365**, and custom IMAP/SMTP providers.
- **📄 Smart MIME & HTML Stripping**: Cleanly converts complex HTML emails and multipart attachments into readable plain text while filtering out MIME boundary noise and script tags.
- **📤 SMTP Email Dispatch**: Built-in support for sending emails via SMTP (supports STARTTLS on port 587 and SSL on port 465).
- **🔄 Auto-Compatibility**: Reads configuration from `~/.kgmail/config.json`, legacy `~/.config/mail-mcp/accounts.json`, or environment variables `KGMAIL_CONFIG` / `EMAIL_MCP_CONFIG`.

---

## 🛠️ MCP Tools Exposed

| Tool | Description | Parameters |
| :--- | :--- | :--- |
| `list_accounts` | Lists all configured accounts and tests IMAP connection health | None |
| `get_unread_emails` | Fetches recent unread emails across one or all accounts in safe PEEK mode | `account` (default: `"all"`), `limit` (default: 10), `folder` (default: `"INBOX"`) |
| `search_emails` | Searches emails by keyword, sender, or subject across accounts | `query` *(required)*, `account`, `limit`, `folder` |
| `read_email` | Retrieves full headers, recipients, and sanitized body for an email by ID | `account` *(required)*, `message_id` *(required)*, `folder`, `max_length` |
| `list_folders` | Lists all available mailboxes/folders for an account (e.g. INBOX, Sent, Archive, Spam) | `account` *(required)* |
| `send_email` | Transmits an email message via SMTP | `account` *(required)*, `to` *(required)*, `subject` *(required)*, `body` *(required)*, `is_html` |

---

## 🚀 Installation

### 1. Build and Install from Source

```bash
git clone https://github.com/karangoel59-dev/kgmail.git
cd kgmail
go build -o kgmail .
sudo mv kgmail /usr/local/bin/kgmail
```

Or install using Go:

```bash
go install github.com/karangoel59-dev/kgmail@latest
```

---

## ⚙️ Configuration

`kgmail` looks for configuration in the following order:
1. Environment variable `$KGMAIL_CONFIG`
2. Environment variable `$EMAIL_MCP_CONFIG`
3. Primary path: `~/.kgmail/config.json`
4. Fallback/Legacy path: `~/.config/mail-mcp/accounts.json`

### Example Configuration (`~/.kgmail/config.json`)

```json
{
  "accounts": {
    "google": {
      "provider": "gmail",
      "host": "imap.gmail.com",
      "port": 993,
      "ssl": true,
      "username": "myemail@gmail.com",
      "password": "your-16-char-app-password",
      "enabled": true
    },
    "zoho": {
      "provider": "zoho",
      "host": "imap.zoho.in",
      "port": 993,
      "ssl": true,
      "username": "user@zohomail.in",
      "password": "your-zoho-app-password",
      "enabled": true
    },
    "work": {
      "provider": "office365",
      "username": "user@company.com",
      "tenant_id": "your-azure-tenant-id-or-common",
      "client_id": "your-azure-app-client-id",
      "enabled": true
    }
  }
}
```

> **Note for Gmail Users**: Gmail requires an **App Password** (generated under Google Account > Security > 2-Step Verification > App Passwords). Spaces in 16-character app passwords are automatically stripped.

### 🏢 Microsoft 365 / Exchange Online OAuth 2.0 (Azure AD)

Since Microsoft disabled Basic Auth / App Passwords for Exchange Online, organizational Microsoft 365 accounts use **OAuth 2.0 (XOAUTH2)** via Azure AD:

1. **Register an Azure AD App** at [portal.azure.com](https://portal.azure.com) → **Microsoft Entra ID** → **App registrations** → **New registration**:
   - Supported account types: *Accounts in this organizational directory only* (single tenant) or *Any Azure AD directory* (multi-tenant).
   - Under **Authentication** → **Advanced settings** → **Allow public client flows**: Select **Yes**.
2. **Add Delegated API Permissions**:
   - Under **API permissions** → **Add a permission** → **APIs my organization uses** (search `Office 365 Exchange Online` or add via Microsoft Graph):
     - `IMAP.AccessAsUser.All` (Read/write access to mailboxes via IMAP)
     - `SMTP.Send` (Send mail on behalf of signed-in user)
     - `offline_access` (Maintain access to data you have given it access to / refresh tokens)
   - Grant admin consent if required by your tenant policy.
3. **Add Account to kgmail**:
   ```bash
   kgmail add-account work --provider office365 \
     --user karan.goel@chat360.io \
     --client-id <YOUR_CLIENT_ID> \
     --tenant-id <YOUR_TENANT_ID>
   ```
4. **Authenticate via Browser**:
   `kgmail` initiates the device-code flow:
   ```bash
   kgmail auth-microsoft work
   ```
   Open `https://microsoft.com/devicelogin`, enter the user code shown in terminal, and log in with your work email. `kgmail` automatically stores the access/refresh tokens and silently refreshes them before expiration.

---

## 💻 CLI Usage

```bash
# Check connectivity and list configured accounts
kgmail list

# Fetch recent unread emails across all accounts (or a specific account)
kgmail unread
kgmail unread google --limit 5

# Search emails by keyword
kgmail search "invoice"
kgmail search "verification code" --account google --limit 3

# Read an email by sequence ID
kgmail read google 197

# List folders / mailboxes
kgmail folders google

# Send an email via SMTP
kgmail send --account google --to colleague@example.com --subject "Meeting update" --body "See you at 3 PM."

# Add or update an account via CLI
kgmail add-account work --provider gmail --user user@company.com --password <token>

# Remove an account
kgmail remove-account work

# Run as native MCP Server
kgmail mcp
```

---

## 🔌 MCP Client Configuration

### Claude Desktop
Add to `~/Library/Application Support/Claude/claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "kgmail": {
      "command": "/usr/local/bin/kgmail",
      "args": ["mcp"]
    }
  }
}
```

### Google Antigravity / Gemini CLI
Add to `~/.gemini/config/mcp_config.json`:
```json
{
  "mcpServers": {
    "kgmail": {
      "command": "/usr/local/bin/kgmail",
      "args": ["mcp"]
    }
  }
}
```

### Claude Code
Add to `~/.claude.json`:
```json
{
  "mcpServers": {
    "kgmail": {
      "command": "/usr/local/bin/kgmail",
      "args": ["mcp"]
    }
  }
}
```

---

## 🧪 Testing

Run automated tests:
```bash
go test -v ./...
```

---

## 📄 License

MIT License. See [LICENSE](LICENSE) for details.
