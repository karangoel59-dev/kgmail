package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeAccount(t *testing.T) {
	acc := AccountConfig{
		Provider: "gmail",
		Username: "test@gmail.com",
		Password: "abcd efgh ijkl mnop",
	}

	norm := normalizeAccount(acc)

	if norm.Password != "abcdefghijklmnop" {
		t.Errorf("expected spaces removed from Gmail password, got: %s", norm.Password)
	}
	if norm.Host != "imap.gmail.com" {
		t.Errorf("expected host to be imap.gmail.com, got: %s", norm.Host)
	}
	if norm.Port != 993 {
		t.Errorf("expected port 993, got: %d", norm.Port)
	}
	if norm.SMTPHost != "smtp.gmail.com" {
		t.Errorf("expected SMTP host smtp.gmail.com, got: %s", norm.SMTPHost)
	}
	if norm.SMTPPort != 587 {
		t.Errorf("expected SMTP port 587, got: %d", norm.SMTPPort)
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kgmail-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpConfig := filepath.Join(tmpDir, "config.json")
	t.Setenv("KGMAIL_CONFIG", tmpConfig)

	cfg := &Config{
		Accounts: map[string]AccountConfig{
			"work": {
				Provider: "zoho",
				Host:     "imap.zoho.com",
				Username: "karan@work.com",
				Password: "secretpassword",
			},
		},
	}

	if err := SaveConfig(cfg, tmpConfig); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	loaded, path, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if path != tmpConfig {
		t.Errorf("expected path %s, got %s", tmpConfig, path)
	}

	workAcc, ok := loaded.Accounts["work"]
	if !ok {
		t.Fatalf("expected 'work' account to exist")
	}

	if workAcc.Username != "karan@work.com" {
		t.Errorf("expected username karan@work.com, got: %s", workAcc.Username)
	}
	if workAcc.Port != 993 {
		t.Errorf("expected default port 993, got: %d", workAcc.Port)
	}
}
