package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/spf13/cobra"

	"agyent/internal/adapters/channels/telegram"
	"agyent/internal/config"
)

var (
	deleteCommandsFlag bool

	registerCommandsCmd = &cobra.Command{
		Use:     "register-commands",
		Aliases: []string{"commands", "set-commands", "sync-commands"},
		Short:   "Register slash commands with Telegram Bot API",
		Long:    `Synchronizes and registers all supported slash commands with the Telegram Bot API so they appear in the bot command menu and autocomplete UI.`,
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error loading configuration from %s: %v\n", cfgFile, err)
				os.Exit(1)
			}

			normalizedBots := cfg.Telegram.GetNormalizedBots()
			if len(normalizedBots) == 0 {
				fmt.Fprintf(os.Stderr, "❌ Telegram bot token or bots configuration is missing.\n")
				os.Exit(1)
			}

			var bots []*gotgbot.Bot
			for _, bCfg := range normalizedBots {
				b, err := gotgbot.NewBot(bCfg.BotToken, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "⚠️ Failed to initialize bot %q: %v\n", bCfg.Name, err)
					continue
				}
				bots = append(bots, b)
			}
			if len(bots) == 0 {
				fmt.Fprintf(os.Stderr, "❌ Failed to initialize any Telegram bot from configuration.\n")
				os.Exit(1)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			adapter := telegram.NewAdapter(cfg, nil, telegram.WithBots(bots...))

			if deleteCommandsFlag {
				fmt.Println("🗑️ Removing all bot commands from Telegram...")
				if err := adapter.DeleteCommands(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "❌ Failed to delete commands: %v\n", err)
					os.Exit(1)
				}
				fmt.Println("✅ Successfully deleted all bot commands.")
				return
			}

			fmt.Println("📡 Registering slash commands with Telegram Bot API...")
			if err := adapter.RegisterCommands(ctx, telegram.DefaultBotCommands); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to register commands: %v\n", err)
				os.Exit(1)
			}

			registered, err := adapter.GetCommands(ctx)
			if err != nil {
				fmt.Printf("⚠️ Commands registered, but failed to fetch confirmation: %v\n", err)
				return
			}

			fmt.Println("✅ Successfully registered the following commands:")
			fmt.Println("------------------------------------------------------------")
			for _, c := range registered {
				fmt.Printf("• /%-14s — %s\n", c.Command, c.Description)
			}
			fmt.Println("------------------------------------------------------------")
			fmt.Printf("Total commands registered: %d\n", len(registered))
		},
	}
)

func init() {
	registerCommandsCmd.Flags().BoolVar(&deleteCommandsFlag, "delete", false, "Delete all registered commands instead of setting them")
	rootCmd.AddCommand(registerCommandsCmd)
}
