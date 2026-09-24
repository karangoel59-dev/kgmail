package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type graphFolderListResponse struct {
	Value []struct {
		ID              string `json:"id"`
		DisplayName     string `json:"displayName"`
		TotalItemCount  int    `json:"totalItemCount"`
		UnreadItemCount int    `json:"unreadItemCount"`
	} `json:"value"`
	Error *graphError `json:"error,omitempty"`
}

type graphRecipient struct {
	EmailAddress struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	} `json:"emailAddress"`
}

type graphMessageItem struct {
	ID                string           `json:"id"`
	Subject           string           `json:"subject"`
	ReceivedDateTime  string           `json:"receivedDateTime"`
	SentDateTime      string           `json:"sentDateTime"`
	BodyPreview       string           `json:"bodyPreview"`
	InternetMessageId string           `json:"internetMessageId"`
	IsRead            bool             `json:"isRead"`
	From              *graphRecipient  `json:"from,omitempty"`
	Sender            *graphRecipient  `json:"sender,omitempty"`
	ToRecipients      []graphRecipient `json:"toRecipients,omitempty"`
	CcRecipients      []graphRecipient `json:"ccRecipients,omitempty"`
	Body              *struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body,omitempty"`
	HasAttachments bool `json:"hasAttachments"`
	Attachments    []struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
		Size        int    `json:"size"`
	} `json:"attachments,omitempty"`
}

type graphMessageListResponse struct {
	Value []graphMessageItem `json:"value"`
	Error *graphError        `json:"error,omitempty"`
}

type graphError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func getGraphAccessToken(cfg *AccountConfig) (string, error) {
	if cfg.AccessToken != "" && cfg.TokenExpiry > time.Now().Unix()+120 {
		return cfg.AccessToken, nil
	}

	tenant := cfg.TenantID
	if tenant == "" {
		tenant = "common"
	}

	endpoint := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/token", tenant)

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)

	if cfg.ClientSecret != "" && cfg.RefreshToken == "" {
		form.Set("client_secret", cfg.ClientSecret)
		form.Set("grant_type", "client_credentials")
		form.Set("scope", "https://graph.microsoft.com/.default")
	} else if cfg.RefreshToken != "" {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", cfg.RefreshToken)
		form.Set("scope", "https://graph.microsoft.com/.default offline_access")
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
	} else {
		return "", fmt.Errorf("account %s requires authorization; run 'kgmail auth-microsoft <account>'", cfg.Username)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.PostForm(endpoint, form)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	var tr microsoftTokenResponse
	if err := json.Unmarshal(bodyBytes, &tr); err != nil {
		return "", fmt.Errorf("failed to parse token JSON: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Microsoft token error [%s]: %s", tr.Error, tr.ErrorDesc)
	}

	cfg.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		cfg.RefreshToken = tr.RefreshToken
	}
	cfg.TokenExpiry = time.Now().Unix() + tr.ExpiresIn
	_ = UpdateAccountTokens(cfg.Username, cfg.AccessToken, cfg.RefreshToken, cfg.TokenExpiry)

	return cfg.AccessToken, nil
}

