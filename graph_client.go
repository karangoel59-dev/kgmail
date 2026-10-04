package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

const graphBaseURL = "https://graph.microsoft.com/v1.0"

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
		Name    string `json:"name,omitempty"`
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

// graphWellKnownFolders maps common folder names (lowercased) to Graph well-known folder names.
var graphWellKnownFolders = map[string]string{
	"inbox":         "inbox",
	"drafts":        "drafts",
	"sent":          "sentitems",
	"sent items":    "sentitems",
	"sentitems":     "sentitems",
	"deleted":       "deleteditems",
	"deleted items": "deleteditems",
	"deleteditems":  "deleteditems",
	"trash":         "deleteditems",
	"junk":          "junkemail",
	"junk email":    "junkemail",
	"junkemail":     "junkemail",
	"spam":          "junkemail",
	"archive":       "archive",
	"outbox":        "outbox",
}

func graphUserURL(cfg AccountConfig) string {
	return fmt.Sprintf("%s/users/%s", graphBaseURL, url.PathEscape(cfg.Username))
}

// graphQueryEscape escapes a query parameter value; Graph expects %20 rather than '+' for spaces.
func graphQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func graphAPIRequest(cfg *AccountConfig, method, apiURL string, body []byte) ([]byte, error) {
	token, err := GetOrRefreshMicrosoftToken(cfg)
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
			return nil, fmt.Errorf("Microsoft Graph API HTTP %d [%s]: %s", resp.StatusCode, gErr.Error.Code, gErr.Error.Message)
		}
		return nil, fmt.Errorf("Microsoft Graph API returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

// splitFolderPath splits a "Parent/Child" folder path into its non-empty segments.
func splitFolderPath(path string) []string {
	var segments []string
	for _, seg := range strings.Split(path, "/") {
		if seg = strings.TrimSpace(seg); seg != "" {
			segments = append(segments, seg)
		}
	}
	return segments
}

// walkGraphFolder resolves as many segments of a folder path as exist. It returns the
// ID (or well-known name) of the deepest folder found and how many segments were resolved.
func walkGraphFolder(cfg *AccountConfig, segments []string) (string, int, error) {
	id := ""
	for i, seg := range segments {
		if i == 0 {
			if wk, ok := graphWellKnownFolders[strings.ToLower(seg)]; ok {
				id = wk
				continue
			}
		}

		listURL := graphUserURL(*cfg) + "/mailFolders"
		if id != "" {
			listURL += "/" + url.PathEscape(id) + "/childFolders"
		}
		apiURL := fmt.Sprintf("%s?$filter=displayName%%20eq%%20'%s'&$select=id",
			listURL, graphQueryEscape(strings.ReplaceAll(seg, "'", "''")))
		data, err := graphAPIRequest(cfg, "GET", apiURL, nil)
		if err != nil {
			return "", 0, fmt.Errorf("failed to look up folder %q: %w", seg, err)
		}
		var resp graphFolderListResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", 0, err
		}
		if len(resp.Value) == 0 {
			return id, i, nil
		}
		id = resp.Value[0].ID
	}
	return id, len(segments), nil
}

// resolveGraphFolder turns a folder name or "Parent/Child" path (as shown by ListFoldersGraph)
// into a Graph folder path segment. Well-known names map directly; other names are looked up
// by display name; a single unknown name is assumed to be a folder ID.
func resolveGraphFolder(cfg *AccountConfig, folder string) (string, error) {
	segments := splitFolderPath(folder)
	if len(segments) == 0 {
		return "inbox", nil
	}
	id, resolved, err := walkGraphFolder(cfg, segments)
	if err != nil {
		return "", err
	}
	if resolved == len(segments) {
		return url.PathEscape(id), nil
	}
	if len(segments) == 1 {
		return url.PathEscape(strings.TrimSpace(folder)), nil
	}
	return "", fmt.Errorf("folder %q not found", folder)
}

// ensureGraphFolder returns the ID of the folder at path, creating missing segments when create is true.
// It reports whether any folder was created.
func ensureGraphFolder(cfg *AccountConfig, path string, create bool) (string, bool, error) {
	segments := splitFolderPath(path)
	if len(segments) == 0 {
		return "", false, fmt.Errorf("folder name is required")
	}
	id, resolved, err := walkGraphFolder(cfg, segments)
	if err != nil {
		return "", false, err
	}
	if resolved == len(segments) {
		return id, false, nil
	}
	if !create {
		return "", false, fmt.Errorf("folder %q does not exist (use list_folders to see existing folders, or create it first)", path)
	}

	for _, seg := range segments[resolved:] {
		createURL := graphUserURL(*cfg) + "/mailFolders"
		if id != "" {
			createURL += "/" + url.PathEscape(id) + "/childFolders"
		}
		body, err := json.Marshal(map[string]string{"displayName": seg})
		if err != nil {
			return "", false, err
		}
		data, err := graphAPIRequest(cfg, "POST", createURL, body)
		if err != nil {
			return "", false, fmt.Errorf("failed to create folder %q: %w", seg, err)
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &created); err != nil {
			return "", false, err
		}
		id = created.ID
	}
	return id, true, nil
}

