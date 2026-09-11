package network

import (
	"testing"

	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNetworkEvaluator_SSRFProtection(t *testing.T) {
	cfg := config.NetworkGuardrailConfig{
		BlockCloudMetadata:   true,
		BlockPrivateNetworks: true,
		PreventDNSRebinding:  true,
	}

	evaluator := NewEvaluator(cfg)

	// Test 1: Cloud Metadata IP
	decision, err := evaluator.EvaluateURL("http://169.254.169.254/latest/meta-data")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Metadata")

	// Test 2: Localhost Loopback
	decision, err = evaluator.EvaluateURL("http://127.0.0.1:8080/admin")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "private network")

	// Test 3: RFC 1918 Private IP (10.0.0.1)
	decision, err = evaluator.EvaluateURL("https://10.1.2.3/internal/api")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)

	// Test 4: Decimal encoded IP (2130706433 = 127.0.0.1)
	decision, err = evaluator.EvaluateURL("http://2130706433:8080/")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)

	// Test 5: Hex encoded IP (0x7f000001 = 127.0.0.1)
	decision, err = evaluator.EvaluateURL("http://0x7f000001:8080/")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)

	// Test 6: Cloud metadata hostname
	decision, err = evaluator.EvaluateURL("http://metadata.google.internal/computeMetadata/v1/")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "Cloud Instance Metadata")

	// Test 7: Public Safe URL
	decision, err = evaluator.EvaluateURL("https://api.github.com/repos/google/antigravity")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionAllow, decision.Decision)

	// Test 8: file:// scheme bypass attempt
	decision, err = evaluator.EvaluateURL("file:///etc/passwd")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "only http and https are permitted")

	// Test 9: gopher:// protocol smuggling attempt
	decision, err = evaluator.EvaluateURL("gopher://127.0.0.1:6379/_flushall")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "only http and https are permitted")

	// Test 10: Empty scheme or relative URL
	decision, err = evaluator.EvaluateURL("//example.com/test")
	require.NoError(t, err)
	assert.Equal(t, domain.DecisionDeny, decision.Decision)
	assert.Contains(t, decision.Reason, "only http and https are permitted")
}
