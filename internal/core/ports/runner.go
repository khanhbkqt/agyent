package ports

import (
	"context"
	"errors"

	"agyent/internal/core/domain"
)

var (
	// ErrConversationNotFound indicates that the specified AGY conversation ID no longer exists or expired.
	ErrConversationNotFound = errors.New("runner: agy conversation not found or expired")
	// ErrProcessExecution indicates the subprocess failed to spawn or crashed.
	ErrProcessExecution = errors.New("runner: agy process execution failed")
	// ErrOutputParse indicates the stdout from the runner could not be parsed.
	ErrOutputParse = errors.New("runner: failed to parse agy output payload")
)

// RunnerPort defines the contract for executing AGY CLI subprocesses.
type RunnerPort interface {
	// Name returns the identifier of the runner backend (e.g., "agy-cli").
	Name() string

	// Execute runs an Antigravity prompt with the specified configuration in batch mode and returns the execution result.
	Execute(ctx context.Context, req domain.ExecutionRequest) (*domain.ExecutionResult, error)

	// ExecuteStream runs an Antigravity prompt in streaming mode, emitting stream events to EventBus and returning the final result.
	ExecuteStream(ctx context.Context, req domain.ExecutionRequest, sessionKey string) (*domain.ExecutionResult, error)

	// HealthCheck checks if the underlying CLI binary is accessible and operational.
	HealthCheck(ctx context.Context) error

	// ListAvailableModels dynamically queries the available models from the underlying CLI.
	ListAvailableModels(ctx context.Context) ([]domain.ModelCapability, error)
}
