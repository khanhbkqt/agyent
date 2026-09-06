package agy_test

import (
	"context"
	"testing"
	"time"

	"agyent/internal/adapters/harness/agy"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeCapabilities_Mock(t *testing.T) {
	t.Setenv("GO_WANT_MOCK_AGY_HELPER", "1")
	t.Setenv("MOCK_SCENARIO", "success")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	caps, err := agy.ProbeCapabilities(ctx, mockBinaryPath)
	require.NoError(t, err)
	require.NotNil(t, caps)

	assert.Contains(t, caps.Version, "mock")
	assert.True(t, caps.SupportsSandbox)
	assert.True(t, caps.SupportsProjectScopedGrants)
	assert.True(t, caps.SupportsStreamJSON)
	assert.NotEmpty(t, caps.Platform)
}

func TestProbeCapabilities_InvalidBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	caps, err := agy.ProbeCapabilities(ctx, "non_existent_binary_xyz")
	assert.Error(t, err)
	assert.Nil(t, caps)
}
