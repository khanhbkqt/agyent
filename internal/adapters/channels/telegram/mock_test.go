package telegram

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

type SentMessageRecord struct {
	ChatID       int64
	ThreadID     int64
	Text         string
	ParseMode    string
	ReplyToMsgID int64
	MessageID    int64
}

type EditMessageRecord struct {
	ChatID    int64
	MessageID int64
	Text      string
	ParseMode string
}

type ChatActionRecord struct {
	ChatID   int64
	ThreadID int64
	Action   string
}

type SentMediaRecord struct {
	ChatID   int64
	ThreadID int64
	Type     string // "photo", "document"
	FileName string
	Caption  string
	Bytes    []byte
}

type MockTelegramServer struct {
	Server             *httptest.Server
	Token              string
	mu                 sync.Mutex
	msgSeq             int64
	SentMessages       []SentMessageRecord
	EditMessages       []EditMessageRecord
	ChatActions        []ChatActionRecord
	SentMedia          []SentMediaRecord
	NextErrorStatus    int
	NextErrorBody      string
	NextEditError      error
	Simulate429Once    bool
	Simulate400Once    bool
	RetryAfterSec      int
	FilesMap           map[string][]byte
	GetMeCount         int64
	RegisteredCommands []gotgbot.BotCommand
}

func NewMockTelegramServer(token string) *MockTelegramServer {
	mock := &MockTelegramServer{
		Token:         token,
		msgSeq:        100,
		FilesMap:      make(map[string][]byte),
		RetryAfterSec: 1,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.handleRequest(w, r)
	})

	mock.Server = httptest.NewServer(handler)
	return mock
}

func (m *MockTelegramServer) Close() {
	if m.Server != nil {
		m.Server.Close()
	}
}

func (m *MockTelegramServer) NewBot() (*gotgbot.Bot, error) {
	opts := &gotgbot.BotOpts{
		BotClient: &gotgbot.BaseBotClient{
			DefaultRequestOpts: &gotgbot.RequestOpts{
				APIURL: m.Server.URL,
			},
		},
	}
	return gotgbot.NewBot(m.Token, opts)
}

func parseRequestParams(r *http.Request) map[string]any {
	res := make(map[string]any)
	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "application/json") {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &res)
		return res
	}

	_ = r.ParseMultipartForm(32 << 20)
	_ = r.ParseForm()

	for k, v := range r.Form {
		if len(v) > 0 {
			res[k] = v[0]
		}
	}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			res[k] = v[0]
		}
	}

	return res
}

