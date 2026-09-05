package zalo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type denyingURLSafetyEvaluator struct{}

func (denyingURLSafetyEvaluator) EvaluateURL(context.Context, string) (domain.SecurityDecision, error) {
	return domain.SecurityDecision{Decision: domain.DecisionDeny}, nil
}

func TestMediaManager_RejectsNetworkPolicyDeniedURLBeforeFetch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	manager := &MediaManager{
		stagingDir: t.TempDir(),
		httpClient: server.Client(),
	}
	manager.SetURLSafetyEvaluator(denyingURLSafetyEvaluator{})

	_, err := manager.DownloadInboundAttachment(context.Background(), server.URL+"/attachment", "image.jpg")
	require.Error(t, err)
	assert.ErrorIs(t, err, ports.ErrAttachmentNotFound)
	assert.Zero(t, requests.Load())
}