func formatGraphRecipient(r *graphRecipient) string {
	if r == nil {
		return ""
	}
	if r.EmailAddress.Name != "" && r.EmailAddress.Name != r.EmailAddress.Address {
		return fmt.Sprintf("%s <%s>", r.EmailAddress.Name, r.EmailAddress.Address)
	}
	return r.EmailAddress.Address
}

func graphSummaries(accountName string, data []byte) ([]EmailSummary, error) {
	var resp graphMessageListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	summaries := make([]EmailSummary, 0, len(resp.Value))
	for _, m := range resp.Value {
		t, _ := time.Parse(time.RFC3339, m.ReceivedDateTime)
		summaries = append(summaries, EmailSummary{
			Account:   accountName,
			ID:        m.ID,
			From:      formatGraphRecipient(m.From),
			Subject:   m.Subject,
			Date:      t,
			Snippet:   m.BodyPreview,
			MessageID: m.InternetMessageId,
		})
	}
	return summaries, nil
}

func TestConnectionGraph(cfg AccountConfig) error {
	_, err := graphAPIRequest(&cfg, "GET", graphUserURL(cfg)+"/mailFolders/inbox", nil)
	return err
}

func ListFoldersGraph(cfg AccountConfig) ([]string, error) {
	data, err := graphAPIRequest(&cfg, "GET", graphUserURL(cfg)+"/mailFolders?$top=100&$select=displayName", nil)
	if err != nil {
		return nil, err
	}

	var resp graphFolderListResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}

	// Return plain display names so they can be passed back as the folder argument.
	var folders []string
	for _, f := range resp.Value {
		folders = append(folders, f.DisplayName)
	}
	return folders, nil
}

