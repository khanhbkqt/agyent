package doctor

import (
	"context"
	"fmt"
	"strings"

	"agyent/internal/wizard"
)

// CheckTelegram validates Telegram Bot tokens and performs live API authentication checks.
func (d *DoctorRunner) CheckTelegram(ctx context.Context) []CheckResult {
	var results []CheckResult
	bots := d.cfg.Telegram.GetNormalizedBots()
	if len(bots) == 0 {
		status := StatusFail
		msg := "No Telegram bot token found in configuration"
		if len(d.cfg.Zalo.GetNormalizedBots()) > 0 {
			status = StatusInfo
			msg = "Telegram bot not configured (Zalo channel active)"
		}
		results = append(results, CheckResult{
			Name:        "Telegram Bot Credentials",
			Category:    CategoryTelegram,
			Status:      status,
			Message:     msg,
			Remediation: "Set 'telegram.bot_token' or 'telegram.bots' in ~/.agyent/config.yaml if Telegram is desired.",
		})
		return results
	}

	for _, b := range bots {
		botName := b.Name
		if botName == "" {
			botName = "default"
		}

		// 1. Validate Token Format
		if err := wizard.ValidateTokenFormat(b.BotToken); err != nil {
			results = append(results, CheckResult{
				Name:        fmt.Sprintf("Bot Token Format [%s]", botName),
				Category:    CategoryTelegram,
				Status:      StatusFail,
				Message:     fmt.Sprintf("Invalid token syntax: %v", err),
				Remediation: "Ensure token follows the standard format: '<bot_id>:<token_secret>'.",
			})
			continue
		}

		results = append(results, CheckResult{
			Name:     fmt.Sprintf("Bot Token Format [%s]", botName),
			Category: CategoryTelegram,
			Status:   StatusPass,
			Message:  "Token format syntax valid",
		})

		// 2. Live getMe API Verification
		resp, err := wizard.VerifyTelegramToken(ctx, "", b.BotToken)
		if err != nil {
			status := StatusFail
			if strings.Contains(strings.ToLower(err.Error()), "timed out") || strings.Contains(strings.ToLower(err.Error()), "network") {
				status = StatusWarn
			}
			results = append(results, CheckResult{
				Name:        fmt.Sprintf("Telegram Bot API Auth [%s]", botName),
				Category:    CategoryTelegram,
				Status:      status,
				Message:     fmt.Sprintf("Telegram authentication check failed: %v", err),
				Remediation: "Verify bot token with @BotFather and check host internet connectivity to api.telegram.org.",
			})
		} else {
			results = append(results, CheckResult{
				Name:     fmt.Sprintf("Telegram Bot API Auth [%s]", botName),
				Category: CategoryTelegram,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Authenticated successfully as @%s (Bot ID: %d, Name: %s)", resp.Result.Username, resp.Result.ID, resp.Result.FirstName),
			})
		}
	}

	return results
}
