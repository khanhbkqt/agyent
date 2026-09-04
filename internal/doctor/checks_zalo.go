package doctor

import (
	"context"
	"fmt"
	"strings"

	"agyent/internal/adapters/channels/zalo"
)

// CheckZalo validates Zalo Bot tokens and performs live API authentication checks.
func (d *DoctorRunner) CheckZalo(ctx context.Context) []CheckResult {
	var results []CheckResult
	bots := d.cfg.Zalo.GetNormalizedBots()
	if len(bots) == 0 {
		status := StatusInfo
		msg := "Zalo bot not configured"
		if len(d.cfg.Telegram.GetNormalizedBots()) == 0 {
			status = StatusFail
			msg = "No Zalo bot token found in configuration"
		}
		results = append(results, CheckResult{
			Name:        "Zalo Bot Credentials",
			Category:    CategoryZalo,
			Status:      status,
			Message:     msg,
			Remediation: "Set 'zalo.bot_token' or 'zalo.bots' in ~/.agyent/config.yaml.",
		})
		return results
	}

	for _, b := range bots {
		botName := b.Name
		if botName == "" {
			botName = "default"
		}

		token := strings.TrimSpace(b.BotToken)
		if token == "" {
			results = append(results, CheckResult{
				Name:        fmt.Sprintf("Zalo Bot Token [%s]", botName),
				Category:    CategoryZalo,
				Status:      StatusFail,
				Message:     "Zalo bot token is empty",
				Remediation: "Provide a valid token from Zalo Bot Platform.",
			})
			continue
		}

		results = append(results, CheckResult{
			Name:     fmt.Sprintf("Zalo Bot Token [%s]", botName),
			Category: CategoryZalo,
			Status:   StatusPass,
			Message:  "Token format valid",
		})

		// Live API Verification
		client := zalo.NewClient(token, d.cfg.Zalo.APIURL)
		user, err := client.GetMe(ctx)
		if err != nil {
			status := StatusFail
			if strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "connection") {
				status = StatusWarn
			}
			results = append(results, CheckResult{
				Name:        fmt.Sprintf("Zalo Bot API Auth [%s]", botName),
				Category:    CategoryZalo,
				Status:      status,
				Message:     fmt.Sprintf("Failed to authenticate with Zalo Bot Platform: %v", err),
				Remediation: "Check your internet connection and verify ZALO_BOT_TOKEN.",
			})
		} else {
			displayName := user.Name
			if displayName == "" {
				displayName = user.ID
			}
			results = append(results, CheckResult{
				Name:     fmt.Sprintf("Zalo Bot API Auth [%s]", botName),
				Category: CategoryZalo,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Authenticated successfully as %s (ID: %s)", displayName, user.ID),
			})
		}
	}

	if d.cfg.Zalo.GroupID == "" && len(d.cfg.Zalo.AllowedGroupIDs) == 0 {
		results = append(results, CheckResult{
			Name:        "Zalo Target Group",
			Category:    CategoryZalo,
			Status:      StatusWarn,
			Message:     "No default target group_id or allowed_group_ids specified",
			Remediation: "Set 'zalo.group_id' in ~/.agyent/config.yaml to receive group notifications.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "Zalo Target Group",
			Category: CategoryZalo,
			Status:   StatusPass,
			Message:  "Target group configuration verified",
		})
	}

	// Webhook Configuration check if mode is webhook
	if strings.ToLower(d.cfg.Zalo.Mode) == "webhook" {
		if strings.TrimSpace(d.cfg.Zalo.WebhookURL) == "" {
			results = append(results, CheckResult{
				Name:        "Zalo Webhook URL",
				Category:    CategoryZalo,
				Status:      StatusFail,
				Message:     "Zalo mode is set to 'webhook' but 'webhook_url' is empty",
				Remediation: "Provide a valid public HTTPS URL for 'zalo.webhook_url' or switch mode to 'polling'.",
			})
		} else if !strings.HasPrefix(strings.ToLower(d.cfg.Zalo.WebhookURL), "https://") && !strings.HasPrefix(strings.ToLower(d.cfg.Zalo.WebhookURL), "http://") {
			results = append(results, CheckResult{
				Name:        "Zalo Webhook URL",
				Category:    CategoryZalo,
				Status:      StatusFail,
				Message:     "Zalo webhook_url must start with https:// (or http:// for local dev)",
				Remediation: "Ensure 'zalo.webhook_url' is a properly formatted URL.",
			})
		} else {
			results = append(results, CheckResult{
				Name:     "Zalo Webhook URL",
				Category: CategoryZalo,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Configured webhook URL: %s", d.cfg.Zalo.WebhookURL),
			})
		}
	}

	return results
}
