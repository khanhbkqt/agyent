package wizard_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agyent/internal/config"
	"agyent/internal/wizard"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTokenFormat(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:    "Valid Token",
			token:   "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ1234567890",
			wantErr: false,
		},
		{
			name:    "Empty Token",
			token:   "",
			wantErr: true,
		},
		{
			name:    "Missing Colon",
			token:   "123456789ABCdefGHIjklMNOpqrSTUvwxYZ1234567890",
			wantErr: true,
		},
		{
			name:    "Non-numeric ID prefix",
			token:   "abc:ABCdefGHIjklMNOpqrSTUvwxYZ1234567890",
			wantErr: true,
		},
		{
			name:    "Too short secret",
			token:   "123456789:abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := wizard.ValidateTokenFormat(tt.token)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseAdminIDs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []int64
		wantErr  bool
	}{
		{
			name:     "Single ID",
			input:    "123456789",
			expected: []int64{123456789},
			wantErr:  false,
		},
		{
			name:     "Comma-separated IDs with spaces",
			input:    "123456, 789012,  345678",
			expected: []int64{123456, 789012, 345678},
			wantErr:  false,
		},
		{
			name:     "Semicolon-separated IDs",
			input:    "111; 222; 333",
			expected: []int64{111, 222, 333},
			wantErr:  false,
		},
		{
			name:     "Empty input",
			input:    "   ",
			expected: nil,
			wantErr:  true,
		},
		{
			name:     "Invalid non-integer characters",
			input:    "123456, not_a_number, 789",
			expected: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := wizard.ParseAdminIDs(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestVerifyTelegramToken_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/bot123456:VALID_TOKEN/getMe", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"ok": true,
			"result": {
				"id": 123456,
				"is_bot": true,
				"first_name": "agyent_test_bot",
				"username": "agyent_test_bot"
			}
		}`))
	}))
	defer server.Close()

	ctx := context.Background()
	resp, err := wizard.VerifyTelegramToken(ctx, server.URL, "123456:VALID_TOKEN")
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.OK)
	assert.Equal(t, "agyent_test_bot", resp.Result.Username)
	assert.Equal(t, int64(123456), resp.Result.ID)
}

func TestVerifyTelegramToken_InvalidToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{
			"ok": false,
			"error_code": 401,
			"description": "Unauthorized"
		}`))
	}))
	defer server.Close()

	ctx := context.Background()
	resp, err := wizard.VerifyTelegramToken(ctx, server.URL, "123456:INVALID_TOKEN")
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "Unauthorized")
}

func TestVerifyTelegramToken_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(6 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	resp, err := wizard.VerifyTelegramToken(ctx, server.URL, "123456:TIMEOUT_TOKEN")
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestRunWizard_NonInteractive(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	agentsDir := filepath.Join(tmpDir, "agents")

	opts := wizard.WizardOptions{
		NonInteractive:     true,
		ConfigPath:         configPath,
		DBPath:             filepath.Join(tmpDir, "test.db"),
		BotToken:           "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ1234567890",
		AdminUserIDs:       []int64{987654321},
		AgentsDir:          agentsDir,
		AGYPath:            "agy",
		DebounceSeconds:    2.5,
		CreateDefaultAgent: true,
	}

	ctx := context.Background()
	cfg, err := wizard.RunWizard(ctx, opts)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Check returned config
	assert.Equal(t, "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ1234567890", cfg.Telegram.BotToken)
	assert.Equal(t, []int64{987654321}, cfg.Telegram.AdminUserIDs)
	assert.Equal(t, agentsDir, cfg.Storage.AgentsDir)
	assert.Equal(t, 2.5, cfg.Storage.DebounceSeconds)

	// Check created file
	loaded, err := config.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, "123456789:ABCdefGHIjklMNOpqrSTUvwxYZ1234567890", loaded.Telegram.BotToken)

	// Check agent workspace dir created
	agentPath := config.ResolveAgentWorkspace(agentsDir, "agyent")
	info, err := os.Stat(agentPath)
	assert.NoError(t, err)
	assert.True(t, info.IsDir())
}
