package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"agyent/internal/core/domain"
)

// Client coordinates IPC communication from the agyent-hook binary to the running Gateway Daemon.
type Client struct {
	addr        string
	secretToken string
	turnID      string
}

// NewClient constructs a new IPC client.
func NewClient(addr string) *Client {
	if addr == "" {
		addr = DefaultIPCAddress
	}
	return &Client{addr: addr}
}

// SetSecretToken sets the daemon IPC secret token on the client.
func (c *Client) SetSecretToken(token string) {
	c.secretToken = token
}

// SetTurnID sets the execution turn ID on the client.
func (c *Client) SetTurnID(turnID string) {
	c.turnID = turnID
}

// SendHookRequest sends a HookRequest and reads the response within the given timeout.
func (c *Client) SendHookRequest(req HookRequest, timeout time.Duration) (HookResponse, error) {
	if timeout <= 0 {
		timeout = 65 * time.Second
	}

	if req.TurnID == "" {
		req.TurnID = c.turnID
		if req.TurnID == "" {
			req.TurnID = os.Getenv("AGYENT_TURN_ID")
		}
	}

	conn, err := net.DialTimeout("tcp", c.addr, 2*time.Second)
	if err != nil {
		// Fail-Safe Default-Deny fallback
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("🛡️ [Security Gateway]: Gateway daemon is offline or unreachable (%v). Fail-safe Default-Deny engaged.", err),
		}, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Failed to encode hook request: %v", err),
		}, err
	}

	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Failed to transmit hook request: %v", err),
		}, err
	}

	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	if !scanner.Scan() {
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   "No response received from Security Gateway IPC Daemon",
		}, fmt.Errorf("empty response from daemon")
	}

	var resp HookResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Malformed decision payload from daemon: %v", err),
		}, err
	}

	return resp, nil
}

// ActionResponse represents the response envelope for an IPC action.
type ActionResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// SendAction sends a custom action request to the gateway daemon.
func (c *Client) SendAction(action string, params any, timeout time.Duration) (ActionResponse, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	conn, err := net.DialTimeout("tcp", c.addr, 2*time.Second)
	if err != nil {
		return ActionResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to connect to gateway IPC daemon (%v)", err),
		}, err
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	payload := map[string]interface{}{
		"action": action,
		"params": params,
	}
	token := c.secretToken
	if token == "" {
		token = os.Getenv("AGYENT_IPC_TOKEN")
	}
	if token != "" {
		payload["token"] = token
	}
	turnID := c.turnID
	if turnID == "" {
		turnID = os.Getenv("AGYENT_TURN_ID")
	}
	if turnID != "" {
		payload["turn_id"] = turnID
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return ActionResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to serialize action payload: %v", err),
		}, err
	}

	if _, err := conn.Write(append(payloadBytes, '\n')); err != nil {
		return ActionResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to write to daemon: %v", err),
		}, err
	}

	scanner := bufio.NewScanner(conn)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	if !scanner.Scan() {
		return ActionResponse{
			Success: false,
			Error:   "No response received from daemon",
		}, fmt.Errorf("empty response from daemon")
	}

	var resp ActionResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return ActionResponse{
			Success: false,
			Error:   fmt.Sprintf("Malformed action response from daemon: %v", err),
		}, err
	}

	return resp, nil
}
