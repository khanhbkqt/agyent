package zalo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agyent/internal/core/retry"
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

// ZaloUser represents a user or bot on Zalo Bot Platform.
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

// ZaloAttachmentPayload represents the nested payload inside a Zalo attachment object.
type ZaloAttachmentPayload struct {
	URL          string `json:"url,omitempty"`
	ImageURL     string `json:"image_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Thumbnail    string `json:"thumbnail,omitempty"`
	SRC          string `json:"src,omitempty"`
	Link         string `json:"link,omitempty"`
	FileID       string `json:"file_id,omitempty"`
	FileName     string `json:"file_name,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Name         string `json:"name,omitempty"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	Caption      string `json:"caption,omitempty"`
}

// ZaloAttachment represents an attached photo, document, or audio.
type ZaloAttachment struct {
	Type         string                 `json:"type"` // "photo", "image", "document", "file", "audio", "voice", "video", "sticker"
	URL          string                 `json:"url,omitempty"`
	ImageURL     string                 `json:"image_url,omitempty"`
	ThumbnailURL string                 `json:"thumbnail_url,omitempty"`
	SRC          string                 `json:"src,omitempty"`
	Link         string                 `json:"link,omitempty"`
	FileID       string                 `json:"file_id,omitempty"`
	FileName     string                 `json:"file_name,omitempty"`
	FileSize     int64                  `json:"file_size,omitempty"`
	Caption      string                 `json:"caption,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Title        string                 `json:"title,omitempty"`
	Payload      *ZaloAttachmentPayload `json:"payload,omitempty"`
}

// GetEffectiveURL returns the resolved media URL from either top-level fields or nested payload.
func (a ZaloAttachment) GetEffectiveURL() string {
	if u := strings.TrimSpace(a.URL); u != "" {
		return u
	}
	if u := strings.TrimSpace(a.ImageURL); u != "" {
		return u
	}
	if u := strings.TrimSpace(a.SRC); u != "" {
		return u
	}
	if u := strings.TrimSpace(a.Link); u != "" {
		return u
	}
	if a.Payload != nil {
		if u := strings.TrimSpace(a.Payload.URL); u != "" {
			return u
		}
		if u := strings.TrimSpace(a.Payload.ImageURL); u != "" {
			return u
		}
		if u := strings.TrimSpace(a.Payload.SRC); u != "" {
			return u
		}
		if u := strings.TrimSpace(a.Payload.Link); u != "" {
			return u
		}
		if u := strings.TrimSpace(a.Payload.ThumbnailURL); u != "" {
			return u
		}
		if u := strings.TrimSpace(a.Payload.Thumbnail); u != "" {
			return u
		}
	}
	return ""
}

// GetEffectiveFileID returns the resolved file ID.
func (a ZaloAttachment) GetEffectiveFileID() string {
	if id := strings.TrimSpace(a.FileID); id != "" {
		return id
	}
	if a.Payload != nil && strings.TrimSpace(a.Payload.FileID) != "" {
		return strings.TrimSpace(a.Payload.FileID)
	}
	return ""
}

// GetEffectiveFileName returns the resolved file name.
func (a ZaloAttachment) GetEffectiveFileName() string {
	if n := strings.TrimSpace(a.FileName); n != "" {
		return n
	}
	if n := strings.TrimSpace(a.Title); n != "" {
		return n
	}
	if a.Payload != nil {
		if n := strings.TrimSpace(a.Payload.FileName); n != "" {
			return n
		}
		if n := strings.TrimSpace(a.Payload.Name); n != "" {
			return n
		}
		if n := strings.TrimSpace(a.Payload.Title); n != "" {
			return n
		}
	}
	return ""
}

// GetEffectiveFileSize returns the declared file size.
func (a ZaloAttachment) GetEffectiveFileSize() int64 {
	if a.FileSize > 0 {
		return a.FileSize
	}
	if a.Payload != nil {
		if a.Payload.FileSize > 0 {
			return a.Payload.FileSize
		}
		if a.Payload.Size > 0 {
			return a.Payload.Size
		}
	}
	return 0
}

// GetEffectiveCaption returns the resolved caption or description.
func (a ZaloAttachment) GetEffectiveCaption() string {
	if c := strings.TrimSpace(a.Caption); c != "" {
		return c
	}
	if c := strings.TrimSpace(a.Description); c != "" {
		return c
	}
	if a.Payload != nil {
		if c := strings.TrimSpace(a.Payload.Caption); c != "" {
			return c
		}
		if c := strings.TrimSpace(a.Payload.Description); c != "" {
			return c
		}
	}
	return ""
}

// ZaloInboundMessage represents an inbound message from Zalo Bot Platform.
type ZaloInboundMessage struct {
	MessageID   string              `json:"message_id"`
	From        ZaloUser            `json:"from"`
	Chat        ZaloChat            `json:"chat"`
	Date        int64               `json:"date"`
	Text        string              `json:"text"`
	Caption     string              `json:"caption,omitempty"`
	Description string              `json:"description,omitempty"`
	Attachments []ZaloAttachment    `json:"attachments,omitempty"`
	Photo       []ZaloAttachment    `json:"photo,omitempty"`
	Image       *ZaloAttachment     `json:"image,omitempty"`
	Document    *ZaloAttachment     `json:"document,omitempty"`
	Audio       *ZaloAttachment     `json:"audio,omitempty"`
	Voice       *ZaloAttachment     `json:"voice,omitempty"`
	Video       *ZaloAttachment     `json:"video,omitempty"`
	ReplyToMsg  *ZaloInboundMessage `json:"reply_to_message,omitempty"`
}

// CollectAttachments aggregates all attachments across all schema variations.
func (m *ZaloInboundMessage) CollectAttachments() []ZaloAttachment {
	if m == nil {
		return nil
	}
	var res []ZaloAttachment
	res = append(res, m.Attachments...)
	for _, p := range m.Photo {
		if p.Type == "" {
			p.Type = "photo"
		}
		res = append(res, p)
	}
	if m.Image != nil {
		img := *m.Image
		if img.Type == "" {
			img.Type = "photo"
		}
		res = append(res, img)
	}
	if m.Document != nil {
		doc := *m.Document
		if doc.Type == "" {
			doc.Type = "document"
		}
		res = append(res, doc)
	}
	if m.Audio != nil {
		aud := *m.Audio
		if aud.Type == "" {
			aud.Type = "audio"
		}
		res = append(res, aud)
	}
	if m.Voice != nil {
		v := *m.Voice
		if v.Type == "" {
			v.Type = "voice"
		}
		res = append(res, v)
	}
	if m.Video != nil {
		vid := *m.Video
		if vid.Type == "" {
			vid.Type = "video"
		}
		res = append(res, vid)
	}
	return res
}

// ZaloUpdate represents an update item in getUpdates or Webhook payload.
type ZaloUpdate struct {
	UpdateID int64               `json:"update_id"`
	Message  *ZaloInboundMessage `json:"message,omitempty"`
	Event    string              `json:"event,omitempty"`
}

// APIResponse is the standard envelope returned by Zalo Bot Platform, supporting
// both Telegram-compatible (ok, result, error_code, description) and Zalo-native
// (error, message, data) response structures.
type APIResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Error       int             `json:"error,omitempty"`
	Message     string          `json:"message,omitempty"`
	Data        json.RawMessage `json:"data,omitempty"`
}

// Normalize harmonizes Telegram-style and Zalo-native envelope fields into canonical
// OK, Result, ErrorCode, and Description values.
func (r *APIResponse) Normalize() {
	if r == nil {
		return
	}
	// 1. Error code normalization:
	if r.ErrorCode == 0 && r.Error != 0 {
		r.ErrorCode = r.Error
	}

	// 2. Description normalization:
	if r.Description == "" && r.Message != "" {
		r.Description = r.Message
	}

	// 3. Payload normalization:
	if len(r.Result) == 0 && len(r.Data) > 0 {
		r.Result = r.Data
	}

	// 4. Success status normalization:
	if r.ErrorCode != 0 || r.Error != 0 {
		r.OK = false
	} else if !r.OK {
		// If neither Error nor ErrorCode is non-zero, infer success when data/result is present or message indicates success
		if len(r.Result) > 0 || len(r.Data) > 0 ||
			strings.EqualFold(r.Description, "success") || strings.EqualFold(r.Description, "ok") ||
			strings.EqualFold(r.Message, "success") || strings.EqualFold(r.Message, "ok") {
			r.OK = true
		}
	}
}

// APIError represents an error returned by the Zalo Bot Platform API or HTTP layer.
type APIError struct {
	StatusCode  int
	ErrorCode   int
	Description string
	RawBody     string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.ErrorCode != 0 && e.Description != "" {
		return fmt.Sprintf("zalo API error [%d]: %s", e.ErrorCode, e.Description)
	}
	if e.StatusCode != 0 && e.Description != "" {
		return fmt.Sprintf("zalo API returned HTTP %d: %s", e.StatusCode, e.Description)
	}
	if e.StatusCode != 0 && e.RawBody != "" {
		return fmt.Sprintf("zalo API returned HTTP %d: %s", e.StatusCode, e.RawBody)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("zalo API returned HTTP %d", e.StatusCode)
	}
	return "zalo API error"
}

// IsTimeout returns true if the error represents an idle request timeout (HTTP 408 or ErrorCode 408 / "Request timeout").
func (e *APIError) IsTimeout() bool {
	if e == nil {
		return false
	}
	if e.StatusCode == http.StatusRequestTimeout || e.ErrorCode == 408 {
		return true
	}
	desc := strings.ToLower(e.Description)
	if strings.Contains(desc, "request timeout") || strings.Contains(desc, "timeout") {
		return true
	}
	raw := strings.ToLower(e.RawBody)
	return strings.Contains(raw, "408") || strings.Contains(raw, "request timeout")
}

// IsTimeoutError reports whether an error indicates an idle polling timeout or network timeout from Zalo.
func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsTimeout()
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "408") ||
		strings.Contains(errStr, "request timeout") ||
		strings.Contains(errStr, "context deadline exceeded") ||
		strings.Contains(errStr, "client.timeout exceeded") ||
		strings.Contains(errStr, "timeout awaiting response headers") ||
		strings.Contains(errStr, "i/o timeout")
}


// Client interacts with the Zalo Bot Platform via HTTP REST with built-in retry and backoff.
type Client struct {
	botToken   string
	apiBaseURL string
	httpClient *http.Client
}

// NewClient creates a new Zalo Bot API client with standard connection pooling.
func NewClient(botToken, apiURL string, httpClient ...*http.Client) *Client {
	baseURL := strings.TrimRight(apiURL, "/")
	if baseURL == "" {
		baseURL = DefaultAPIURL
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:      10,
			IdleConnTimeout:   30 * time.Second,
			DisableKeepAlives: false,
		},
	}
	if len(httpClient) > 0 && httpClient[0] != nil {
		client = httpClient[0]
	}
	return &Client{
		botToken:   strings.TrimSpace(botToken),
		apiBaseURL: baseURL,
		httpClient: client,
	}
}

// BotToken returns the active bot token.
func (c *Client) BotToken() string {
	return c.botToken
}

// BaseURL returns the configured API base URL.
func (c *Client) BaseURL() string {
	return c.apiBaseURL
}

// endpoint returns the full URL for a bot method.
func (c *Client) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.apiBaseURL, c.botToken, method)
}

// executeRequest sends an HTTP POST request with exponential backoff and jitter for transient errors.
func (c *Client) executeRequest(ctx context.Context, method string, payload interface{}, out interface{}) error {
	var bodyBytes []byte
	if payload != nil {
		var err error
		bodyBytes, err = json.Marshal(payload)
		if err != nil {
			return retry.Permanent(fmt.Errorf("failed to marshal request body for %s: %w", method, err))
		}
	}

	url := c.endpoint(method)

	return retry.Do(ctx, func(turnCtx context.Context) error {
		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(turnCtx, http.MethodPost, url, bodyReader)
		if err != nil {
			return retry.Permanent(fmt.Errorf("failed to create request for %s: %w", method, err))
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "agyent-gateway/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Network error, connection reset, or timeout: allow retry
			return fmt.Errorf("zalo API HTTP request %s failed: %w", method, err)
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		if err != nil {
			return fmt.Errorf("failed to read response body for %s: %w", method, err)
		}

		// Handle rate limits (429) or transient 5xx server errors
		if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
			return fmt.Errorf("transient Zalo API HTTP %d: %s", resp.StatusCode, string(respBytes))
		}

		// Permanent HTTP error (400, 401, 403, 404, etc.)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiResp APIResponse
			_ = json.Unmarshal(respBytes, &apiResp)
			apiResp.Normalize()
			desc := apiResp.Description
			if desc == "" {
				desc = string(respBytes)
			}
			return retry.Permanent(&APIError{
				StatusCode:  resp.StatusCode,
				ErrorCode:   apiResp.ErrorCode,
				Description: desc,
				RawBody:     string(respBytes),
			})
		}

		var apiResp APIResponse
		if err := json.Unmarshal(respBytes, &apiResp); err != nil {
			return retry.Permanent(fmt.Errorf("failed to decode Zalo API envelope: %w (body: %s)", err, string(respBytes)))
		}
		apiResp.Normalize()

		if !apiResp.OK && apiResp.ErrorCode != 0 {
			// Specific retryable error codes from Zalo Bot Platform (e.g. rate limit / server busy)
			if apiResp.ErrorCode == 429 || apiResp.ErrorCode == -1 {
				return fmt.Errorf("transient Zalo API error [%d]: %s", apiResp.ErrorCode, apiResp.Description)
			}
			return retry.Permanent(&APIError{
				StatusCode:  resp.StatusCode,
				ErrorCode:   apiResp.ErrorCode,
				Description: apiResp.Description,
				RawBody:     string(respBytes),
			})
		}

		if out != nil && len(apiResp.Result) > 0 {
			if err := json.Unmarshal(apiResp.Result, out); err != nil {
				return retry.Permanent(fmt.Errorf("failed to unmarshal API result for %s: %w", method, err))
			}
		}
		return nil
	}, retry.WithMaxAttempts(3), retry.WithInitialInterval(300*time.Millisecond), retry.WithMaxInterval(3*time.Second))
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
	var raw json.RawMessage
	err := c.executeRequest(ctx, "getUpdates", payload, &raw)
	if err != nil {
		if IsTimeoutError(err) {
			// Zalo long-polling timeout (HTTP 408 or ErrorCode 408 / "Request timeout")
			// is the expected idle poll completion when no new messages arrived.
			return []ZaloUpdate{}, nil
		}
		return nil, err
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	if trimmed[0] == '{' {
		var update ZaloUpdate
		if err := json.Unmarshal(trimmed, &update); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Zalo update object: %w", err)
		}
		if update.UpdateID == 0 && update.Message == nil && update.Event == "" {
			return nil, nil
		}
		return []ZaloUpdate{update}, nil
	}

	if trimmed[0] == '[' {
		var updates []ZaloUpdate
		if err := json.Unmarshal(trimmed, &updates); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Zalo update array: %w", err)
		}
		return updates, nil
	}

	return nil, fmt.Errorf("unexpected JSON format for getUpdates: %s", string(trimmed))
}

// uploadMultipart handles multipart file upload (photos, documents) with retry.
func (c *Client) uploadMultipart(ctx context.Context, method, fieldName, chatID, filePath, caption string) error {
	url := c.endpoint(method)

	return retry.Do(ctx, func(turnCtx context.Context) error {
		file, err := os.Open(filePath)
		if err != nil {
			return retry.Permanent(fmt.Errorf("failed to open file %s: %w", filePath, err))
		}
		defer file.Close()

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		_ = writer.WriteField("chat_id", chatID)
		if caption != "" {
			_ = writer.WriteField("caption", caption)
			_ = writer.WriteField("parse_mode", "markdown")
		}

		part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
		if err != nil {
			return retry.Permanent(fmt.Errorf("failed to create multipart field: %w", err))
		}
		if _, err := io.Copy(part, file); err != nil {
			return fmt.Errorf("failed to copy file payload: %w", err)
		}

		if err := writer.Close(); err != nil {
			return retry.Permanent(fmt.Errorf("failed to close multipart writer: %w", err))
		}

		req, err := http.NewRequestWithContext(turnCtx, http.MethodPost, url, body)
		if err != nil {
			return retry.Permanent(fmt.Errorf("failed to create multipart request: %w", err))
		}

		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("User-Agent", "agyent-gateway/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("%s HTTP request failed: %w", method, err)
		}
		defer resp.Body.Close()

		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
			return fmt.Errorf("transient Zalo %s HTTP %d: %s", method, resp.StatusCode, string(respBytes))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return retry.Permanent(fmt.Errorf("%s returned HTTP %d: %s", method, resp.StatusCode, string(respBytes)))
		}

		var apiResp APIResponse
		if err := json.Unmarshal(respBytes, &apiResp); err == nil && !apiResp.OK && apiResp.ErrorCode != 0 {
			return retry.Permanent(fmt.Errorf("%s returned API error [%d]: %s", method, apiResp.ErrorCode, apiResp.Description))
		}

		return nil
	}, retry.WithMaxAttempts(3), retry.WithInitialInterval(500*time.Millisecond), retry.WithMaxInterval(3*time.Second))
}

// SendPhoto uploads or sends an image attachment.
func (c *Client) SendPhoto(ctx context.Context, chatID, photo, caption string) error {
	photoURL := photo
	if !strings.HasPrefix(photo, "http://") && !strings.HasPrefix(photo, "https://") {
		uploaded, err := UploadPublicMedia(ctx, photo)
		if err != nil {
			return fmt.Errorf("failed to upload local photo to public CDN: %w", err)
		}
		photoURL = uploaded
	}

	payload := map[string]string{
		"chat_id": chatID,
		"photo":   photoURL,
	}
	if caption != "" {
		payload["caption"] = caption
	}
	return c.executeRequest(ctx, "sendPhoto", payload, nil)
}

// SendDocument uploads a document to public CDN and delivers the download link.
func (c *Client) SendDocument(ctx context.Context, chatID, filePath, caption string) error {
	docURL := filePath
	if !strings.HasPrefix(filePath, "http://") && !strings.HasPrefix(filePath, "https://") {
		uploaded, err := UploadPublicMedia(ctx, filePath)
		if err != nil {
			return fmt.Errorf("failed to upload local document to public CDN: %w", err)
		}
		docURL = uploaded
	}

	fileName := filepath.Base(filePath)
	text := fmt.Sprintf("📄 **Tài liệu:** [%s](%s)", fileName, docURL)
	if caption != "" {
		text = fmt.Sprintf("📄 **Tài liệu:** [%s](%s)\n%s", fileName, docURL, caption)
	}

	_, err := c.SendMessage(ctx, SendMessageRequest{
		ChatID:    chatID,
		Text:      text,
		ParseMode: "markdown",
	})
	return err
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

// ParseNumericID preserves provider-native numeric IDs and derives a stable,
// positive routing ID for providers that expose opaque string bot IDs. The
// domain session contract currently uses int64 bot identities.
func ParseNumericID(idStr string) int64 {
	if id, err := strconv.ParseInt(idStr, 10, 64); err == nil && id > 0 {
		return id
	}
	if idStr == "" {
		return 0
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(idStr))
	id := int64(hasher.Sum64() & uint64(^uint64(0)>>1))
	if id == 0 {
		return 1
	}
	return id
}
