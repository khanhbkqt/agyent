package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// TC-TG-01: Unauthorized 1-1 User Ingestion
func TestRouter_UnauthorizedPrivateUser(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.AdminUserIDs = []int64{111222}

	inbound := make(chan domain.CanonicalMessage, 10)
	router := NewRouter(cfg, nil, inbound, nil)

	update := &gotgbot.Update{
		UpdateId: 1,
		Message: &gotgbot.Message{
			MessageId: 10,
			Date:      time.Now().Unix(),
			Chat: gotgbot.Chat{
				Id:   999888,
				Type: "private",
			},
			From: &gotgbot.User{
				Id:        999888, // Not in AdminUserIDs
				Username:  "stranger",
				FirstName: "Bob",
			},
			Text: "Hello there",
		},
	}

	err := router.HandleUpdate(context.Background(), nil, update)
	require.NoError(t, err)

	// Should not deliver to inbound
	assert.Equal(t, 0, len(inbound))
}

// TC-TG-02: Authorized Admin 1-1 Ingestion
func TestRouter_AuthorizedAdminPrivateUser(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.AdminUserIDs = []int64{111222}

	inbound := make(chan domain.CanonicalMessage, 10)
	router := NewRouter(cfg, nil, inbound, nil)

	update := &gotgbot.Update{
		UpdateId: 2,
		Message: &gotgbot.Message{
			MessageId: 20,
			Date:      time.Now().Unix(),
			Chat: gotgbot.Chat{
				Id:   111222,
				Type: "private",
			},
			From: &gotgbot.User{
				Id:        111222,
				Username:  "admin_user",
				FirstName: "Alice",
				LastName:  "Admin",
			},
			Text: "Deploy current project",
		},
	}

	err := router.HandleUpdate(context.Background(), nil, update)
	require.NoError(t, err)

	select {
	case msg := <-inbound:
		assert.Equal(t, "20", msg.ID)
		assert.Equal(t, "telegram", msg.Channel)
		assert.Equal(t, "111222", msg.Sender.ID)
		assert.Equal(t, "admin_user", msg.Sender.Username)
		assert.Equal(t, "Alice Admin", msg.Sender.FullName)
		assert.Equal(t, "111222", msg.Chat.ID)
		assert.Equal(t, "private", msg.Chat.Type)
		assert.Equal(t, "Deploy current project", msg.Text)
		assert.Equal(t, "Deploy current project", msg.RawText)
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for canonical message")
	}
}

// TC-TG-03: Group Message Without Mention or Reply (Ignored)
func TestRouter_GroupMessageWithoutMentionOrReply(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.AllowedGroupIDs = []string{"-100123456789"}

	inbound := make(chan domain.CanonicalMessage, 10)
	router := NewRouter(cfg, nil, inbound, nil)

	mockBot := &gotgbot.Bot{
		User: gotgbot.User{
			Id:       555,
			Username: "agyent_bot",
		},
	}

	update := &gotgbot.Update{
		UpdateId: 3,
		Message: &gotgbot.Message{
			MessageId: 30,
			Date:      time.Now().Unix(),
			Chat: gotgbot.Chat{
				Id:    -100123456789,
				Type:  "supergroup",
				Title: "Dev Group",
			},
			From: &gotgbot.User{
				Id:        777,
				Username:  "dev1",
				FirstName: "Dave",
			},
			Text: "Hello teammates, what's up?",
		},
	}

	err := router.HandleUpdate(context.Background(), mockBot, update)
	require.NoError(t, err)
	assert.Equal(t, 0, len(inbound))
}

