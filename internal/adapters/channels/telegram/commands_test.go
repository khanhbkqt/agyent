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
	"agyent/internal/core/eventbus"
)

func TestRegisterCommands_Success(t *testing.T) {
	mockServer := NewMockTelegramServer("token_cmd_01")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_cmd_01"

	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = adapter.RegisterCommands(ctx, nil)
	require.NoError(t, err)

	cmds, err := adapter.GetCommands(ctx)
	require.NoError(t, err)
	assert.Len(t, cmds, len(DefaultBotCommands))

	// Verify specific commands exist
	cmdNames := make(map[string]string)
	for _, c := range cmds {
		cmdNames[c.Command] = c.Description
	}

	assert.Contains(t, cmdNames, "help")
	assert.Contains(t, cmdNames, "status")
	assert.Contains(t, cmdNames, "context")
	assert.Contains(t, cmdNames, "skills")
	assert.Contains(t, cmdNames, "plugins")
	assert.Contains(t, cmdNames, "stream")
	assert.Contains(t, cmdNames, "agents")
	assert.Contains(t, cmdNames, "use")
	assert.Contains(t, cmdNames, "projects")
	assert.Contains(t, cmdNames, "reset")
	assert.Contains(t, cmdNames, "bootstrap")
	assert.Contains(t, cmdNames, "force_unlock")
}

func TestRegisterCommands_Custom(t *testing.T) {
	mockServer := NewMockTelegramServer("token_cmd_02")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))

	customCmds := []gotgbot.BotCommand{
		{Command: "ping", Description: "Ping bot"},
		{Command: "echo", Description: "Echo text"},
	}

	ctx := context.Background()
	err = adapter.RegisterCommands(ctx, customCmds)
	require.NoError(t, err)

	cmds, err := adapter.GetCommands(ctx)
	require.NoError(t, err)
	assert.Equal(t, customCmds, cmds)
}

func TestRegisterCommands_NilBot(t *testing.T) {
	cfg := config.DefaultConfig()
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus)

	ctx := context.Background()
	err := adapter.RegisterCommands(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "telegram bot client is not initialized")

	_, err = adapter.GetCommands(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "telegram bot client is not initialized")

	err = adapter.DeleteCommands(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "telegram bot client is not initialized")
}

func TestDeleteCommands_Success(t *testing.T) {
	mockServer := NewMockTelegramServer("token_cmd_03")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	ctx := context.Background()

	// 1. Register commands first
	err = adapter.RegisterCommands(ctx, DefaultBotCommands)
	require.NoError(t, err)

	cmds, err := adapter.GetCommands(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, cmds)

	// 2. Delete commands
	err = adapter.DeleteCommands(ctx)
	require.NoError(t, err)

	cmds, err = adapter.GetCommands(ctx)
	require.NoError(t, err)
	assert.Empty(t, cmds)
}

func TestAdapter_AutoRegistersCommandsOnStart(t *testing.T) {
	mockServer := NewMockTelegramServer("token_cmd_04")
	defer mockServer.Close()

	bot, err := mockServer.NewBot()
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.Telegram.BotToken = "token_cmd_04"
	cfg.Telegram.Mode = "polling"
	cfg.Telegram.AdminUserIDs = []int64{12345}

	bus := eventbus.NewEventBus(10, 1)
	defer bus.Close()

	adapter := NewAdapter(cfg, bus, WithBot(bot))
	inbound := make(chan domain.CanonicalMessage, 10)

	ctx := context.Background()
	err = adapter.Start(ctx, inbound)
	require.NoError(t, err)

	// Verify commands were registered automatically on Start
	cmds, err := adapter.GetCommands(ctx)
	require.NoError(t, err)
	assert.Len(t, cmds, len(DefaultBotCommands))

	_ = adapter.Stop()
}
