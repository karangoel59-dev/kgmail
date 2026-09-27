package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
)

const (
	defaultMicrosoftScope = "https://outlook.office365.com/IMAP.AccessAsUser.All https://outlook.office365.com/SMTP.Send offline_access"
	graphDelegatedScope   = "https://graph.microsoft.com/.default offline_access"
)

// xoauth2Client implements sasl.Client for IMAP XOAUTH2 authentication.
type xoauth2Client struct {
	username string
	token    string
}

func newXOAuth2Client(username, token string) sasl.Client {
	return &xoauth2Client{username: username, token: token}
}

func (c *xoauth2Client) Start() (string, []byte, error) {
	ir := []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.username, c.token))
	return "XOAUTH2", ir, nil
}

func (c *xoauth2Client) Next(challenge []byte) ([]byte, error) {
	// If the server sends an error challenge (base64 JSON error payload),
	// an empty response prompts the server to return the final error.
	return []byte(""), nil
}

// smtpXOAuth2Auth implements net/smtp.Auth for SMTP XOAUTH2 authentication.
type smtpXOAuth2Auth struct {
	username string
	token    string
}

func newSMTPXOAuth2Auth(username, token string) smtp.Auth {
	return &smtpXOAuth2Auth{username: username, token: token}
}

func (a *smtpXOAuth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	resp := []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.token))
	return "XOAUTH2", resp, nil
}

func (a *smtpXOAuth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return []byte(""), nil
	}
	return nil, nil
}

type microsoftTokenResponse struct {
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

type microsoftDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
	Error           string `json:"error"`
	ErrorDesc       string `json:"error_description"`
}

// microsoftScopes returns the delegated (refresh/device-code) and app-only
// (client-credentials) scopes for the API the account talks to. IMAP/SMTP and
// Microsoft Graph tokens have different audiences and are not interchangeable.
func microsoftScopes(cfg *AccountConfig) (delegated, appOnly string) {
	if cfg.IsGraph() {
		return graphDelegatedScope, "https://graph.microsoft.com/.default"
	}
	return defaultMicrosoftScope, "https://outlook.office365.com/.default"
}

// GetOrRefreshMicrosoftToken returns a valid access token for the given account,
// refreshing it via Microsoft Identity Platform if it is expired or expiring soon.
// Refreshed tokens are persisted to the config entry named by cfg.Name.
func GetOrRefreshMicrosoftToken(cfg *AccountConfig) (string, error) {
	// If cached token is still valid (with 2 minute safety margin), use it
	if cfg.AccessToken != "" && cfg.TokenExpiry > time.Now().Unix()+120 {
		return cfg.AccessToken, nil
	}

	delegatedScope, appOnlyScope := microsoftScopes(cfg)

	// Try refresh token if available
	if cfg.RefreshToken != "" {
		form := url.Values{}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", cfg.RefreshToken)
		form.Set("scope", delegatedScope)
		tr, err := requestMicrosoftToken(cfg, tenantOrDefault(cfg.TenantID, "common"), form)
		if err == nil {
			storeMicrosoftToken(cfg, tr)
			return cfg.AccessToken, nil
		}
		// If refresh failed and we have no secret, report the error
		if cfg.ClientSecret == "" {
			return "", fmt.Errorf("failed to refresh token (%v); please re-authenticate with 'kgmail auth-microsoft %s'", err, cfg.Name)
		}
	}

	// Try client credentials if client_secret is present
	if cfg.ClientSecret != "" {
		form := url.Values{}
		form.Set("grant_type", "client_credentials")
		form.Set("scope", appOnlyScope)
		tr, err := requestMicrosoftToken(cfg, tenantOrDefault(cfg.TenantID, "organizations"), form)
		if err != nil {
			return "", fmt.Errorf("client credentials: %w", err)
		}
		// Client-credential responses carry no refresh token
		tr.RefreshToken = ""
		storeMicrosoftToken(cfg, tr)
		return cfg.AccessToken, nil
	}

	return "", fmt.Errorf("account %s requires Microsoft OAuth2 authentication. Run 'kgmail auth-microsoft %s' to sign in", cfg.Username, cfg.Name)
}

func tenantOrDefault(tenant, def string) string {
	if tenant == "" {
		return def
	}
	return tenant
}

// requestMicrosoftToken posts form (plus client_id/client_secret) to the tenant's token endpoint.
func requestMicrosoftToken(cfg *AccountConfig, tenant string, form url.Values) (*microsoftTokenResponse, error) {
	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", url.PathEscape(tenant))

	form.Set("client_id", cfg.ClientID)
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to Microsoft token endpoint failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var tr microsoftTokenResponse
	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Microsoft token error [%s]: %s", tr.Error, tr.ErrorDesc)
	}

	if tr.AccessToken == "" {
		return nil, fmt.Errorf("received empty access token from Microsoft")
	}

	return &tr, nil
}

