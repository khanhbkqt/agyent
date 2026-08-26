package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"agyent/internal/core/domain"
)

// Client coordinates IPC communication from the agyent-hook binary to the running Gateway Daemon.
type Client struct {
	addr string
}

// NewClient constructs a new IPC client.
func NewClient(addr string) *Client {
	if addr == "" {
		addr = DefaultIPCAddress
	}
	return &Client{addr: addr}
}

// SendHookRequest sends a domain.HookRequest and reads the response within the given timeout.
func (c *Client) SendHookRequest(req domain.HookRequest, timeout time.Duration) (domain.HookResponse, error) {
	if timeout <= 0 {
		timeout = 65 * time.Second
	}

	conn, err := net.DialTimeout("tcp", c.addr, 2*time.Second)
	if err != nil {
		// Fail-Safe Default-Deny fallback
		return domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("🛡️ [Security Gateway]: Gateway daemon is offline or unreachable (%v). Fail-safe Default-Deny engaged.", err),
		}, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Failed to encode hook request: %v", err),
		}, err
	}

	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		return domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Failed to transmit hook request: %v", err),
		}, err
	}

	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	if !scanner.Scan() {
		return domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   "No response received from Security Gateway IPC Daemon",
		}, fmt.Errorf("empty response from daemon")
	}

	var resp domain.HookResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Malformed decision payload from daemon: %v", err),
		}, err
	}

	return resp, nil
}
