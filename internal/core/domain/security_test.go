package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"agyent/internal/core/domain"
)

func TestParseApprovalAction(t *testing.T) {
	tests := []struct {
		input    string
		expected domain.ApprovalAction
		ok       bool
	}{
		// Allow all session
		{"allow_all_session", domain.ActionAllowAllSession, true},
		{"all_session", domain.ActionAllowAllSession, true},
		{"all", domain.ActionAllowAllSession, true},
		{"ALLOW_ALL", domain.ActionAllowAllSession, true},
		{"3", domain.ActionAllowAllSession, true},
		{"tat_ca", domain.ActionAllowAllSession, true},
		{"tat ca", domain.ActionAllowAllSession, true},
		{"tatca", domain.ActionAllowAllSession, true},
		{"tất cả", domain.ActionAllowAllSession, true},
		{"tất_cả", domain.ActionAllowAllSession, true},
		{"tấtcả", domain.ActionAllowAllSession, true},

		// Allow session
		{"allow_session", domain.ActionAllowSession, true},
		{"session", domain.ActionAllowSession, true},
		{"always", domain.ActionAllowSession, true},
		{"2", domain.ActionAllowSession, true},
		{"phien", domain.ActionAllowSession, true},
		{"ca_phien", domain.ActionAllowSession, true},
		{"ca phien", domain.ActionAllowSession, true},
		{"phiên", domain.ActionAllowSession, true},
		{"cả phiên", domain.ActionAllowSession, true},
		{"cả_phiên", domain.ActionAllowSession, true},
		{"cảphiên", domain.ActionAllowSession, true},

		// Allow once
		{"allow_once", domain.ActionAllowOnce, true},
		{"allow", domain.ActionAllowOnce, true},
		{"once", domain.ActionAllowOnce, true},
		{"approve", domain.ActionAllowOnce, true},
		{"ok", domain.ActionAllowOnce, true},
		{"OK", domain.ActionAllowOnce, true},
		{"yes", domain.ActionAllowOnce, true},
		{"y", domain.ActionAllowOnce, true},
		{"1", domain.ActionAllowOnce, true},
		{"duyet", domain.ActionAllowOnce, true},
		{"duyệt", domain.ActionAllowOnce, true},
		{"dong_y", domain.ActionAllowOnce, true},
		{"dong y", domain.ActionAllowOnce, true},
		{"dongy", domain.ActionAllowOnce, true},
		{"đồng ý", domain.ActionAllowOnce, true},
		{"đồng_ý", domain.ActionAllowOnce, true},
		{"đồngý", domain.ActionAllowOnce, true},
		{"true", domain.ActionAllowOnce, true},

		// Deny
		{"deny", domain.ActionDeny, true},
		{"reject", domain.ActionDeny, true},
		{"no", domain.ActionDeny, true},
		{"n", domain.ActionDeny, true},
		{"4", domain.ActionDeny, true},
		{"tu_choi", domain.ActionDeny, true},
		{"tu choi", domain.ActionDeny, true},
		{"tuchoi", domain.ActionDeny, true},
		{"từ chối", domain.ActionDeny, true},
		{"từ_chối", domain.ActionDeny, true},
		{"từchối", domain.ActionDeny, true},
		{"ko", domain.ActionDeny, true},
		{"khong", domain.ActionDeny, true},
		{"không", domain.ActionDeny, true},
		{"false", domain.ActionDeny, true},

		// Force kill
		{"force_kill", domain.ActionForceKill, true},
		{"kill", domain.ActionForceKill, true},
		{"terminate", domain.ActionForceKill, true},
		{"stop", domain.ActionForceKill, true},
		{"5", domain.ActionForceKill, true},
		{"dung", domain.ActionForceKill, true},
		{"dừng", domain.ActionForceKill, true},

		// Timeout & Cancel
		{"timeout", domain.ActionTimeout, true},
		{"cancelled", domain.ActionCancelled, true},
		{"cancel", domain.ActionCancelled, true},
		{"huy", domain.ActionCancelled, true},
		{"hủy", domain.ActionCancelled, true},

		// Unknown / Invalid
		{"unknown_action", domain.ApprovalAction("unknown_action"), false},
		{"", domain.ApprovalAction(""), false},
		{"foo_bar_baz", domain.ApprovalAction("foo_bar_baz"), false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			act, ok := domain.ParseApprovalAction(tc.input)
			assert.Equal(t, tc.ok, ok, "ParseApprovalAction(%q) ok flag mismatch", tc.input)
			assert.Equal(t, tc.expected, act, "ParseApprovalAction(%q) action mismatch", tc.input)
		})
	}
}