// TC-TG-04: Group Message With @mention or Bot Reply
func TestRouter_GroupMessageWithMentionOrReply(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.AllowedGroupIDs = []string{"-100123456789"}

	mockBot := &gotgbot.Bot{
		User: gotgbot.User{
			Id:       555,
			Username: "agyent_bot",
		},
	}

	t.Run("with @mention", func(t *testing.T) {
		inbound := make(chan domain.CanonicalMessage, 10)
		router := NewRouter(cfg, mockBot, inbound, nil)

		update := &gotgbot.Update{
			UpdateId: 4,
			Message: &gotgbot.Message{
				MessageId: 40,
				Date:      time.Now().Unix(),
				Chat: gotgbot.Chat{
					Id:    -100123456789,
					Type:  "supergroup",
					Title: "Dev Group",
				},
				From: &gotgbot.User{
					Id:        777,
					Username:  "dev1",
					FirstName: "Dave",
				},
				Text: "@agyent_bot review the code",
			},
		}

		err := router.HandleUpdate(context.Background(), mockBot, update)
		require.NoError(t, err)

		select {
		case msg := <-inbound:
			assert.True(t, msg.IsMentioned)
			assert.Equal(t, "review the code", msg.Text)
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for message")
		}
	})

	t.Run("with reply to bot", func(t *testing.T) {
		inbound := make(chan domain.CanonicalMessage, 10)
		router := NewRouter(cfg, mockBot, inbound, nil)

		update := &gotgbot.Update{
			UpdateId: 5,
			Message: &gotgbot.Message{
				MessageId: 41,
				Date:      time.Now().Unix(),
				Chat: gotgbot.Chat{
					Id:    -100123456789,
					Type:  "supergroup",
					Title: "Dev Group",
				},
				From: &gotgbot.User{
					Id:        777,
					Username:  "dev1",
					FirstName: "Dave",
				},
				ReplyToMessage: &gotgbot.Message{
					MessageId: 39,
					From: &gotgbot.User{
						Id:       555, // bot ID
						Username: "agyent_bot",
					},
				},
				Text: "Yes please proceed",
			},
		}

		err := router.HandleUpdate(context.Background(), mockBot, update)
		require.NoError(t, err)

		select {
		case msg := <-inbound:
			assert.True(t, msg.IsReplyToBot)
			assert.Equal(t, "39", msg.ReplyToMessageID)
			assert.Equal(t, "Yes please proceed", msg.Text)
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for message")
		}
	})
}