func graphAPIRequest(cfg *AccountConfig, method, apiURL string, body []byte) ([]byte, error) {
	token, err := getGraphAccessToken(cfg)
	if err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, apiURL, reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Graph API request to %s failed: %w", apiURL, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var gErr struct {
			Error graphError `json:"error"`
		}
		if json.Unmarshal(respBytes, &gErr) == nil && gErr.Error.Message != "" {
			return nil, fmt.Errorf("Microsoft Graph API [%s]: %s", gErr.Error.Code, gErr.Error.Message)
		}
		return nil, fmt.Errorf("Microsoft Graph API returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

func TestConnectionGraph(cfg AccountConfig) error {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/mailFolders/inbox", url.PathEscape(cfg.Username))
	_, err := graphAPIRequest(&cfg, "GET", url, nil)
	return err
}

func ListFoldersGraph(cfg AccountConfig) ([]string, error) {
	url := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/mailFolders?$top=50", url.PathEscape(cfg.Username))
	data, err := graphAPIRequest(&cfg, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	var resp graphFolderListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var folders []string
	for _, f := range resp.Value {
		folders = append(folders, fmt.Sprintf("%s (unread: %d, total: %d)", f.DisplayName, f.UnreadItemCount, f.TotalItemCount))
	}
	return folders, nil
}

func GetUnreadEmailsGraph(accountName string, cfg AccountConfig, folder string, limit int) ([]EmailSummary, error) {
	if limit <= 0 {
		limit = 10
	}

	folderPath := "mailFolders/inbox"
	if folder != "" && !strings.EqualFold(folder, "INBOX") {
		folderPath = fmt.Sprintf("mailFolders/%s", url.PathEscape(folder))
	}

	apiURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/%s/messages?$filter=isRead%%20eq%%20false&$top=%d&$select=id,subject,from,receivedDateTime,bodyPreview,internetMessageId&$orderby=receivedDateTime%%20desc",
		url.PathEscape(cfg.Username), folderPath, limit)

	data, err := graphAPIRequest(&cfg, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	var resp graphMessageListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var summaries []EmailSummary
	for _, m := range resp.Value {
		fromStr := ""
		if m.From != nil {
			if m.From.EmailAddress.Name != "" {
				fromStr = fmt.Sprintf("%s <%s>", m.From.EmailAddress.Name, m.From.EmailAddress.Address)
			} else {
				fromStr = m.From.EmailAddress.Address
			}
		}

		t, _ := time.Parse(time.RFC3339, m.ReceivedDateTime)

		summaries = append(summaries, EmailSummary{
			Account:   accountName,
			ID:        m.ID,
			From:      fromStr,
			Subject:   m.Subject,
			Date:      t,
			Snippet:   m.BodyPreview,
			MessageID: m.InternetMessageId,
		})
	}

	return summaries, nil
}

func SearchEmailsGraph(accountName string, cfg AccountConfig, query string, folder string, limit int) ([]EmailSummary, error) {
	if limit <= 0 {
		limit = 10
	}

	apiURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/messages?$search=\"%s\"&$top=%d&$select=id,subject,from,receivedDateTime,bodyPreview,internetMessageId",
		url.PathEscape(cfg.Username), url.QueryEscape(query), limit)

	data, err := graphAPIRequest(&cfg, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	var resp graphMessageListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	var summaries []EmailSummary
	for _, m := range resp.Value {
		fromStr := ""
		if m.From != nil {
			if m.From.EmailAddress.Name != "" {
				fromStr = fmt.Sprintf("%s <%s>", m.From.EmailAddress.Name, m.From.EmailAddress.Address)
			} else {
				fromStr = m.From.EmailAddress.Address
			}
		}

		t, _ := time.Parse(time.RFC3339, m.ReceivedDateTime)

		summaries = append(summaries, EmailSummary{
			Account:   accountName,
			ID:        m.ID,
			From:      fromStr,
			Subject:   m.Subject,
			Date:      t,
			Snippet:   m.BodyPreview,
			MessageID: m.InternetMessageId,
		})
	}

	return summaries, nil
}

func ReadEmailGraph(accountName string, cfg AccountConfig, id string, maxBodyLen int) (*EmailDetail, error) {
	if maxBodyLen <= 0 {
		maxBodyLen = defaultMaxBodyLen
	}

	apiURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/messages/%s?$expand=attachments($select=id,name,contentType,size)",
		url.PathEscape(cfg.Username), url.PathEscape(id))

	data, err := graphAPIRequest(&cfg, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	var m graphMessageItem
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	fromStr := ""
	if m.From != nil {
		if m.From.EmailAddress.Name != "" {
			fromStr = fmt.Sprintf("%s <%s>", m.From.EmailAddress.Name, m.From.EmailAddress.Address)
		} else {
			fromStr = m.From.EmailAddress.Address
		}
	}

	var toList, ccList []string
	for _, to := range m.ToRecipients {
		toList = append(toList, fmt.Sprintf("%s <%s>", to.EmailAddress.Name, to.EmailAddress.Address))
	}
	for _, cc := range m.CcRecipients {
		ccList = append(ccList, fmt.Sprintf("%s <%s>", cc.EmailAddress.Name, cc.EmailAddress.Address))
	}

	bodyContent := ""
	if m.Body != nil {
		if strings.EqualFold(m.Body.ContentType, "html") {
			bodyContent = StripHTML(m.Body.Content)
		} else {
			bodyContent = m.Body.Content
		}
	}
	if len(bodyContent) > maxBodyLen {
		bodyContent = bodyContent[:maxBodyLen] + "\n...[truncated by kgmail]"
	}

	var attNames []string
	for _, a := range m.Attachments {
		if a.Name != "" {
			attNames = append(attNames, a.Name)
		}
	}

	t, _ := time.Parse(time.RFC3339, m.ReceivedDateTime)

	return &EmailDetail{
		Account:     accountName,
		ID:          m.ID,
		MessageID:   m.InternetMessageId,
		From:        fromStr,
		To:          toList,
		Cc:          ccList,
		Subject:     m.Subject,
		Date:        t,
		Body:        bodyContent,
		Attachments: attNames,
	}, nil
}

func SendEmailGraph(cfg AccountConfig, to []string, subject, body string, isHTML bool) error {
	apiURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/sendMail", url.PathEscape(cfg.Username))

	type recipientObj struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	}

	var recipients []recipientObj
	for _, addr := range to {
		r := recipientObj{}
		r.EmailAddress.Address = addr
		recipients = append(recipients, r)
	}

	contentType := "Text"
	if isHTML {
		contentType = "HTML"
	}

	payload := map[string]any{
		"message": map[string]any{
			"subject": subject,
			"body": map[string]string{
				"contentType": contentType,
				"content":     body,
			},
			"toRecipients": recipients,
		},
		"saveToSentItems": true,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = graphAPIRequest(&cfg, "POST", apiURL, bodyBytes)
	if err != nil {
		if strings.Contains(err.Error(), "ErrorAccessDenied") || strings.Contains(err.Error(), "403") {
			return fmt.Errorf("sending email failed: Azure App %s lacks 'Mail.Send' permission in tenant %s (granted permissions: Mail.Read, Mail.ReadWrite)", cfg.ClientID, cfg.TenantID)
		}
		return err
	}
	return nil
}
