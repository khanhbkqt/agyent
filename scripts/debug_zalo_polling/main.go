package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"agyent/internal/adapters/channels/zalo"
)

const defaultAPIURL = "https://bot-api.zaloplatforms.com"

func main() {
	var token string
	var apiURL string
	flag.StringVar(&token, "token", os.Getenv("ZALO_BOT_TOKEN"), "Zalo Bot Token (or set ZALO_BOT_TOKEN env var)")
	flag.StringVar(&apiURL, "api-url", defaultAPIURL, "Zalo Bot API Base URL")
	flag.Parse()

	token = strings.TrimSpace(token)
	if token == "" {
		fmt.Println("❌ Error: bot token is empty. Provide via --token flag or ZALO_BOT_TOKEN environment variable.")
		os.Exit(1)
	}

	fmt.Println("================================================================================")
	fmt.Println("🚀 ZALO BOT POLLING & PAYLOAD INSPECTOR")
	fmt.Println("================================================================================")
	fmt.Printf("🌐 API Base URL: %s\n", apiURL)
	masked := token
	if len(token) > 12 {
		masked = token[:6] + "..." + token[len(token)-4:]
	}
	fmt.Printf("🔑 Bot Token:   %s\n", masked)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigChan
		fmt.Println("\n🛑 Interrupted by user. Shutting down polling...")
		cancel()
	}()

	client := zalo.NewClient(token, apiURL)

	// Step 1: Ensure Webhook is cleared so polling works without conflict
	fmt.Print("🔄 Clearing existing webhook (deleteWebhook)... ")
	if err := client.DeleteWebhook(ctx); err != nil {
		fmt.Printf("⚠️ %v (continuing)\n", err)
	} else {
		fmt.Println("✅ done.")
	}

	// Step 2: Test getMe
	fmt.Print("🤖 Probing bot profile (getMe)... ")
	user, err := client.GetMe(ctx)
	if err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ OK!\n   • Bot ID:       %s\n   • Display Name: %s\n   • Username:     %s\n",
		user.ID, user.GetEffectiveName(), user.Username)

	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("🟢 Polling started! Send an image, text, or file in Zalo to see live payload.")
	fmt.Println("   Waiting for updates... (Press Ctrl+C to stop)")
	fmt.Println("--------------------------------------------------------------------------------")

	var offset int64 = 0
	pollCount := 0

	for {
		select {
		case <-ctx.Done():
			fmt.Println("👋 Polling stopped cleanly.")
			return
		default:
		}

		pollCount++

		// Send raw getUpdates request to capture the exact raw JSON body
		rawResp, rawUpdates, err := fetchRawUpdates(ctx, apiURL, token, offset, 50, 10)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if zalo.IsTimeoutError(err) {
				continue
			}
			fmt.Printf("⚠️ Poll error: %v (retrying in 2s)\n", err)
			time.Sleep(2 * time.Second)
			continue
		}

		if len(rawUpdates) == 0 {
			continue
		}

		nowStr := time.Now().Format("2006-01-02 15:04:05.000")
		fmt.Printf("\n\n================================================================================\n")
		fmt.Printf("📩 [%s] RECEIVED %d UPDATE(S)\n", nowStr, len(rawUpdates))
		fmt.Printf("================================================================================\n")

		// Pretty-print the raw JSON response
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, rawResp, "", "  "); err == nil {
			fmt.Println("📦 RAW API RESPONSE BODY:")
			fmt.Println(prettyJSON.String())
		} else {
			fmt.Println("📦 RAW API RESPONSE BODY:")
			fmt.Println(string(rawResp))
		}
		fmt.Println("--------------------------------------------------------------------------------")

		for idx, u := range rawUpdates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}

			fmt.Printf("\n🔍 [UPDATE #%d] UpdateID: %d | Event: %s\n", idx+1, u.UpdateID, u.Event)
			if u.Message != nil {
				msg := u.Message
				fmt.Printf("   • Message ID:  %s\n", msg.MessageID)
				fmt.Printf("   • From User:   ID=%s | Name=%q | Username=%q | IsBot=%t\n",
					msg.From.ID, msg.From.GetEffectiveName(), msg.From.Username, msg.From.IsBot)
				fmt.Printf("   • Chat:        ID=%s | Type=%s | Title=%q | ThreadID=%d\n",
					msg.Chat.ID, msg.Chat.EffectiveType(), msg.Chat.Title, msg.Chat.ThreadID)
				fmt.Printf("   • Text:        %q\n", msg.Text)
				fmt.Printf("   • Caption:     %q\n", msg.Caption)
				fmt.Printf("   • Description: %q\n", msg.Description)

				attachments := msg.CollectAttachments()
				fmt.Printf("   • Total Attachments: %d\n", len(attachments))
				for aIdx, att := range attachments {
					fmt.Printf("     [%d] Type:        %s\n", aIdx+1, att.Type)
					fmt.Printf("         URL:         %s\n", att.GetEffectiveURL())
					fmt.Printf("         File ID:     %s\n", att.GetEffectiveFileID())
					fmt.Printf("         File Name:   %s\n", att.GetEffectiveFileName())
					fmt.Printf("         File Size:   %d bytes\n", att.GetEffectiveFileSize())
					fmt.Printf("         Caption:     %s\n", att.GetEffectiveCaption())
					if att.Payload != nil {
						pJSON, _ := json.Marshal(att.Payload)
						fmt.Printf("         Payload obj: %s\n", string(pJSON))
					}

					// Quick URL check
					mediaURL := att.GetEffectiveURL()
					if mediaURL != "" && (strings.HasPrefix(mediaURL, "http://") || strings.HasPrefix(mediaURL, "https://")) {
						probeMediaURL(mediaURL)
					}
				}
			} else {
				fmt.Println("   ⚠️ Update message field is nil (non-message event)")
			}
		}
		fmt.Println("================================================================================")
	}
}

