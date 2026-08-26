package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"agyent/internal/adapters/security/ipc"
	"agyent/internal/core/domain"
	"github.com/spf13/cobra"
)

var hookBridgeCmd = &cobra.Command{
	Use:   "hook-bridge [pre|post]",
	Short: "Bridge Antigravity lifecycle hook events to the Agyent Security Gateway",
	Run: func(cmd *cobra.Command, args []string) {
		hookType := "pre"
		if len(args) > 0 && args[0] == "post" {
			hookType = "post"
		}

		var req domain.HookRequest
		if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
			// If stdin cannot be decoded, output fail-safe response
			resp, _ := json.Marshal(domain.HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   fmt.Sprintf("Failed to decode STDIN hook payload: %v", err),
			})
			fmt.Println(string(resp))
			return
		}

		req.HookType = hookType

		ipcAddr := os.Getenv("AGYENT_SECURITY_IPC_ADDR")
		if ipcAddr == "" {
			ipcAddr = ipc.DefaultIPCAddress
		}
		client := ipc.NewClient(ipcAddr)
		resp, err := client.SendHookRequest(req, 65*time.Second)
		if err != nil {
			// Fail-safe Default-Deny if daemon is unreachable
			resp = domain.HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   fmt.Sprintf("🛡️ [Security Gateway]: Gateway daemon offline (%v). Fail-safe Default-Deny engaged.", err),
			}
		}

		respBytes, _ := json.Marshal(resp)
		fmt.Println(string(respBytes))
	},
}

func init() {
	rootCmd.AddCommand(hookBridgeCmd)
}
