package retry

import (
	"context"
	"errors"
	"time"
)

// Do executes an operation with exponential backoff and jitter according to the configured options.
// It delegates directly to DoWithResult to ensure zero code duplication.
func Do(ctx context.Context, op func(ctx context.Context) error, opts ...Option) error {
	_, err := DoWithResult(ctx, func(turnCtx context.Context) (struct{}, error) {
		return struct{}{}, op(turnCtx)
	}, opts...)
	return err
}

// DoWithResult executes an operation returning a value (T) and an error, applying
// exponential backoff with jitter on transient failures.
func DoWithResult[T any](ctx context.Context, op func(ctx context.Context) (T, error), opts ...Option) (T, error) {
	var zero T

	// 1. Check if context is already expired before starting
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	cfg := DefaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	cfg.normalize()

	var prevDelay time.Duration

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		res, err := op(ctx)
		if err == nil {
			return res, nil
		}

		// 2. Check for explicit permanent unrecoverable errors
		if IsPermanent(err) {
			unwrapped := errors.Unwrap(err)
			if unwrapped != nil {
				return zero, unwrapped
			}
			return zero, err
		}

		// 3. Evaluate caller retry filter if provided
		if cfg.RetryIf != nil && !cfg.RetryIf(err) {
			return zero, err
		}

		// 4. Do not sleep if this was the last attempt
		if attempt >= cfg.MaxAttempts-1 {
			return zero, err
		}

		// 5. Compute delay: prioritize explicit server RetryAfter if available
		var delay time.Duration
		var delayHandled bool

		if cfg.CustomDelay != nil {
			if explicitDelay, ok := cfg.CustomDelay(err); ok {
				// Server-specified delay is a hard minimum floor: apply strictly additive jitter
				delay = ApplyAdditiveJitter(explicitDelay, cfg.MaxAdditiveJitter)
				delayHandled = true
			}
		}

		if !delayHandled {
			var dp DelayProvider
			if errors.As(err, &dp) {
				delay = ApplyAdditiveJitter(dp.RetryAfter(), cfg.MaxAdditiveJitter)
				delayHandled = true
			}
		}

		if !delayHandled {
			delay = CalculateBackoff(attempt, cfg, prevDelay)
		}
		prevDelay = delay

		// 6. Invoke telemetry / logging callback
		if cfg.OnRetry != nil {
			cfg.OnRetry(ctx, attempt+1, delay, err)
		}

		// 7. Await backoff with safe timer cleanup and context cancellation defense
		if delay <= 0 {
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			continue
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
			// Defend against simultaneous selection race when context cancels at timer expiration
			if err := ctx.Err(); err != nil {
				return zero, err
			}
		}
	}

	return zero, ctx.Err()
}
