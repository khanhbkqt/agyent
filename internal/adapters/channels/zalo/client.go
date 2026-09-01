package zalo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultAPIURL is the official production base URL for Zalo Bot Platform.
const DefaultAPIURL = "https://bot-api.zaloplatforms.com"

// TextStyleItem represents a styled run in Zalo Bot rich text.
// st supports: "b" (bold), "i" (italic), "u" (underline), "s" (strikethrough),
// "c_HEX" (color, e.g. "c_db342e"), "f_SIZE" (font size, e.g. "f_18", "f_20").
type TextStyleItem struct {
	Start  int      `json:"start"`
	Length int      `json:"len"`
	Styles []string `json:"st"`
}

// SendMessageRequest is the payload for sendMessage endpoint.
type SendMessageRequest struct {
	ChatID     string          `json:"chat_id"`
	Text       string          `json:"text"`
	ParseMode  string          `json:"parse_mode,omitempty"`  // "markdown", ""
	TextStyles []TextStyleItem `json:"text_styles,omitempty"` // Array of style runs
	ReplyToID  string          `json:"reply_to_message_id,omitempty"`
}

// SendChatActionRequest is the payload for sendChatAction endpoint.
type SendChatActionRequest struct {
	ChatID string `json:"chat_id"`
	Action string `json:"action"` // "typing", "upload_photo", "upload_document"
}

// ZaloUser represents a user on Zalo Bot Platform.
type ZaloUser struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Username  string `json:"username,omitempty"`
	IsBot     bool   `json:"is_bot,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// ZaloChat represents a chat/group on Zalo Bot Platform.
type ZaloChat struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "private", "group"
	Title    string `json:"title,omitempty"`
	ThreadID int64  `json:"thread_id,omitempty"`
}

// ZaloAttachment represents an attached photo, document, or audio.
type ZaloAttachment struct {
	Type     string `json:"type"` // "photo", "document", "sticker"
	URL      string `json:"url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}

// ZaloInboundMessage represents an inbound message from Zalo Bot Platform.
type ZaloInboundMessage struct {
	MessageID   string              `json:"message_id"`
	From        ZaloUser            `json:"from"`
	Chat        ZaloChat            `json:"chat"`
	Date        int64               `json:"date"`
	Text        string              `json:"text"`
	Attachments []ZaloAttachment    `json:"attachments,omitempty"`
	ReplyToMsg  *ZaloInboundMessage `json:"reply_to_message,omitempty"`
}

// ZaloUpdate represents an update item in getUpdates or Webhook payload.
type ZaloUpdate struct {
	UpdateID int64               `json:"update_id"`
	Message  *ZaloInboundMessage `json:"message,omitempty"`
	Event    string              `json:"event,omitempty"`
}

// APIResponse is the standard envelope returned by Zalo Bot Platform.
type APIResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
}

// Client interacts with the Zalo Bot Platform via HTTP REST.
type Client struct {
	botToken   string
	apiBaseURL string
	httpClient *http.Client
}

// NewClient creates a new Zalo Bot API client.
func NewClient(botToken, apiURL string) *Client {
	baseURL := strings.TrimRight(apiURL, "/")
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	return &Client{
		botToken:   strings.TrimSpace(botToken),
		apiBaseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// endpoint returns the full URL for a bot method.
func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.apiBaseURL, c.botToken, method)
}

// executeRequest sends an HTTP POST request with JSON payload.
func (c *Client) executeRequest(ctx context.Context, method string, payload interface{}, out interface{}) error {
	var bodyReader io.Reader
	if payload != nil {
		bodyBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal request body for %s: %w", method, err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	url := c.endpoint(method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request for %s: %w", method, err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("zalo API HTTP request %s failed: %w", method, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body for %s: %w", method, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("zalo API returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return fmt.Errorf("failed to decode Zalo API envelope: %w (body: %s)", err, string(respBytes))
	}

	if !apiResp.OK && apiResp.ErrorCode != 0 {
		return fmt.Errorf("zalo API error [%d]: %s", apiResp.ErrorCode, apiResp.Description)
	}

	if out != nil && len(apiResp.Result) > 0 {
		if err := json.Unmarshal(apiResp.Result, out); err != nil {
			return fmt.Errorf("failed to unmarshal API result for %s: %w", method, err)
		}
	}
	return nil
}

// GetMe fetches the bot profile.
func (c *Client) GetMe(ctx context.Context) (*ZaloUser, error) {
	var user ZaloUser
	err := c.executeRequest(ctx, "getMe", nil, &user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// SendMessage sends a text message to a chat/group.
func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (*ZaloInboundMessage, error) {
	if strings.TrimSpace(req.ChatID) == "" {
		return nil, errors.New("chat_id cannot be empty")
	}
	var msg ZaloInboundMessage
	err := c.executeRequest(ctx, "sendMessage", req, &msg)
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// SendChatAction triggers a typing or uploading status.
func (c *Client) SendChatAction(ctx context.Context, chatID string, action string) error {
	req := SendChatActionRequest{
		ChatID: chatID,
		Action: action,
	}
	return c.executeRequest(ctx, "sendChatAction", req, nil)
}

// GetUpdates polls for updates using long polling.
func (c *Client) GetUpdates(ctx context.Context, offset int64, limit int, timeoutSec int) ([]ZaloUpdate, error) {
	payload := map[string]interface{}{
		"offset":  offset,
		"limit":   limit,
		"timeout": timeoutSec,
	}
	var updates []ZaloUpdate
	err := c.executeRequest(ctx, "getUpdates", payload, &updates)
	if err != nil {
		return nil, err
	}
	return updates, nil
}

// SendPhoto uploads or sends an image attachment.
func (c *Client) SendPhoto(ctx context.Context, chatID, filePath, caption string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open photo file %s: %w", filePath, err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("chat_id", chatID)
	if caption != "" {
		_ = writer.WriteField("caption", caption)
		_ = writer.WriteField("parse_mode", "markdown")
	}

	part, err := writer.CreateFormFile("photo", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("failed to create multipart photo field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("failed to copy photo stream: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := c.endpoint("sendPhoto")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("failed to create sendPhoto request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sendPhoto HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendPhoto returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}
	return nil
}

// SendDocument uploads or sends a document/file attachment.
func (c *Client) SendDocument(ctx context.Context, chatID, filePath, caption string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open document file %s: %w", filePath, err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	_ = writer.WriteField("chat_id", chatID)
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}

	part, err := writer.CreateFormFile("document", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("failed to create multipart document field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("failed to copy document stream: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := c.endpoint("sendDocument")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("failed to create sendDocument request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "agyent-gateway/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sendDocument HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("sendDocument returned HTTP %d: %s", resp.StatusCode, string(respBytes))
	}
	return nil
}

// SetWebhook registers a webhook endpoint for instant updates.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secretToken string) error {
	payload := map[string]string{
		"url": webhookURL,
	}
	if secretToken != "" {
		payload["secret_token"] = secretToken
	}
	return c.executeRequest(ctx, "setWebhook", payload, nil)
}

// DeleteWebhook removes registered webhook.
func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.executeRequest(ctx, "deleteWebhook", nil, nil)
}