// TC-TG-05: Supergroup Forum Topic Thread Context Extraction
func TestRouter_SupergroupForumTopicThread(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.AllowedGroupIDs = []string{"-100123456789"}

	mockBot := &gotgbot.Bot{
		User: gotgbot.User{
			Id:       555,
			Username: "agyent_bot",
		},
	}

	inbound := make(chan domain.CanonicalMessage, 10)
	router := NewRouter(cfg, mockBot, inbound, nil)

	update := &gotgbot.Update{
		UpdateId: 6,
		Message: &gotgbot.Message{
			MessageId:       50,
			Date:            time.Now().Unix(),
			MessageThreadId: 10042,
			IsTopicMessage:  true,
			Chat: gotgbot.Chat{
				Id:      -100123456789,
				Type:    "supergroup",
				Title:   "Forum Group",
				IsForum: true,
			},
			From: &gotgbot.User{
				Id:        777,
				Username:  "dev1",
				FirstName: "Dave",
			},
			Text: "/status",
		},
	}

	err := router.HandleUpdate(context.Background(), mockBot, update)
	require.NoError(t, err)

	select {
	case msg := <-inbound:
		assert.Equal(t, int64(10042), msg.Chat.ThreadID)
		assert.Equal(t, "telegram:-100123456789:10042", msg.SessionKey())
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TC-TG-06: Slash Command Fast Extraction (/p, /a, /reset)
func TestRouter_SlashCommandExtraction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.AllowedGroupIDs = []string{"-100123456789"}

	mockBot := &gotgbot.Bot{
		User: gotgbot.User{
			Id:       555,
			Username: "agyent_bot",
		},
	}

	tests := []struct {
		rawText      string
		expectedCmd  string
		expectedArgs []string
	}{
		{
			rawText:      "/p webapp",
			expectedCmd:  "/p",
			expectedArgs: []string{"webapp"},
		},
		{
			rawText:      "/p@agyent_bot api_service extra_arg",
			expectedCmd:  "/p",
			expectedArgs: []string{"api_service", "extra_arg"},
		},
		{
			rawText:      "/reset",
			expectedCmd:  "/reset",
			expectedArgs: []string{},
		},
		{
			rawText:      "/force_unlock",
			expectedCmd:  "/force_unlock",
			expectedArgs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.rawText, func(t *testing.T) {
			inbound := make(chan domain.CanonicalMessage, 10)
			router := NewRouter(cfg, mockBot, inbound, nil)

			update := &gotgbot.Update{
				UpdateId: 7,
				Message: &gotgbot.Message{
					MessageId: 60,
					Date:      time.Now().Unix(),
					Chat: gotgbot.Chat{
						Id:   -100123456789,
						Type: "supergroup",
					},
					From: &gotgbot.User{
						Id:        777,
						Username:  "dev1",
						FirstName: "Dave",
					},
					Text: tt.rawText,
				},
			}

			err := router.HandleUpdate(context.Background(), mockBot, update)
			require.NoError(t, err)

			select {
			case msg := <-inbound:
				assert.True(t, msg.IsCommand())
				cmd, args := msg.CommandArgs()
				assert.Equal(t, tt.expectedCmd, cmd)
				if len(tt.expectedArgs) == 0 {
					assert.Empty(t, args)
				} else {
					assert.Equal(t, tt.expectedArgs, args)
				}
			case <-time.After(1 * time.Second):
				t.Fatal("timed out waiting for message")
			}
		})
	}
}

func TestRouter_CallbackQuery(t *testing.T) {
	cfg := config.DefaultConfig()
	adminID := int64(111222)
	cfg.Telegram.AdminUserIDs = []int64{adminID}

	tests := []struct {
		name         string
		userID       int64
		data         string
		expectSent   bool
		expectedText string
	}{
		{
			name:         "Switch Conversation Callback",
			userID:       adminID,
			data:         "c:sw:f8b6c9bf-55ad-405a-90d8-7a96305a1227",
			expectSent:   true,
			expectedText: "/c switch f8b6c9bf-55ad-405a-90d8-7a96305a1227",
		},
		{
			name:         "Pin Conversation Callback",
			userID:       adminID,
			data:         "c:pin:f8b6c9bf-55ad-405a-90d8-7a96305a1227",
			expectSent:   true,
			expectedText: "/pin f8b6c9bf-55ad-405a-90d8-7a96305a1227",
		},
		{
			name:         "Unpin Conversation Callback",
			userID:       adminID,
			data:         "c:unpin:f8b6c9bf-55ad-405a-90d8-7a96305a1227",
			expectSent:   true,
			expectedText: "/unpin f8b6c9bf-55ad-405a-90d8-7a96305a1227",
		},
		{
			name:         "New Conversation Callback",
			userID:       adminID,
			data:         "c:new",
			expectSent:   true,
			expectedText: "/new",
		},
		{
			name:         "Pagination Callback",
			userID:       adminID,
			data:         "c:page:2",
			expectSent:   true,
			expectedText: "/c 2",
		},
		{
			name:         "Archive Conversation Callback",
			userID:       adminID,
			data:         "c:arc:f8b6c9bf-55ad-405a-90d8-7a96305a1227",
			expectSent:   true,
			expectedText: "/c archive f8b6c9bf-55ad-405a-90d8-7a96305a1227",
		},
		{
			name:       "Unauthorized User Callback",
			userID:     999999,
			data:       "c:new",
			expectSent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inbound := make(chan domain.CanonicalMessage, 10)
			router := NewRouter(cfg, nil, inbound, nil)

			update := &gotgbot.Update{
				UpdateId: 99,
				CallbackQuery: &gotgbot.CallbackQuery{
					Id: "cb-1",
					From: gotgbot.User{
						Id:        tt.userID,
						Username:  "tester",
						FirstName: "Test",
					},
					Data: tt.data,
					Message: &gotgbot.Message{
						MessageId: 100,
						Chat: gotgbot.Chat{
							Id:   tt.userID,
							Type: "private",
						},
					},
				},
			}

			err := router.HandleUpdate(context.Background(), nil, update)
			require.NoError(t, err)

			if tt.expectSent {
				select {
				case msg := <-inbound:
					assert.Equal(t, tt.expectedText, msg.Text)
					assert.True(t, msg.IsCommand())
				case <-time.After(500 * time.Millisecond):
					t.Fatal("expected message to be delivered to inbound")
				}
			} else {
				assert.Equal(t, 0, len(inbound))
			}
		})
	}
}
