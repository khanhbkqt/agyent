package retry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errTransient = errors.New("temporary transient failure")
	errFatal     = errors.New("unrecoverable fatal error")
)

type customRateLimitError struct {
	retryAfter time.Duration
}

func (e *customRateLimitError) Error() string {
	return "rate limited"
}

func (e *customRateLimitError) RetryAfter() time.Duration {
	return e.retryAfter
}

// Simulates gotgbot.TelegramError structure without importing gotgbot
type simulatedTelegram429 struct {
	Code       int
	RetrySec   int64
	StatusText string
}

func (e *simulatedTelegram429) Error() string {
	return e.StatusText
}

func TestRetry_SuccessFirstAttempt(t *testing.T) {
	ctx := context.Background()
	calls := 0

	err := Do(ctx, func(c context.Context) error {
		calls++
		return nil
	}, WithMaxAttempts(3))

	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestRetry_SuccessAfterRetries(t *testing.T) {
	ctx := context.Background()
	calls := 0
	var retryLogs []int

	err := Do(ctx, func(c context.Context) error {
		calls++
		if calls < 3 {
			return errTransient
		}
		return nil
	},
		WithMaxAttempts(4),
		WithInitialInterval(10*time.Millisecond),
		WithMaxInterval(100*time.Millisecond),
		WithOnRetry(func(ctx context.Context, attempt int, delay time.Duration, err error) {
			retryLogs = append(retryLogs, attempt)
			assert.True(t, delay >= 0)
		}),
	)

	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Equal(t, []int{1, 2}, retryLogs)
}

func TestRetry_MaxAttemptsExceeded(t *testing.T) {
	ctx := context.Background()
	calls := 0

	err := Do(ctx, func(c context.Context) error {
		calls++
		return errTransient
	},
		WithMaxAttempts(3),
		WithInitialInterval(5*time.Millisecond),
	)

	assert.ErrorIs(t, err, errTransient)
	assert.Equal(t, 3, calls)
}

func TestRetry_PermanentError(t *testing.T) {
	ctx := context.Background()
	calls := 0

	err := Do(ctx, func(c context.Context) error {
		calls++
		return Permanent(errFatal)
	},
		WithMaxAttempts(5),
		WithInitialInterval(10*time.Millisecond),
	)

	assert.ErrorIs(t, err, errFatal)
	assert.False(t, IsPermanent(err), "Do should return unwrapped error")
	assert.Equal(t, 1, calls, "Permanent error must abort on first attempt")
}

func TestRetry_RetryIfFilter(t *testing.T) {
	ctx := context.Background()
	calls := 0

	err := Do(ctx, func(c context.Context) error {
		calls++
		return errFatal
	},
		WithMaxAttempts(5),
		WithInitialInterval(5*time.Millisecond),
		WithRetryIf(func(err error) bool {
			return errors.Is(err, errTransient)
		}),
	)

	assert.ErrorIs(t, err, errFatal)
	assert.Equal(t, 1, calls, "Should not retry when RetryIf returns false")
}

func TestRetry_ContextCancelledBefore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := Do(ctx, func(c context.Context) error {
		calls++
		return nil
	}, WithMaxAttempts(3))

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, calls, "Must not invoke op when context is already canceled")
}

func TestRetry_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	start := time.Now()

	err := Do(ctx, func(c context.Context) error {
		calls++
		if calls == 1 {
			go func() {
				time.Sleep(20 * time.Millisecond)
				cancel()
			}()
			return errTransient
		}
		return nil
	},
		WithMaxAttempts(5),
		WithInitialInterval(500*time.Millisecond), // Long sleep
	)

	elapsed := time.Since(start)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, calls)
	assert.True(t, elapsed < 250*time.Millisecond, "Should immediately abort when context is canceled")
}

func TestRetry_DelayProvider_AdditiveJitter(t *testing.T) {
	ctx := context.Background()
	serverFloor := 50 * time.Millisecond
	maxJitter := 20 * time.Millisecond

	var observedDelays []time.Duration
	calls := 0

	err := Do(ctx, func(c context.Context) error {
		calls++
		if calls < 3 {
			return &customRateLimitError{retryAfter: serverFloor}
		}
		return nil
	},
		WithMaxAttempts(3),
		WithMaxAdditiveJitter(maxJitter),
		WithOnRetry(func(ctx context.Context, attempt int, delay time.Duration, err error) {
			observedDelays = append(observedDelays, delay)
		}),
	)

	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Len(t, observedDelays, 2)

	for _, d := range observedDelays {
		// Crucial Tech Lead Invariant: Jitter must NEVER reduce below the server floor
		assert.True(t, d >= serverFloor, "Delay %v must be >= server floor %v", d, serverFloor)
		assert.True(t, d <= serverFloor+maxJitter, "Delay %v must be <= floor+jitter %v", d, serverFloor+maxJitter)
	}
}