func (m *MockTelegramServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := r.URL.Path

	// Serve files: /file/bot<token>/<filepath>
	if strings.HasPrefix(path, "/file/bot") {
		filePath := strings.TrimPrefix(path, fmt.Sprintf("/file/bot%s/", m.Token))
		if data, ok := m.FilesMap[filePath]; ok {
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("mock file content"))
		return
	}

	expectedPrefix := fmt.Sprintf("/bot%s/", m.Token)
	if !strings.HasPrefix(path, expectedPrefix) {
		http.NotFound(w, r)
		return
	}

	method := strings.TrimPrefix(path, expectedPrefix)

	// Check simulated global next error
	if m.NextErrorStatus != 0 {
		w.WriteHeader(m.NextErrorStatus)
		if m.NextErrorBody != "" {
			w.Write([]byte(m.NextErrorBody))
		}
		m.NextErrorStatus = 0
		m.NextErrorBody = ""
		return
	}

	params := parseRequestParams(r)

	switch method {
	case "getMe":
		atomic.AddInt64(&m.GetMeCount, 1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"id":         123456789,
				"is_bot":     true,
				"first_name": "AgyentBot",
				"username":   "agyent_test_bot",
			},
		})

	case "deleteWebhook", "setWebhook":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})

	case "setMyCommands":
		m.RegisteredCommands = nil
		var rawCmds []gotgbot.BotCommand
		if cmdStr, ok := params["commands"].(string); ok {
			_ = json.Unmarshal([]byte(cmdStr), &rawCmds)
		} else if cmdsList, ok := params["commands"].([]any); ok {
			bytes, _ := json.Marshal(cmdsList)
			_ = json.Unmarshal(bytes, &rawCmds)
		}
		m.RegisteredCommands = rawCmds
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})

	case "getMyCommands":
		w.Header().Set("Content-Type", "application/json")
		cmds := m.RegisteredCommands
		if cmds == nil {
			cmds = []gotgbot.BotCommand{}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": cmds,
		})

	case "deleteMyCommands":
		m.RegisteredCommands = nil
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})

	case "setChatMenuButton":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})

	case "getUpdates":
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": []any{},
		})

	case "sendMessage":
		if m.Simulate429Once {
			m.Simulate429Once = false
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  429,
				"description": "Too Many Requests: retry after 1",
				"parameters": map[string]any{
					"retry_after": m.RetryAfterSec,
				},
			})
			return
		}

		parseMode, _ := params["parse_mode"].(string)
		if m.Simulate400Once && parseMode != "" {
			m.Simulate400Once = false
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  400,
				"description": "Bad Request: can't parse entities in message text",
			})
			return
		}

		m.msgSeq++
		msgID := m.msgSeq

		chatID := getInt64FromMap(params, "chat_id")
		threadID := getInt64FromMap(params, "message_thread_id")
		text, _ := params["text"].(string)
		replyTo := getInt64FromMap(params, "reply_to_message_id")

		m.SentMessages = append(m.SentMessages, SentMessageRecord{
			ChatID:       chatID,
			ThreadID:     threadID,
			Text:         text,
			ParseMode:    parseMode,
			ReplyToMsgID: replyTo,
			MessageID:    msgID,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": msgID,
				"chat": map[string]any{
					"id": chatID,
				},
				"text": text,
				"date": 1724580000,
			},
		})

	case "editMessageText":
		if m.Simulate429Once {
			m.Simulate429Once = false
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  429,
				"description": "Too Many Requests: retry after 1",
				"parameters": map[string]any{
					"retry_after": m.RetryAfterSec,
				},
			})
			return
		}

		parseMode, _ := params["parse_mode"].(string)
		if m.Simulate400Once && parseMode != "" {
			m.Simulate400Once = false
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{
				"ok":          false,
				"error_code":  400,
				"description": "Bad Request: can't parse entities in message text",
			})
			return
		}

		chatID := getInt64FromMap(params, "chat_id")
		msgID := getInt64FromMap(params, "message_id")
		text, _ := params["text"].(string)

		m.EditMessages = append(m.EditMessages, EditMessageRecord{
			ChatID:    chatID,
			MessageID: msgID,
			Text:      text,
			ParseMode: parseMode,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": msgID,
				"chat": map[string]any{
					"id": chatID,
				},
				"text": text,
				"date": 1724580000,
			},
		})

	case "sendChatAction":
		chatID := getInt64FromMap(params, "chat_id")
		threadID := getInt64FromMap(params, "message_thread_id")
		action, _ := params["action"].(string)

		m.ChatActions = append(m.ChatActions, ChatActionRecord{
			ChatID:   chatID,
			ThreadID: threadID,
			Action:   action,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})

	case "sendPhoto":
		m.msgSeq++
		msgID := m.msgSeq
		caption, _ := params["caption"].(string)
		chatID := getInt64FromMap(params, "chat_id")
		threadID := getInt64FromMap(params, "message_thread_id")

		file, header, err := r.FormFile("photo")
		var fileBytes []byte
		fileName := ""
		if err == nil && file != nil {
			defer file.Close()
			fileBytes, _ = io.ReadAll(file)
			fileName = header.Filename
		}

		m.SentMedia = append(m.SentMedia, SentMediaRecord{
			ChatID:   chatID,
			ThreadID: threadID,
			Type:     "photo",
			FileName: fileName,
			Caption:  caption,
			Bytes:    fileBytes,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": msgID,
				"chat": map[string]any{
					"id": chatID,
				},
				"caption": caption,
			},
		})

	case "sendDocument":
		m.msgSeq++
		msgID := m.msgSeq
		caption, _ := params["caption"].(string)
		chatID := getInt64FromMap(params, "chat_id")
		threadID := getInt64FromMap(params, "message_thread_id")

		file, header, err := r.FormFile("document")
		var fileBytes []byte
		fileName := ""
		if err == nil && file != nil {
			defer file.Close()
			fileBytes, _ = io.ReadAll(file)
			fileName = header.Filename
		}

		m.SentMedia = append(m.SentMedia, SentMediaRecord{
			ChatID:   chatID,
			ThreadID: threadID,
			Type:     "document",
			FileName: fileName,
			Caption:  caption,
			Bytes:    fileBytes,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": msgID,
				"chat": map[string]any{
					"id": chatID,
				},
				"caption": caption,
			},
		})

	case "getFile":
		fileID, _ := params["file_id"].(string)
		filePath := fmt.Sprintf("mock_files/%s.bin", fileID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"file_id":   fileID,
				"file_path": filePath,
				"file_size": 1024,
			},
		})

	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": true,
		})
	}
}

func getInt64FromMap(m map[string]any, key string) int64 {
	val, ok := m[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		id, _ := strconv.ParseInt(v, 10, 64)
		return id
	}
	return 0
}
