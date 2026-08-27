package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	var targetKey string
	var dbPathOverride string
	flag.StringVar(&targetKey, "session", "", "Target session key (e.g. telegram:8718145628:8544450322)")
	flag.StringVar(&dbPathOverride, "db", "", "Path to agyent.db (default: ~/.agyent/agyent.db)")
	flag.Parse()

	if targetKey == "" && len(flag.Args()) > 0 {
		targetKey = flag.Args()[0]
	}

	dbPath := dbPathOverride
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Printf("❌ Failed to resolve home directory: %v\n", err)
			os.Exit(1)
		}
		dbPath = filepath.Join(home, ".agyent", "agyent.db")
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Printf("❌ Database not found at: %s\n", dbPath)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		fmt.Printf("❌ Failed to open SQLite database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	fmt.Println("================================================================================")
	fmt.Printf("🔍 AGYENT SESSION DIAGNOSTICS — DB: %s\n", dbPath)
	fmt.Println("================================================================================")

	// 1. Inspect Sessions
	fmt.Println("\n📋 1. SESSIONS OVERVIEW:")
	sessionQuery := "SELECT session_key, active_agent, active_project, global_conversation_id, updated_at FROM sessions ORDER BY updated_at DESC"
	var sessionRows *sql.Rows
	if targetKey != "" {
		sessionQuery = "SELECT session_key, active_agent, active_project, global_conversation_id, updated_at FROM sessions WHERE session_key = ? ORDER BY updated_at DESC"
		sessionRows, err = db.Query(sessionQuery, targetKey)
	} else {
		sessionRows, err = db.Query(sessionQuery)
	}

	var activeConvID string
	var foundTarget bool
	if err != nil {
		fmt.Printf("   ⚠️ Error querying sessions: %v\n", err)
	} else {
		defer sessionRows.Close()
		count := 0
		for sessionRows.Next() {
			count++
			var key, agent, proj, convID string
			var updated int64
			sessionRows.Scan(&key, &agent, &proj, &convID, &updated)
			t := time.UnixMilli(updated).Format("2006-01-02 15:04:05")
			scope := "Global"
			if proj != "" {
				scope = "Project: " + proj
			}
			marker := "  "
			if key == targetKey {
				marker = "👉"
				foundTarget = true
				activeConvID = convID
			} else if targetKey == "" && count == 1 {
				activeConvID = convID
			}
			fmt.Printf("%s [%s] Key: %-35s | Agent: %-10s | %-20s | Conv: %s\n", marker, t, key, agent, scope, convID)
		}
		if count == 0 {
			fmt.Println("   (No sessions found)")
		}
	}

	// 2. Inspect Audit Logs
	fmt.Println("\n📊 2. AUDIT LOGS (RECENT TURNS):")
	auditQuery := "SELECT session_key, agent_name, conversation_id, status, error_message, duration_seconds, total_tokens, input_tokens, output_tokens, thinking_tokens, created_at FROM audit_logs "
	var auditRows *sql.Rows
	if targetKey != "" {
		auditQuery += "WHERE session_key = ? ORDER BY id DESC LIMIT 10"
		auditRows, err = db.Query(auditQuery, targetKey)
	} else {
		auditQuery += "ORDER BY id DESC LIMIT 10"
		auditRows, err = db.Query(auditQuery)
	}

	var totalTokensLastTurn int
	var lastStatus string
	var lastError string

	if err != nil {
		fmt.Printf("   ⚠️ Error querying audit logs: %v\n", err)
	} else {
		defer auditRows.Close()
		idx := 0
		for auditRows.Next() {
			idx++
			var key, agent, convID, status, errMsg string
			var dur float64
			var totalTok, inTok, outTok, thinkTok int
			var createdAt int64
			auditRows.Scan(&key, &agent, &convID, &status, &errMsg, &dur, &totalTok, &inTok, &outTok, &thinkTok, &createdAt)
			t := time.UnixMilli(createdAt).Format("15:04:05")
			statusIcon := "✅"
			if status == "ERROR" {
				statusIcon = "❌"
			}
			if idx == 1 {
				totalTokensLastTurn = totalTok
				lastStatus = status
				lastError = errMsg
			}
			errSnippet := ""
			if errMsg != "" {
				errSnippet = fmt.Sprintf(" | Err: %s", errMsg)
				if len(errSnippet) > 80 {
					errSnippet = errSnippet[:77] + "..."
				}
			}
			fmt.Printf("   %s [%s] %-7s | Dur: %6.1fs | Tokens: %7d (In:%d/Out:%d/Think:%d)%s\n",
				statusIcon, t, status, dur, totalTok, inTok, outTok, thinkTok, errSnippet)
		}
		if idx == 0 {
			fmt.Println("   (No audit logs found)")
		}
	}

	// 3. Inspect Subagents
	fmt.Println("\n🤖 3. SUBAGENT TASKS:")
	subQuery := "SELECT id, parent_session_key, agent_name, title, status, error_message, duration_seconds, created_at FROM subagent_tasks "
	var subRows *sql.Rows
	if targetKey != "" {
		subQuery += "WHERE parent_session_key = ? ORDER BY created_at DESC LIMIT 5"
		subRows, err = db.Query(subQuery, targetKey)
	} else {
		subQuery += "ORDER BY created_at DESC LIMIT 5"
		subRows, err = db.Query(subQuery)
	}

	if err != nil {
		fmt.Printf("   ⚠️ Error querying subagent tasks: %v\n", err)
	} else {
		defer subRows.Close()
		subCount := 0
		for subRows.Next() {
			subCount++
			var id, pKey, agent, title, status, errMsg string
			var dur float64
			var createdAt int64
			subRows.Scan(&id, &pKey, &agent, &title, &status, &errMsg, &dur, &createdAt)
			t := time.UnixMilli(createdAt).Format("15:04:05")
			icon := "⏳"
			if status == "COMPLETED" {
				icon = "✅"
			} else if status == "FAILED" {
				icon = "❌"
			}
			errSnippet := ""
			if errMsg != "" {
				errSnippet = " | Err: " + errMsg
				if len(errSnippet) > 70 {
					errSnippet = errSnippet[:67] + "..."
				}
			}
			fmt.Printf("   %s [%s] Task: %-14s | %-10s | %-12s | Dur: %5.1fs%s\n", icon, t, id, agent, status, dur, errSnippet)
		}
		if subCount == 0 {
			fmt.Println("   (No subagent tasks found)")
		}
	}

	// 4. Automated Health Assessment & Actionable Recommendations
	fmt.Println("\n================================================================================")
	fmt.Println("🩺 4. AUTOMATED HEALTH ASSESSMENT & PLAYBOOK:")
	fmt.Println("================================================================================")

	issuesFound := 0

	if totalTokensLastTurn > 1000000 {
		issuesFound++
		fmt.Printf("⚠️  [HIGH TOKEN CONTEXT]: Conversation context has accumulated %d tokens (>1.0M tokens).\n", totalTokensLastTurn)
		fmt.Println("   • Impact: High LLM inference latency, increased risk of hitting the 300s timeout watchdog.")
		fmt.Println("   • Remediation: In Telegram chat, send `/new` or `/reset` to start a clean conversation session.")
	}

	if lastStatus == "ERROR" {
		issuesFound++
		fmt.Printf("❌  [LAST TURN FAILED]: Error message: %q\n", lastError)
		if strings.Contains(lastError, "timed out") || strings.Contains(lastError, "context deadline exceeded") {
			fmt.Println("   • Cause: Subprocess execution exceeded watchdog limit without milestone activity.")
			fmt.Println("   • Remediation: Run `/new` to drop bloated context, or `/force_unlock` if lock is held.")
		} else if strings.Contains(lastError, "lock") {
			fmt.Println("   • Cause: Session lock is held by another active/hung turn.")
			fmt.Println("   • Remediation: In Telegram chat, send `/force_unlock` to cancel the hung turn.")
		}
	}

	if activeConvID != "" {
		fmt.Printf("📁 [CONVERSATION BRAIN]: ID = %s\n", activeConvID)
		home, _ := os.UserHomeDir()
		brainPath := filepath.Join(home, ".gemini", "antigravity", "brain", activeConvID)
		transcriptPath := filepath.Join(brainPath, ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(transcriptPath); err == nil {
			fmt.Printf("   • Transcript Log: %s\n", transcriptPath)
		}
	}

	if issuesFound == 0 {
		fmt.Println("✅ All session metrics and recent turns appear healthy!")
	}
	fmt.Println("================================================================================")
	_ = foundTarget
}
