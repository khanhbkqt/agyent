package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHookBridge_PassThroughOutsideAgyentTurn(t *testing.T) {
	// 1. Pre hook outside active agyent turn -> allow
	t.Run("Pre_Allow", func(t *testing.T) {
		payload := `{"toolCall":{"name":"run_command","args":{"CommandLine":"ls"}},"stepIdx":1}`
		inBuf := strings.NewReader(payload)
		outBuf := new(bytes.Buffer)

		oldStdin, oldStdout := os.Stdin, os.Stdout
		rIn, wIn, _ := os.Pipe()
		rOut, wOut, _ := os.Pipe()
		os.Stdin, os.Stdout = rIn, wOut

		_, _ = wIn.Write([]byte(payload))
		_ = wIn.Close()

		t.Setenv("AGYENT_TURN_ID", "")
		t.Setenv("AGYENT_SECURITY_IPC_TOKEN", "")

		done := make(chan struct{})
		go func() {
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rOut)
			outBuf.WriteString(buf.String())
			close(done)
		}()

		hookBridgeCmd.Run(hookBridgeCmd, []string{"pre"})

		_ = wOut.Close()
		<-done
		os.Stdin, os.Stdout = oldStdin, oldStdout
		_ = inBuf

		var resp map[string]interface{}
		require.NoError(t, json.Unmarshal(outBuf.Bytes(), &resp))
		assert.Equal(t, "allow", resp["decision"])
	})

	// 2. Post hook outside active agyent turn -> {}
	t.Run("Post_EmptyObject", func(t *testing.T) {
		payload := `{"toolCall":{"name":"run_command","args":{"output":"hello"}},"stepIdx":1}`
		outBuf := new(bytes.Buffer)

		oldStdin, oldStdout := os.Stdin, os.Stdout
		rIn, wIn, _ := os.Pipe()
		rOut, wOut, _ := os.Pipe()
		os.Stdin, os.Stdout = rIn, wOut

		_, _ = wIn.Write([]byte(payload))
		_ = wIn.Close()

		t.Setenv("AGYENT_TURN_ID", "")
		t.Setenv("AGYENT_SECURITY_IPC_TOKEN", "")

		done := make(chan struct{})
		go func() {
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(rOut)
			outBuf.WriteString(buf.String())
			close(done)
		}()

		hookBridgeCmd.Run(hookBridgeCmd, []string{"post"})

		_ = wOut.Close()
		<-done
		os.Stdin, os.Stdout = oldStdin, oldStdout

		assert.Equal(t, "{}\n", outBuf.String())
	})
}