func GetUnreadEmailsGraph(accountName string, cfg AccountConfig, folder string, limit int) ([]EmailSummary, error) {
	limit = clampLimit(limit)

	folderPath, err := resolveGraphFolder(&cfg, folder)
	if err != nil {
		return nil, err
	}

	// Graph rejects $orderby on a property that isn't also the first term of $filter
	// (InefficientFilter), hence the always-true receivedDateTime clause.
	apiURL := fmt.Sprintf("%s/mailFolders/%s/messages?$filter=receivedDateTime%%20ge%%201900-01-01T00:00:00Z%%20and%%20isRead%%20eq%%20false&$orderby=receivedDateTime%%20desc&$top=%d&$select=id,subject,from,receivedDateTime,bodyPreview,internetMessageId",
		graphUserURL(cfg), folderPath, limit)

	data, err := graphAPIRequest(&cfg, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	return graphSummaries(accountName, data)
}

func SearchEmailsGraph(accountName string, cfg AccountConfig, query string, folder string, limit int) ([]EmailSummary, error) {
	limit = clampLimit(limit)

	folderPath, err := resolveGraphFolder(&cfg, folder)
	if err != nil {
		return nil, err
	}

	// $search takes a double-quoted KQL string; embedded quotes must be backslash-escaped.
	escaped := strings.ReplaceAll(strings.ReplaceAll(query, `\`, `\\`), `"`, `\"`)
	apiURL := fmt.Sprintf("%s/mailFolders/%s/messages?$search=%s&$top=%d&$select=id,subject,from,receivedDateTime,bodyPreview,internetMessageId",
		graphUserURL(cfg), folderPath, graphQueryEscape(`"`+escaped+`"`), limit)

	data, err := graphAPIRequest(&cfg, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	return graphSummaries(accountName, data)
}

func ReadEmailGraph(accountName string, cfg AccountConfig, id string, maxBodyLen int) (*EmailDetail, error) {
	if maxBodyLen <= 0 {
		maxBodyLen = defaultMaxBodyLen
	}

	apiURL := fmt.Sprintf("%s/messages/%s?$expand=attachments($select=id,name,contentType,size)",
		graphUserURL(cfg), url.PathEscape(id))

	data, err := graphAPIRequest(&cfg, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}

	var m graphMessageItem
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	var toList, ccList []string
	for i := range m.ToRecipients {
		toList = append(toList, formatGraphRecipient(&m.ToRecipients[i]))
	}
	for i := range m.CcRecipients {
		ccList = append(ccList, formatGraphRecipient(&m.CcRecipients[i]))
	}

	bodyContent := ""
	if m.Body != nil {
		if strings.EqualFold(m.Body.ContentType, "html") {
			bodyContent = StripHTML(m.Body.Content)
		} else {
			bodyContent = m.Body.Content
		}
	}
	bodyContent = truncateBody(strings.TrimSpace(bodyContent), maxBodyLen)

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
		From:        formatGraphRecipient(m.From),
		To:          toList,
		Cc:          ccList,
		Subject:     m.Subject,
		Date:        t,
		Body:        bodyContent,
		Attachments: attNames,
	}, nil
}

func graphRecipients(addrs []*mail.Address) []graphRecipient {
	recipients := make([]graphRecipient, 0, len(addrs))
	for _, addr := range addrs {
		var r graphRecipient
		r.EmailAddress.Name = addr.Name
		r.EmailAddress.Address = addr.Address
		recipients = append(recipients, r)
	}
	return recipients
}

// graphFileAttachments converts attachments to Graph fileAttachment objects. They are sent
// inline with sendMail, which limits the combined size (larger files would need an upload session).
func graphFileAttachments(attachments []Attachment) ([]map[string]string, error) {
	total := 0
	files := make([]map[string]string, 0, len(attachments))
	for _, a := range attachments {
		total += len(a.Data)
		files = append(files, map[string]string{
			"@odata.type":  "#microsoft.graph.fileAttachment",
			"name":         a.Name,
			"contentType":  a.ContentType,
			"contentBytes": base64.StdEncoding.EncodeToString(a.Data),
		})
	}
	if total > maxGraphAttachmentBytes {
		return nil, fmt.Errorf("attachments total %s; Microsoft Graph accounts currently support up to %d MB of attachments per email",
			formatSize(total), maxGraphAttachmentBytes>>20)
	}
	return files, nil
}

func SendEmailGraph(cfg AccountConfig, msg OutgoingEmail) error {
	if msg.InReplyTo != "" {
		return fmt.Errorf("replying in-thread (in_reply_to) is not supported for Microsoft Graph accounts yet")
	}

	to, err := parseAddresses(msg.To)
	if err != nil {
		return err
	}
	cc, err := parseAddresses(msg.Cc)
	if err != nil {
		return err
	}
	bcc, err := parseAddresses(msg.Bcc)
	if err != nil {
		return err
	}

	contentType := "Text"
	if msg.IsHTML {
		contentType = "HTML"
	}

	message := map[string]any{
		"subject": msg.Subject,
		"body": map[string]string{
			"contentType": contentType,
			"content":     msg.Body,
		},
		"toRecipients": graphRecipients(to),
	}
	if len(cc) > 0 {
		message["ccRecipients"] = graphRecipients(cc)
	}
	if len(bcc) > 0 {
		message["bccRecipients"] = graphRecipients(bcc)
	}
	if len(msg.Attachments) > 0 {
		files, err := graphFileAttachments(msg.Attachments)
		if err != nil {
			return err
		}
		message["attachments"] = files
	}

	bodyBytes, err := json.Marshal(map[string]any{
		"message":         message,
		"saveToSentItems": true,
	})
	if err != nil {
		return err
	}

	_, err = graphAPIRequest(&cfg, "POST", graphUserURL(cfg)+"/sendMail", bodyBytes)
	if err != nil {
		if strings.Contains(err.Error(), "ErrorAccessDenied") || strings.Contains(err.Error(), "HTTP 403") {
			return fmt.Errorf("sending email failed: Azure App %s likely lacks the 'Mail.Send' permission in tenant %s: %w", cfg.ClientID, cfg.TenantID, err)
		}
		return err
	}
	return nil
}

// CreateFolderGraph creates a mail folder (and any missing parents) for a "Parent/Child" path.
// It reports false when the folder already existed.
func CreateFolderGraph(cfg AccountConfig, path string) (bool, error) {
	_, created, err := ensureGraphFolder(&cfg, path, true)
	return created, err
}

// MoveEmailsGraph moves messages to dest. Graph assigns moved messages new IDs, which are returned.
func MoveEmailsGraph(cfg AccountConfig, ids []string, dest string, createIfMissing bool) (*MoveResult, error) {
	destID, _, err := ensureGraphFolder(&cfg, dest, createIfMissing)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]string{"destinationId": destID})
	if err != nil {
		return nil, err
	}

	result := &MoveResult{}
	for _, id := range ids {
		data, err := graphAPIRequest(&cfg, "POST", fmt.Sprintf("%s/messages/%s/move", graphUserURL(cfg), url.PathEscape(id)), body)
		if err != nil {
			if strings.Contains(err.Error(), "HTTP 404") {
				result.NotFound = append(result.NotFound, id)
				continue
			}
			return result, fmt.Errorf("failed to move message %s: %w", id, err)
		}
		var moved struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &moved)
		result.Moved = append(result.Moved, MovedEmail{ID: id, NewID: moved.ID})
	}
	return result, nil
}
