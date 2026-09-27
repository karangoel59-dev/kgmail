package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestOAuth2ConfigAndTokens(t *testing.T) {
	acc := AccountConfig{
		Provider: "office365",
		Username: "karan.goel@chat360.io",
		ClientID: "client-12345",
	}

	norm := normalizeAccount(acc)
	if !norm.IsOAuth2() {
		t.Errorf("expected IsOAuth2 to be true")
	}
	if norm.TenantID != "common" {
		t.Errorf("expected default TenantID to be 'common', got: %s", norm.TenantID)
	}
	if norm.Host != "outlook.office365.com" {
		t.Errorf("expected Host outlook.office365.com, got: %s", norm.Host)
	}
	if norm.SMTPHost != "smtp.office365.com" {
		t.Errorf("expected SMTPHost smtp.office365.com, got: %s", norm.SMTPHost)
	}

	tmpDir, err := os.MkdirTemp("", "kgmail-oauth-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpConfig := filepath.Join(tmpDir, "config.json")
	t.Setenv("KGMAIL_CONFIG", tmpConfig)

	cfg := &Config{
		Accounts: map[string]AccountConfig{
			"m365": norm,
		},
	}
	if err := SaveConfig(cfg, tmpConfig); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Update tokens
	if err := UpdateAccountTokens("m365", "access-token-abc", "refresh-token-xyz", 1899999999); err != nil {
		t.Fatalf("failed to update tokens: %v", err)
	}

	reloaded, _, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}

	m365Acc := reloaded.Accounts["m365"]
	if m365Acc.AccessToken != "access-token-abc" {
		t.Errorf("expected access token 'access-token-abc', got: %s", m365Acc.AccessToken)
	}
	if m365Acc.RefreshToken != "refresh-token-xyz" {
		t.Errorf("expected refresh token 'refresh-token-xyz', got: %s", m365Acc.RefreshToken)
	}
	if m365Acc.TokenExpiry != 1899999999 {
		t.Errorf("expected token expiry 1899999999, got: %d", m365Acc.TokenExpiry)
	}
}

func TestUpdateAccountTokens_SharedUsername(t *testing.T) {
	tmpConfig := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("KGMAIL_CONFIG", tmpConfig)

	// Two accounts for the same mailbox (e.g. IMAP OAuth and Graph)
	cfg := &Config{Accounts: map[string]AccountConfig{
		"imap":  {Provider: "office365", Username: "me@corp.com", ClientID: "c", TenantID: "t"},
		"graph": {Provider: "graph", Username: "me@corp.com", ClientID: "c", TenantID: "t"},
	}}
	if err := SaveConfig(cfg, tmpConfig); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	for i := 0; i < 20; i++ {
		if err := UpdateAccountTokens("graph", "graph-token", "", 1899999999); err != nil {
			t.Fatalf("UpdateAccountTokens: %v", err)
		}
	}

	loaded, _, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := loaded.Accounts["imap"].AccessToken; got != "" {
		t.Errorf("imap account received another account's token: %q", got)
	}
	if got := loaded.Accounts["graph"].AccessToken; got != "graph-token" {
		t.Errorf("graph account token = %q", got)
	}
	if loaded.Accounts["graph"].Name != "graph" {
		t.Errorf("expected LoadConfig to populate Name")
	}

	info, err := os.Stat(tmpConfig)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("config permissions = %o, want 600", perm)
	}
}

func TestResolveTargets(t *testing.T) {
	disabled := false
	cfg := &Config{Accounts: map[string]AccountConfig{
		"b":   {},
		"a":   {},
		"off": {Enabled: &disabled},
	}}
	names, err := ResolveTargets(cfg, "all")
	if err != nil || strings.Join(names, ",") != "a,b" {
		t.Errorf("ResolveTargets(all) = %v, %v", names, err)
	}
	if _, err := ResolveTargets(cfg, "missing"); err == nil {
		t.Errorf("expected error for unknown account")
	}
}