func fetchRawUpdates(ctx context.Context, apiURL, token string, offset int64, limit, timeoutSec int) ([]byte, []zalo.ZaloUpdate, error) {
	url := fmt.Sprintf("%s/bot%s/getUpdates", strings.TrimRight(apiURL, "/"), token)
	payload := map[string]interface{}{
		"offset":  offset,
		"limit":   limit,
		"timeout": timeoutSec,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agyent-zalo-debugger/1.0")

	httpClient := &http.Client{Timeout: time.Duration(timeoutSec+15) * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	var envelope zalo.APIResponse
	if err := json.Unmarshal(respBytes, &envelope); err != nil {
		return respBytes, nil, fmt.Errorf("failed to decode JSON response: %w", err)
	}
	envelope.Normalize()

	if !envelope.OK && envelope.ErrorCode != 0 {
		return respBytes, nil, &zalo.APIError{
			StatusCode:  resp.StatusCode,
			ErrorCode:   envelope.ErrorCode,
			Description: envelope.Description,
			RawBody:     string(respBytes),
		}
	}

	var updates []zalo.ZaloUpdate
	trimmed := bytes.TrimSpace(envelope.Result)
	if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if trimmed[0] == '{' {
			var u zalo.ZaloUpdate
			if err := json.Unmarshal(trimmed, &u); err == nil && (u.UpdateID != 0 || u.Message != nil) {
				updates = []zalo.ZaloUpdate{u}
			}
		} else if trimmed[0] == '[' {
			_ = json.Unmarshal(trimmed, &updates)
		}
	}

	return respBytes, updates, nil
}

func probeMediaURL(u string) {
	fmt.Printf("         🌐 Probing URL reachability: %s\n", u)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("HEAD", u, nil)
	if err != nil {
		fmt.Printf("            ⚠️ Failed to create HEAD request: %v\n", err)
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("            ⚠️ HEAD request error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	fmt.Printf("            ✅ HTTP %d | Content-Type: %s | Content-Length: %s\n",
		resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Length"))
}
