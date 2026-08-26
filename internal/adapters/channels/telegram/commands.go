package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/PaulSonOfLars/gotgbot/v2"
)

// DefaultBotCommands defines the canonical list of slash commands registered with the Telegram Bot API.
var DefaultBotCommands = []gotgbot.BotCommand{
	{Command: "help", Description: "Show commands guide and help details"},
	{Command: "status", Description: "View system uptime, active agent & stats"},
	{Command: "model", Description: "Select or inspect active AI model"},
	{Command: "effort", Description: "Set reasoning effort (low/med/high/none)"},
	{Command: "tokens", Description: "View token usage & KV-cache metrics"},
	{Command: "context", Description: "Inspect active context directives & MCPs"},
	{Command: "skills", Description: "List Progressive Disclosure skills"},
	{Command: "plugins", Description: "Manage capability plugins"},
	{Command: "stream", Description: "Toggle real-time streaming mode (on/off)"},
	{Command: "agents", Description: "List and manage active agent profiles"},
	{Command: "use", Description: "Switch active agent profile"},
	{Command: "projects", Description: "List and manage project workspaces"},
	{Command: "conversations", Description: "Manage multi-conversation contexts"},
	{Command: "new", Description: "Start a fresh new conversation"},
	{Command: "reset", Description: "Clear conversation context for active scope"},
	{Command: "bootstrap", Description: "Trigger Genesis Bootstrap protocol"},
	{Command: "force_unlock", Description: "Force unlock session mutex & reset state"},
}

// RegisterCommands registers a list of bot commands with the Telegram API across all scopes
// (Default, AllPrivateChats, AllGroupChats) and enables the Chat Menu Button.
// If commands is nil or empty, DefaultBotCommands is used.
func (a *Adapter) RegisterCommands(ctx context.Context, commands []gotgbot.BotCommand) error {
	a.mu.RLock()
	bot := a.bot
	a.mu.RUnlock()

	if bot == nil {
		return errors.New("telegram bot client is not initialized")
	}

	if len(commands) == 0 {
		commands = DefaultBotCommands
	}

	// 1. Register for Default scope
	_, err := bot.SetMyCommandsWithContext(ctx, commands, &gotgbot.SetMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeDefault{},
	})
	if err != nil {
		return fmt.Errorf("failed to register default scope commands: %w", err)
	}

	// 2. Register for All Private Chats scope
	_, _ = bot.SetMyCommandsWithContext(ctx, commands, &gotgbot.SetMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeAllPrivateChats{},
	})

	// 3. Register for All Group Chats scope
	_, _ = bot.SetMyCommandsWithContext(ctx, commands, &gotgbot.SetMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeAllGroupChats{},
	})

	// 4. Configure Chat Menu Button to display commands list
	_, _ = bot.SetChatMenuButtonWithContext(ctx, &gotgbot.SetChatMenuButtonOpts{
		MenuButton: gotgbot.MenuButtonCommands{},
	})

	return nil
}

// GetCommands retrieves the currently registered bot commands from the Telegram API.
func (a *Adapter) GetCommands(ctx context.Context) ([]gotgbot.BotCommand, error) {
	a.mu.RLock()
	bot := a.bot
	a.mu.RUnlock()

	if bot == nil {
		return nil, errors.New("telegram bot client is not initialized")
	}

	cmds, err := bot.GetMyCommandsWithContext(ctx, &gotgbot.GetMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeDefault{},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve telegram commands: %w", err)
	}

	return cmds, nil
}

// DeleteCommands removes all registered bot commands and resets the chat menu button.
func (a *Adapter) DeleteCommands(ctx context.Context) error {
	a.mu.RLock()
	bot := a.bot
	a.mu.RUnlock()

	if bot == nil {
		return errors.New("telegram bot client is not initialized")
	}

	// 1. Delete Default scope commands
	_, err := bot.DeleteMyCommandsWithContext(ctx, &gotgbot.DeleteMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeDefault{},
	})
	if err != nil {
		return fmt.Errorf("failed to delete default scope commands: %w", err)
	}

	// 2. Delete Private Chats scope commands
	_, _ = bot.DeleteMyCommandsWithContext(ctx, &gotgbot.DeleteMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeAllPrivateChats{},
	})

	// 3. Delete Group Chats scope commands
	_, _ = bot.DeleteMyCommandsWithContext(ctx, &gotgbot.DeleteMyCommandsOpts{
		Scope: gotgbot.BotCommandScopeAllGroupChats{},
	})

	// 4. Reset Chat Menu Button to Default
	_, _ = bot.SetChatMenuButtonWithContext(ctx, &gotgbot.SetChatMenuButtonOpts{
		MenuButton: gotgbot.MenuButtonDefault{},
	})

	return nil
}
