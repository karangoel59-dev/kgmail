package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AccountConfig defines the settings for an individual email account.
type AccountConfig struct {
	Provider string `json:"provider"`           // gmail, zoho, outlook, imap
	Host     string `json:"host"`               // IMAP host
	Port     int    `json:"port,omitempty"`     // IMAP port (default: 993)
	SSL      *bool  `json:"ssl,omitempty"`      // Use TLS/SSL (default: true)
	StartTLS bool   `json:"starttls,omitempty"` // Use StartTLS (default: false)
	Username string `json:"username"`           // Username / email address
	Password string `json:"password"`           // Password or App Password
	Enabled  *bool  `json:"enabled,omitempty"`  // Enabled flag (default: true)

	// SMTP configuration (optional, for sending emails)
	SMTPHost string `json:"smtp_host,omitempty"` // SMTP host (e.g. smtp.gmail.com)
	SMTPPort int    `json:"smtp_port,omitempty"` // SMTP port (default: 587 or 465)
	SMTPUser string `json:"smtp_user,omitempty"` // SMTP username (defaults to Username)
	SMTPPass string `json:"smtp_pass,omitempty"` // SMTP password (defaults to Password)
}

// Config represents the top-level configuration file.
type Config struct {
	Accounts map[string]AccountConfig `json:"accounts"`
}

// DefaultConfigPath returns ~/.kgmail/config.json
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "kgmail.json"
	}
	return filepath.Join(home, ".kgmail", "config.json")
}

// LegacyConfigPath returns ~/.config/mail-mcp/accounts.json
func LegacyConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mail-mcp", "accounts.json")
}

// ResolveConfigPath checks environment variables, ~/.kgmail/config.json, and legacy ~/.config/mail-mcp/accounts.json
func ResolveConfigPath() string {
	if env := os.Getenv("KGMAIL_CONFIG"); env != "" {
		return env
	}
	if env := os.Getenv("EMAIL_MCP_CONFIG"); env != "" {
		return env
	}

	primary := DefaultConfigPath()
	if _, err := os.Stat(primary); err == nil {
		return primary
	}

	legacy := LegacyConfigPath()
	if legacy != "" {
		if _, err := os.Stat(legacy); err == nil {
			return legacy
		}
	}

	return primary
}

// LoadConfig loads and normalizes accounts from the active config file.
func LoadConfig() (*Config, string, error) {
	cfgPath := ResolveConfigPath()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Accounts: make(map[string]AccountConfig)}, cfgPath, nil
		}
		return nil, cfgPath, fmt.Errorf("failed to read config from %s: %w", cfgPath, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, cfgPath, fmt.Errorf("failed to parse config JSON: %w", err)
	}

	if cfg.Accounts == nil {
		cfg.Accounts = make(map[string]AccountConfig)
	}

	// Normalize account defaults
	for name, acc := range cfg.Accounts {
		acc = normalizeAccount(acc)
		cfg.Accounts[name] = acc
	}

	return &cfg, cfgPath, nil
}

// SaveConfig writes the configuration to disk.
func SaveConfig(cfg *Config, targetPath string) error {
	if targetPath == "" {
		targetPath = ResolveConfigPath()
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize config JSON: %w", err)
	}

	if err := os.WriteFile(targetPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config to %s: %w", targetPath, err)
	}

	return nil
}

func normalizeAccount(acc AccountConfig) AccountConfig {
	provider := strings.ToLower(strings.TrimSpace(acc.Provider))

	// Password cleanup: Gmail app passwords frequently contain spaces
	if provider == "gmail" && acc.Password != "" {
		acc.Password = strings.ReplaceAll(acc.Password, " ", "")
	} else if acc.Password != "" {
		acc.Password = strings.TrimSpace(acc.Password)
	}

	if acc.Port == 0 {
		acc.Port = 993
	}

	if acc.SSL == nil {
		ssl := true
		acc.SSL = &ssl
	}

	if acc.Enabled == nil {
		en := true
		acc.Enabled = &en
	}

	// Auto-fill provider presets if host is empty
	switch provider {
	case "gmail", "google":
		if acc.Host == "" {
			acc.Host = "imap.gmail.com"
		}
		if acc.SMTPHost == "" {
			acc.SMTPHost = "smtp.gmail.com"
		}
		if acc.SMTPPort == 0 {
			acc.SMTPPort = 587
		}
	case "zoho":
		if acc.Host == "" {
			acc.Host = "imap.zoho.in"
		}
		if acc.SMTPHost == "" {
			if strings.HasSuffix(acc.Host, ".in") {
				acc.SMTPHost = "smtp.zoho.in"
			} else {
				acc.SMTPHost = "smtp.zoho.com"
			}
		}
		if acc.SMTPPort == 0 {
			acc.SMTPPort = 587
		}
	case "outlook", "office365", "microsoft":
		if acc.Host == "" {
			acc.Host = "outlook.office365.com"
		}
		if acc.SMTPHost == "" {
			acc.SMTPHost = "smtp-mail.outlook.com"
		}
		if acc.SMTPPort == 0 {
			acc.SMTPPort = 587
		}
	}

	return acc
}