func TestRetry_WithCustomDelay_TelegramSimulation(t *testing.T) {
	ctx := context.Background()
	tgFloorSec := int64(1) // 1 second
	var observedDelay time.Duration

	err := Do(ctx, func(c context.Context) error {
		return &simulatedTelegram429{Code: 429, RetrySec: tgFloorSec, StatusText: "Too Many Requests"}
	},
		WithMaxAttempts(2),
		WithMaxAdditiveJitter(50*time.Millisecond),
		WithCustomDelay(func(err error) (time.Duration, bool) {
			var tgErr *simulatedTelegram429
			if errors.As(err, &tgErr) && tgErr.Code == 429 && tgErr.RetrySec > 0 {
				return time.Duration(tgErr.RetrySec) * time.Second, true
			}
			return 0, false
		}),
		WithOnRetry(func(ctx context.Context, attempt int, delay time.Duration, err error) {
			observedDelay = delay
		}),
	)

	assert.Error(t, err)
	assert.True(t, observedDelay >= time.Second, "Observed delay %v must respect 1s floor", observedDelay)
	assert.True(t, observedDelay <= time.Second+50*time.Millisecond)
}

func TestRetry_BackoffJitterBounds(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialInterval = 100 * time.Millisecond
	cfg.MaxInterval = 1 * time.Second
	cfg.Multiplier = 2.0

	// FullJitter: delay in [0, capped]
	cfg.Jitter = FullJitter
	for attempt := 0; attempt < 5; attempt++ {
		for sample := 0; sample < 50; sample++ {
			d := CalculateBackoff(attempt, cfg, 0)
			assert.True(t, d >= 0)
			assert.True(t, d <= cfg.MaxInterval)
		}
	}

	// EqualJitter: delay in [half, capped]
	cfg.Jitter = EqualJitter
	for attempt := 0; attempt < 5; attempt++ {
		for sample := 0; sample < 50; sample++ {
			d := CalculateBackoff(attempt, cfg, 0)
			assert.True(t, d >= cfg.InitialInterval/2)
			assert.True(t, d <= cfg.MaxInterval)
		}
	}

	// NoJitter: deterministic
	cfg.Jitter = NoJitter
	d0 := CalculateBackoff(0, cfg, 0)
	assert.Equal(t, 100*time.Millisecond, d0)
	d1 := CalculateBackoff(1, cfg, 0)
	assert.Equal(t, 200*time.Millisecond, d1)
	d2 := CalculateBackoff(2, cfg, 0)
	assert.Equal(t, 400*time.Millisecond, d2)
	d3 := CalculateBackoff(3, cfg, 0)
	assert.Equal(t, 800*time.Millisecond, d3)
	d4 := CalculateBackoff(4, cfg, 0)
	assert.Equal(t, 1000*time.Millisecond, d4, "Must cap at MaxInterval")
}

func TestRetry_BackoffOverflowResistance(t *testing.T) {
	cfg := DefaultConfig()
	cfg.InitialInterval = 500 * time.Millisecond
	cfg.MaxInterval = 30 * time.Second
	cfg.Multiplier = 2.0
	cfg.Jitter = NoJitter

	// Huge attempt count that would cause math.Pow / int64 overflow
	d := CalculateBackoff(100, cfg, 0)
	assert.Equal(t, cfg.MaxInterval, d, "Must safely cap at MaxInterval without overflow")
	assert.True(t, d > 0, "Duration must remain strictly positive")

	dInf := CalculateBackoff(1000, cfg, 0)
	assert.Equal(t, cfg.MaxInterval, dInf)
}

func TestRetry_ConfigNormalization(t *testing.T) {
	cfg := Config{
		MaxAttempts:       -5,
		InitialInterval:   -10 * time.Second,
		MaxInterval:       -5 * time.Second,
		Multiplier:        0.5,
		MaxAdditiveJitter: -100 * time.Millisecond,
	}
	cfg.normalize()

	assert.Equal(t, 3, cfg.MaxAttempts)
	assert.Equal(t, 100*time.Millisecond, cfg.InitialInterval)
	assert.Equal(t, 100*time.Millisecond, cfg.MaxInterval)
	assert.Equal(t, 2.0, cfg.Multiplier)
	assert.Equal(t, time.Duration(0), cfg.MaxAdditiveJitter)
}

func TestRetry_ConcurrentSafe(t *testing.T) {
	// Shared immutable config across 100 goroutines tested with -race
	cfg := FastConfig()
	cfg.MaxAttempts = 3

	var wg sync.WaitGroup
	var successfulOps atomic.Int64
	numWorkers := 100

	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			calls := 0
			err := Do(context.Background(), func(ctx context.Context) error {
				calls++
				if calls < 2 {
					return errTransient
				}
				return nil
			}, WithConfig(cfg))

			if err == nil {
				successfulOps.Add(1)
			}
		}(i)
	}

	wg.Wait()
	assert.Equal(t, int64(numWorkers), successfulOps.Load())
}

func TestRetry_DoWithResult_Generics(t *testing.T) {
	ctx := context.Background()

	type CustomPayload struct {
		ID   string
		Data int
	}

	calls := 0
	res, err := DoWithResult(ctx, func(c context.Context) (*CustomPayload, error) {
		calls++
		if calls < 2 {
			return nil, errTransient
		}
		return &CustomPayload{ID: "payload-123", Data: 42}, nil
	},
		WithMaxAttempts(3),
		WithInitialInterval(5*time.Millisecond),
	)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "payload-123", res.ID)
	assert.Equal(t, 42, res.Data)
	assert.Equal(t, 2, calls)
}
