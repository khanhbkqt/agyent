package zalo_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZaloHITL_ApprovalFlow_AllowOnce(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "group_test",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_test_001",
		SessionKey:  "zalo:group_test",
		ToolName:    "run_command",
		CommandLine: "rm -rf ./tmp",
		RiskLevel:   "High",
		Reason:      "Potentially destructive filesystem operation",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	// Wait briefly for coordinator to register request
	time.Sleep(50 * time.Millisecond)

	// Admin executes /approve req_test_001
	err := coordinator.HandleCommandApproval(context.Background(), "req_test_001", "admin_999", "allow_once")
	require.NoError(t, err)

	select {
	case dec := <-done:
		assert.Equal(t, "req_test_001", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, domain.ActionAllowOnce, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval decision")
	}
}

func TestZaloHITL_ApprovalFlow_AllowSession(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "group_test",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_test_002",
		SessionKey:  "zalo:group_test",
		ToolName:    "run_command",
		CommandLine: "npm test",
		RiskLevel:   "Medium",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Admin executes /approve req_test_002 session
	err := coordinator.HandleCommandApproval(context.Background(), "req_test_002", "admin_999", "allow_session")
	require.NoError(t, err)

	select {
	case dec := <-done:
		assert.Equal(t, "req_test_002", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, domain.ActionAllowSession, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval decision")
	}
}

func TestZaloHITL_ApprovalFlow_AllowAllSession(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "group_test",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	for _, actionInput := range []string{"all", "all_session", "allow_all_session", "allow_all"} {
		reqID := "req_test_all_" + actionInput
		req := domain.ApprovalRequest{
			RequestID:   reqID,
			SessionKey:  "zalo:group_test",
			ToolName:    "run_command",
			CommandLine: "pip install requests",
			RiskLevel:   "Medium",
			ExpiresAt:   time.Now().Add(5 * time.Second),
		}

		done := make(chan domain.ApprovalDecision, 1)
		go func() {
			dec, err := coordinator.RequestApproval(context.Background(), req)
			require.NoError(t, err)
			done <- dec
		}()

		time.Sleep(50 * time.Millisecond)

		err := coordinator.HandleCommandApproval(context.Background(), reqID, "admin_999", actionInput)
		require.NoError(t, err)

		select {
		case dec := <-done:
			assert.Equal(t, reqID, dec.RequestID)
			assert.True(t, dec.Approved)
			assert.Equal(t, domain.ActionAllowAllSession, dec.Action)
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for approval decision on action %s", actionInput)
		}
	}
}

func TestZaloHITL_NonAdminDenied(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:  "req_test_003",
		SessionKey: "zalo:group_test",
		ExpiresAt:  time.Now().Add(5 * time.Second),
	}

	go func() {
		_, _ = coordinator.RequestApproval(context.Background(), req)
	}()

	time.Sleep(50 * time.Millisecond)

	// Non-admin tries to approve
	err := coordinator.HandleCommandApproval(context.Background(), "req_test_003", "random_user", "allow_once")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quyền quản trị")
}

func TestZaloHITL_CancelPendingRequest(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:  "req_test_004",
		SessionKey: "zalo:group_test",
		ExpiresAt:  time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, _ := coordinator.RequestApproval(context.Background(), req)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)
	coordinator.CancelPendingRequest("req_test_004")

	select {
	case dec := <-done:
		assert.Equal(t, "req_test_004", dec.RequestID)
		assert.False(t, dec.Approved)
		assert.Equal(t, domain.ActionCancelled, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
}

func TestZaloHITL_HandleFlexibleApproval_QuickCommandNoID(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "chat_flexible_1",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_quick_1",
		SessionKey:  "zalo:chat_flexible_1",
		ToolName:    "run_command",
		CommandLine: "ls -la",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Admin executes bare quick command /approve all without specifying ID
	handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_flexible_1", "", "/approve all", "admin_999")
	require.NoError(t, err)
	assert.True(t, handled)

	select {
	case dec := <-done:
		assert.Equal(t, "req_quick_1", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, domain.ActionAllowAllSession, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval via quick command")
	}
}

func TestZaloHITL_HandleFlexibleApproval_ShortCode(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "chat_flexible_2",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_short_1",
		ShortCode:   "4821",
		SessionKey:  "zalo:chat_flexible_2",
		ToolName:    "run_command",
		CommandLine: "git pull",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Admin executes command with shortcode: /approve 4821 session
	handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_flexible_2", "", "/approve 4821 session", "admin_999")
	require.NoError(t, err)
	assert.True(t, handled)

	select {
	case dec := <-done:
		assert.Equal(t, "req_short_1", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, domain.ActionAllowSession, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval via shortcode")
	}
}

func TestZaloHITL_HandleFlexibleApproval_QuoteReply(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "chat_flexible_3",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_quote_1",
		SessionKey:  "zalo:chat_flexible_3",
		ToolName:    "run_command",
		CommandLine: "docker build .",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Simulate quote reply: user replies "ok"
	handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_flexible_3", "", "ok", "admin_999")
	// Since replyToMsgID was empty and "ok" does not start with /approve or #, handled is false
	assert.False(t, handled)

	// Now with quick command /approve
	handled, err = coordinator.HandleFlexibleApproval(context.Background(), "chat_flexible_3", "", "/approve", "admin_999")
	require.NoError(t, err)
	assert.True(t, handled)

	select {
	case dec := <-done:
		assert.Equal(t, "req_quote_1", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, domain.ActionAllowOnce, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval via quote reply")
	}
}

func TestZaloHITL_HandleFlexibleApproval_NonAdminBlocked(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "chat_flexible_4",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_non_admin_1",
		SessionKey:  "zalo:chat_flexible_4",
		ToolName:    "run_command",
		CommandLine: "rm -rf /",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	go func() {
		_, _ = coordinator.RequestApproval(context.Background(), req)
	}()

	time.Sleep(50 * time.Millisecond)

	// Attacker tries to approve
	handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_flexible_4", "", "/approve all", "attacker_user")
	require.NoError(t, err)
	assert.True(t, handled, "Handled must be true to consume the message and prevent it from being sent to LLM")
}

func TestZaloHITL_HandleFlexibleApproval_NonApprovalIgnored(t *testing.T) {
	coordinator := zalo.NewHITLCoordinator(nil, nil)

	for _, msg := range []string{
		"Xin chào bot!",
		"#hashtag",
		"#123",
		"#12345",
		"#9999", // Unregistered 4-digit code must not be hijacked
		"/approved",
		"/denied",
	} {
		handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_1", "", msg, "user_1")
		require.NoError(t, err)
		assert.False(t, handled, "Message %q must not be consumed as approval", msg)
	}
}

func TestZaloHITL_HandleFlexibleApproval_DirectShortCode_And_Vietnamese(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "chat_vn",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_vn_1",
		ShortCode:   "1234",
		SessionKey:  "zalo:chat_vn",
		ToolName:    "run_command",
		CommandLine: "ls -la",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Admin executes direct shortcode with accented Vietnamese: #1234 tất cả
	handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_vn", "", "#1234 tất cả", "admin_999")
	require.NoError(t, err)
	assert.True(t, handled)

	select {
	case dec := <-done:
		assert.Equal(t, "req_vn_1", dec.RequestID)
		assert.True(t, dec.Approved)
		assert.Equal(t, domain.ActionAllowAllSession, dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval via direct shortcode with Vietnamese")
	}
}

func TestZaloHITL_HandleFlexibleApproval_UnknownTargetDoesNotMutateLatest(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
			GroupID:      "chat_guard",
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	req := domain.ApprovalRequest{
		RequestID:   "req_guard_1",
		ShortCode:   "5555",
		SessionKey:  "zalo:chat_guard",
		ToolName:    "run_command",
		CommandLine: "npm test",
		ExpiresAt:   time.Now().Add(5 * time.Second),
	}

	done := make(chan domain.ApprovalDecision, 1)
	go func() {
		dec, err := coordinator.RequestApproval(context.Background(), req)
		require.NoError(t, err)
		done <- dec
	}()

	time.Sleep(50 * time.Millisecond)

	// Admin tries to deny a NON-EXISTENT shortcode /deny 9999
	handled, err := coordinator.HandleFlexibleApproval(context.Background(), "chat_guard", "", "/deny 9999", "admin_999")
	require.NoError(t, err)
	assert.True(t, handled, "Slash command is consumed")

	// Ensure req_guard_1 is STILL pending and was NOT denied!
	select {
	case <-done:
		t.Fatal("req_guard_1 must NOT have been resolved by /deny 9999!")
	case <-time.After(200 * time.Millisecond):
		// Expected: req_guard_1 is still pending
	}

	// Now properly approve req_guard_1
	handled, err = coordinator.HandleFlexibleApproval(context.Background(), "chat_guard", "", "/approve", "admin_999")
	require.NoError(t, err)
	assert.True(t, handled)

	select {
	case dec := <-done:
		assert.Equal(t, "req_guard_1", dec.RequestID)
		assert.True(t, dec.Approved)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for proper approval")
	}
}

func TestZaloHITL_ErrorWrapping_Ports(t *testing.T) {
	cfg := &config.Config{
		Zalo: config.ZaloConfig{
			AdminUserIDs: []string{"admin_999"},
		},
	}
	coordinator := zalo.NewHITLCoordinator(nil, cfg)

	// Non-existent request must return error wrapping ports.ErrNotFound
	err := coordinator.HandleCommandApproval(context.Background(), "non_existent", "admin_999", "allow_once")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrNotFound)

	// Non-admin request must return error wrapping ports.ErrAccessDenied
	req := domain.ApprovalRequest{
		RequestID:  "req_rbac",
		SessionKey: "zalo:test",
		ExpiresAt:  time.Now().Add(5 * time.Second),
	}
	go func() {
		_, _ = coordinator.RequestApproval(context.Background(), req)
	}()
	time.Sleep(50 * time.Millisecond)

	err = coordinator.HandleCommandApproval(context.Background(), "req_rbac", "hacker", "allow_once")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrAccessDenied)

	// Empty userID must return error wrapping ports.ErrAccessDenied
	err = coordinator.HandleCommandApproval(context.Background(), "req_rbac", "", "allow_once")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrAccessDenied)
}
