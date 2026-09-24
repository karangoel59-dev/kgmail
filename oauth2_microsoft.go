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

// GetOrRefreshMicrosoftToken returns a valid access token for the given account,
// refreshing it via Microsoft Identity Platform if it is expired or expiring soon.
func GetOrRefreshMicrosoftToken(cfg *AccountConfig) (string, error) {
	// If cached token is still valid (with 2 minute safety margin), use it
	if cfg.AccessToken != "" && cfg.TokenExpiry > time.Now().Unix()+120 {
		return cfg.AccessToken, nil
	}

	// Try refresh token if available
	if cfg.RefreshToken != "" {
		token, err := refreshMicrosoftToken(cfg)
		if err == nil {
			return token, nil
		}
		// If refresh failed and we have no secret, report the error
		if cfg.ClientSecret == "" {
			return "", fmt.Errorf("failed to refresh token (%v); please re-authenticate with 'kgmail auth-microsoft'", err)
		}
	}

	// Try client credentials if client_secret is present
	if cfg.ClientSecret != "" {
		return clientCredentialsMicrosoftToken(cfg)
	}

	return "", fmt.Errorf("account %s requires Microsoft OAuth2 authentication. Run 'kgmail auth-microsoft <account>' to sign in", cfg.Username)
}

func refreshMicrosoftToken(cfg *AccountConfig) (string, error) {
	tenant := cfg.TenantID
	if tenant == "" {
		tenant = "common"
	}

	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", cfg.RefreshToken)
	form.Set("scope", defaultMicrosoftScope)
	if cfg.ClientSecret != "" {
		form.Set("client_secret", cfg.ClientSecret)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return "", fmt.Errorf("HTTP request to Microsoft token endpoint failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var tr microsoftTokenResponse
	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Microsoft token error [%s]: %s", tr.Error, tr.ErrorDesc)
	}

	if tr.AccessToken == "" {
		return "", fmt.Errorf("received empty access token from Microsoft")
	}

	cfg.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		cfg.RefreshToken = tr.RefreshToken
	}
	cfg.TokenExpiry = time.Now().Unix() + tr.ExpiresIn

	// Persist refreshed tokens to configuration file
	_ = UpdateAccountTokens(cfg.Username, cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry)

	return cfg.AccessToken, nil
}

func clientCredentialsMicrosoftToken(cfg *AccountConfig) (string, error) {
	tenant := cfg.TenantID
	if tenant == "" {
		tenant = "organizations"
	}

	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("grant_type", "client_credentials")
	form.Set("scope", "https://outlook.office365.com/.default")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return "", fmt.Errorf("HTTP request to Microsoft token endpoint failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var tr microsoftTokenResponse
	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Microsoft client credentials token error [%s]: %s", tr.Error, tr.ErrorDesc)
	}

	if tr.AccessToken == "" {
		return "", fmt.Errorf("received empty access token from Microsoft")
	}

	cfg.AccessToken = tr.AccessToken
	cfg.TokenExpiry = time.Now().Unix() + tr.ExpiresIn

	_ = UpdateAccountTokens(cfg.Username, cfg.AccessToken, "", cfg.TokenExpiry)

	return cfg.AccessToken, nil
}

// MicrosoftDeviceCodeFlow guides the user through device code authorization in their browser.
func MicrosoftDeviceCodeFlow(accountName string, cfg *AccountConfig) error {
	if cfg.ClientID == "" {
		return fmt.Errorf("client_id is required for Microsoft OAuth2. Set client_id in configuration")
	}

	tenant := cfg.TenantID
	if tenant == "" {
		tenant = "common"
	}

	deviceEndpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/devicecode", tenant)
	tokenEndpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("scope", defaultMicrosoftScope)

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

			fmt.Println("Testing IMAP connection to outlook.office365.com...")
			if testErr := TestConnection(*cfg); testErr != nil {
				fmt.Fprintf(os.Stderr, "⚠️ Note: Connection test returned: %v\n", testErr)
			} else {
				fmt.Println("✅ Connected and authenticated successfully via XOAUTH2!")
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
