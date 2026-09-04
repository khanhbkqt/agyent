package retry

import (
	"context"
	"errors"
	"time"
)

// permanentError wraps an underlying error to mark it as unrecoverable.
type permanentError struct {
	err error
}

func (p *permanentError) Error() string {
	if p.err != nil {
		return p.err.Error()
	}
	return "permanent error"
}

func (p *permanentError) Unwrap() error {
	return p.err
}

// Permanent marks an error as non-retryable, instructing the retry runner
// to immediately abort and return the unwrapped inner error without expending further attempts.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &permanentError{err: err}
}

// IsPermanent returns true if the error (or any error in its chain) was wrapped with Permanent.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var pe *permanentError
	return errors.As(err, &pe)
}

// DelayProvider is an optional interface that domain errors can implement to specify
// an explicit retry delay (e.g. from an HTTP 429 Retry-After response).
type DelayProvider interface {
	RetryAfter() time.Duration
}

// CustomDelayFunc is a caller-supplied function that extracts an explicit delay from an error.
// Returning (delay, true) causes the retry runner to honor that duration as a floor.
type CustomDelayFunc func(error) (time.Duration, bool)

// RetryPredicate determines whether a given error should be retried.
type RetryPredicate func(error) bool

// OnRetryCallback is invoked before each backoff sleep, providing execution context,
// attempt number (1-indexed), the computed delay, and the error triggering the retry.
type OnRetryCallback func(ctx context.Context, attempt int, delay time.Duration, err error)

// Config encapsulates tuning parameters for exponential backoff and retry behavior.
// All fields are safe for concurrent read access once initialized.
type Config struct {
	// MaxAttempts is the total number of execution attempts (including the initial call).
	// Must be >= 1. Default is 3.
	MaxAttempts int

	// InitialInterval is the baseline backoff delay before multiplier is applied.
	// Default is 500ms.
	InitialInterval time.Duration

	// MaxInterval is the upper bound on exponential backoff delays.
	// Default is 10s.
	MaxInterval time.Duration

	// Multiplier is the rate of exponential growth per attempt.
	// Must be > 1.0. Default is 2.0.
	Multiplier float64

	// Jitter specifies the randomization strategy.
	// Default is FullJitter.
	Jitter JitterMode

	// MaxAdditiveJitter defines the maximum random duration added to server-instructed
	// RetryAfter durations to desynchronize simultaneous callers. Default is 250ms.
	MaxAdditiveJitter time.Duration

	// RetryIf evaluates whether an error is transient and eligible for retry.
	// If nil, all non-permanent errors are retried.
	RetryIf RetryPredicate

	// CustomDelay allows callers to extract explicit delay durations from error types
	// without coupling internal/core/retry to third-party packages.
	CustomDelay CustomDelayFunc

	// OnRetry is an optional callback for structured logging or telemetry.
	OnRetry OnRetryCallback
}

// normalize sanitizes configuration fields against zero, negative, or invalid values.
func (c *Config) normalize() {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.InitialInterval <= 0 {
		c.InitialInterval = 100 * time.Millisecond
	}
	if c.MaxInterval < c.InitialInterval {
		c.MaxInterval = c.InitialInterval
	}
	if c.Multiplier <= 1.0 {
		c.Multiplier = 2.0
	}
	if c.MaxAdditiveJitter < 0 {
		c.MaxAdditiveJitter = 0
	}
}

// DefaultConfig returns standard production defaults for general operations.
func DefaultConfig() Config {
	return Config{
		MaxAttempts:       3,
		InitialInterval:   500 * time.Millisecond,
		MaxInterval:       10 * time.Second,
		Multiplier:        2.0,
		Jitter:            FullJitter,
		MaxAdditiveJitter: 250 * time.Millisecond,
	}
}

// NetworkConfig returns conservative defaults tuned for external HTTP / Telegram API calls.
func NetworkConfig() Config {
	return Config{
		MaxAttempts:       5,
		InitialInterval:   200 * time.Millisecond,
		MaxInterval:       15 * time.Second,
		Multiplier:        2.0,
		Jitter:            FullJitter,
		MaxAdditiveJitter: 250 * time.Millisecond,
	}
}

// FastConfig returns tight defaults tuned for local I/O, file locks, or IPC operations.
func FastConfig() Config {
	return Config{
		MaxAttempts:       5,
		InitialInterval:   50 * time.Millisecond,
		MaxInterval:       500 * time.Millisecond,
		Multiplier:        1.5,
		Jitter:            FullJitter,
		MaxAdditiveJitter: 50 * time.Millisecond,
	}
}

// Option configures retry execution parameters.
type Option func(*Config)

// WithConfig initializes options from an existing Config struct.
func WithConfig(cfg Config) Option {
	return func(c *Config) {
		*c = cfg
	}
}

// WithMaxAttempts sets the total number of attempts.
func WithMaxAttempts(attempts int) Option {
	return func(c *Config) {
		c.MaxAttempts = attempts
	}
}

// WithInitialInterval sets the baseline delay.
func WithInitialInterval(d time.Duration) Option {
	return func(c *Config) {
		c.InitialInterval = d
	}
}

// WithMaxInterval sets the upper bound on delay.
func WithMaxInterval(d time.Duration) Option {
	return func(c *Config) {
		c.MaxInterval = d
	}
}

// WithMultiplier sets the exponential multiplier factor.
func WithMultiplier(m float64) Option {
	return func(c *Config) {
		c.Multiplier = m
	}
}

// WithJitter sets the jitter strategy.
func WithJitter(mode JitterMode) Option {
	return func(c *Config) {
		c.Jitter = mode
	}
}

// WithMaxAdditiveJitter sets the ceiling for additive jitter on explicit delays.
func WithMaxAdditiveJitter(d time.Duration) Option {
	return func(c *Config) {
		c.MaxAdditiveJitter = d
	}
}

// WithRetryIf configures the transient error evaluation predicate.
func WithRetryIf(predicate RetryPredicate) Option {
	return func(c *Config) {
		c.RetryIf = predicate
	}
}

// WithCustomDelay configures a function to extract explicit retry delay from an error.
func WithCustomDelay(fn CustomDelayFunc) Option {
	return func(c *Config) {
		c.CustomDelay = fn
	}
}

// WithOnRetry registers a callback invoked prior to each retry sleep.
func WithOnRetry(cb OnRetryCallback) Option {
	return func(c *Config) {
		c.OnRetry = cb
	}
}
