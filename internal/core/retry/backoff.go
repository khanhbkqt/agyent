package retry

import (
	"math"
	"math/rand/v2"
	"time"
)

// JitterMode specifies the random jitter strategy applied to exponential delays.
type JitterMode int

const (
	// FullJitter yields a sleep duration uniformly chosen in [0, cappedDuration].
	// This is the canonical AWS/Google recommendation to prevent thundering herd issues.
	FullJitter JitterMode = iota

	// EqualJitter keeps 50% of the capped duration as a guaranteed floor
	// and jitters the remaining 50% uniformly in [cappedDuration/2, cappedDuration].
	EqualJitter

	// DecorrelatedJitter adjusts the delay based on the previous delay,
	// maintaining an evolving spread within [initialInterval, maxInterval].
	DecorrelatedJitter

	// NoJitter disables jitter and uses purely deterministic exponential backoff.
	NoJitter
)

// CalculateBackoff computes the delay for a given attempt index (0-indexed).
// It safely caps exponential growth to prevent float64 and time.Duration overflow.
func CalculateBackoff(attempt int, cfg Config, prevDelay time.Duration) time.Duration {
	if cfg.InitialInterval <= 0 {
		return 0
	}

	maxFloat := float64(cfg.MaxInterval)
	initialFloat := float64(cfg.InitialInterval)

	// Guard against negative attempts
	if attempt < 0 {
		attempt = 0
	}

	// Compute exponential factor safely
	var cappedDuration time.Duration
	if cfg.Multiplier <= 1.0 || attempt == 0 {
		cappedDuration = cfg.InitialInterval
	} else {
		// Guard against float overflow: math.Pow with large attempt
		pow := math.Pow(cfg.Multiplier, float64(attempt))
		if math.IsInf(pow, 0) || pow > maxFloat/initialFloat {
			cappedDuration = cfg.MaxInterval
		} else {
			val := initialFloat * pow
			if val >= maxFloat {
				cappedDuration = cfg.MaxInterval
			} else {
				cappedDuration = time.Duration(val)
			}
		}
	}

	if cappedDuration > cfg.MaxInterval {
		cappedDuration = cfg.MaxInterval
	}

	switch cfg.Jitter {
	case FullJitter:
		if cappedDuration <= 0 {
			return 0
		}
		// rand.N returns a uniform random duration in [0, cappedDuration)
		return rand.N(cappedDuration)

	case EqualJitter:
		if cappedDuration <= 0 {
			return 0
		}
		half := cappedDuration / 2
		if half <= 0 {
			return cappedDuration
		}
		return half + rand.N(half)

	case DecorrelatedJitter:
		// sleep = min(maxInterval, uniform(initialInterval, prevSleep * 3))
		floor := cfg.InitialInterval
		ceiling := prevDelay * 3
		if ceiling < floor {
			ceiling = floor
		}
		if ceiling > cfg.MaxInterval {
			ceiling = cfg.MaxInterval
		}
		delta := ceiling - floor
		if delta <= 0 {
			return floor
		}
		return floor + rand.N(delta)

	case NoJitter:
		fallthrough
	default:
		return cappedDuration
	}
}

// ApplyAdditiveJitter applies a strictly non-negative random jitter to a base delay.
// This is critical when respecting server-specified RetryAfter durations (e.g. HTTP 429),
// ensuring the delay NEVER falls below the server-mandated floor while desynchronizing callers.
func ApplyAdditiveJitter(baseDelay time.Duration, maxJitter time.Duration) time.Duration {
	if baseDelay < 0 {
		baseDelay = 0
	}
	if maxJitter <= 0 {
		return baseDelay
	}
	return baseDelay + rand.N(maxJitter)
}
