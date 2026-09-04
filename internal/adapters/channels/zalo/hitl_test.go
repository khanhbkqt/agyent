package zalo_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/adapters/channels/zalo"
	"agyent/internal/config"
	"agyent/internal/core/domain"

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
		assert.Equal(t, "allow_once", dec.Action)
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
		assert.Equal(t, "allow_session", dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for approval decision")
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
		assert.Equal(t, "cancelled", dec.Action)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
}