// storeMicrosoftToken updates cfg with tr and persists it to the config file.
func storeMicrosoftToken(cfg *AccountConfig, tr *microsoftTokenResponse) {
	cfg.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		cfg.RefreshToken = tr.RefreshToken
	}
	cfg.TokenExpiry = time.Now().Unix() + tr.ExpiresIn

	// Microsoft rotates refresh tokens, so a failed save is worth surfacing.
	// Stderr is safe here: stdout carries the MCP protocol.
	if err := UpdateAccountTokens(cfg.Name, cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry); err != nil {
		fmt.Fprintf(os.Stderr, "kgmail: warning: failed to persist refreshed tokens for %s: %v\n", cfg.Name, err)
	}
}

// MicrosoftDeviceCodeFlow guides the user through device code authorization in their browser.
func MicrosoftDeviceCodeFlow(accountName string, cfg *AccountConfig) error {
	if cfg.ClientID == "" {
		return fmt.Errorf("client_id is required for Microsoft OAuth2. Set client_id in configuration")
	}
	cfg.Name = accountName

	tenant := cfg.TenantID
	if tenant == "" {
		tenant = "common"
	}

	deviceEndpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/devicecode", url.PathEscape(tenant))
	tokenEndpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", url.PathEscape(tenant))

	scope, _ := microsoftScopes(cfg)
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("scope", scope)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm(deviceEndpoint, form)
	if err != nil {
		return fmt.Errorf("failed to request device code from Microsoft: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var dc microsoftDeviceCodeResponse
	if err := json.Unmarshal(bodyBytes, &dc); err != nil {
		return fmt.Errorf("failed to parse device code response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Microsoft device code error [%s]: %s", dc.Error, dc.ErrorDesc)
	}

	uri := dc.VerificationURI
	if uri == "" {
		uri = "https://microsoft.com/devicelogin"
	}

	fmt.Println()
	fmt.Println(strings.Repeat("=", 78))
	fmt.Println("  Microsoft 365 OAuth 2.0 Device Authorization")
	fmt.Println(strings.Repeat("=", 78))
	fmt.Printf("  Account:     %s (%s)\n", accountName, cfg.Username)
	fmt.Printf("  Tenant ID:   %s\n", tenant)
	fmt.Printf("  Client ID:   %s\n\n", cfg.ClientID)
	fmt.Println("  Follow these steps to authorize kgmail:")
	fmt.Printf("    1. Open your browser:  \033[1;34m%s\033[0m\n", uri)
	fmt.Printf("    2. Enter the code:     \033[1;32m%s\033[0m\n", dc.UserCode)
	if cfg.Username != "" {
		fmt.Printf("    3. Sign in as:         %s\n", cfg.Username)
	} else {
		fmt.Println("    3. Sign in with your work/school Microsoft account")
	}
	fmt.Println("    4. Consent to the requested email permissions")
	fmt.Println(strings.Repeat("=", 78))
	fmt.Print("Waiting for authorization")

	interval := dc.Interval
	if interval <= 0 {
		interval = 5
	}

	expiresAt := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)

	for {
		time.Sleep(time.Duration(interval) * time.Second)

		if time.Now().After(expiresAt) {
			fmt.Println()
			return fmt.Errorf("device authorization code has expired. Please run 'kgmail auth-microsoft %s' again", accountName)
		}

		pollForm := url.Values{}
		pollForm.Set("client_id", cfg.ClientID)
		pollForm.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		pollForm.Set("device_code", dc.DeviceCode)
		if cfg.ClientSecret != "" {
			pollForm.Set("client_secret", cfg.ClientSecret)
		}

		tResp, tErr := client.PostForm(tokenEndpoint, pollForm)
		if tErr != nil {
			fmt.Print(".")
			continue
		}

		tBody, tErr := io.ReadAll(tResp.Body)
		tResp.Body.Close()
		if tErr != nil {
			fmt.Print(".")
			continue
		}

		var tr microsoftTokenResponse
		if jsonErr := json.Unmarshal(tBody, &tr); jsonErr != nil {
			fmt.Print(".")
			continue
		}

		if tResp.StatusCode == http.StatusOK {
			fmt.Println()
			fmt.Println("✅ Authorization received from Microsoft Identity Platform!")

			cfg.AccessToken = tr.AccessToken
			cfg.RefreshToken = tr.RefreshToken
			cfg.TokenExpiry = time.Now().Unix() + tr.ExpiresIn

			if err := UpdateAccountTokens(accountName, cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️ Warning: failed to save tokens to config: %v\n", err)
			} else {
				fmt.Println("💾 Tokens successfully persisted to config.")
			}

			fmt.Println("Testing connection...")
			if testErr := TestConnection(*cfg); testErr != nil {
				fmt.Fprintf(os.Stderr, "⚠️ Note: Connection test returned: %v\n", testErr)
			} else {
				fmt.Println("✅ Connected and authenticated successfully!")
			}

			return nil
		}

		// Handle error cases
		switch tr.Error {
		case "authorization_pending":
			fmt.Print(".")
			continue
		case "slow_down":
			interval += 5
			fmt.Print("s")
			continue
		case "code_expired":
			fmt.Println()
			return fmt.Errorf("device code expired. Please run 'kgmail auth-microsoft %s' again", accountName)
		case "access_denied":
			fmt.Println()
			return fmt.Errorf("authorization was declined by the user or organizational policy")
		default:
			fmt.Println()
			return fmt.Errorf("OAuth2 error [%s]: %s", tr.Error, tr.ErrorDesc)
		}
	}
}
