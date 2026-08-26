package concurrency_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"agyent/internal/core/concurrency"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestSessionLockManager_ExhaustiveSuite(t *testing.T) {
	defer goleak.VerifyNone(t)

	t.Run("SequentialAcquireAndRelease", func(t *testing.T) {
		lm := concurrency.NewSessionLockManager()
		ctx := context.Background()

		unlock1, err := lm.Acquire(ctx, "session-1", 1*time.Second)
		require.NoError(t, err)
		assert.Equal(t, 1, lm.ActiveLockCount())
		unlock1()

		assert.Equal(t, 0, lm.ActiveLockCount(), "active lock count should be 0 after unlock")

		// Re-acquire same session
		unlock2, err := lm.Acquire(ctx, "session-1", 1*time.Second)
		require.NoError(t, err)
		assert.Equal(t, 1, lm.ActiveLockCount())
		unlock2()
		assert.Equal(t, 0, lm.ActiveLockCount())
	})

	t.Run("MutualExclusionBetweenCompetingGoroutines", func(t *testing.T) {
		lm := concurrency.NewSessionLockManager()
		ctx := context.Background()

		order := make([]int, 0, 2)
		var mu sync.Mutex

		unlock1, err := lm.Acquire(ctx, "session-2", 2*time.Second)
		require.NoError(t, err)

		var wg sync.WaitGroup
		wg.Add(1)

		go func() {
			defer wg.Done()
			unlock2, err := lm.Acquire(ctx, "session-2", 2*time.Second)
			require.NoError(t, err)
			mu.Lock()
			order = append(order, 2)
			mu.Unlock()
			unlock2()
		}()

		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		order = append(order, 1)
		mu.Unlock()
		unlock1()

		wg.Wait()
		assert.Equal(t, []int{1, 2}, order, "first acquirer must execute before second acquirer")
		assert.Equal(t, 0, lm.ActiveLockCount())
	})

	t.Run("TimeoutExceededReturnsErrLockTimeout", func(t *testing.T) {
		lm := concurrency.NewSessionLockManager()
		ctx := context.Background()

		unlock1, err := lm.Acquire(ctx, "session-3", 1*time.Second)
		require.NoError(t, err)
		defer unlock1()

		start := time.Now()
		_, err2 := lm.Acquire(ctx, "session-3", 100*time.Millisecond)
		elapsed := time.Since(start)

		require.Error(t, err2)
		assert.True(t, errors.Is(err2, concurrency.ErrLockTimeout))
		assert.GreaterOrEqual(t, elapsed, 90*time.Millisecond)
	})

	t.Run("ContextCanceledReturnsErrLockCanceled", func(t *testing.T) {
		lm := concurrency.NewSessionLockManager()
		ctx, cancel := context.WithCancel(context.Background())

		unlock1, err := lm.Acquire(ctx, "session-4", 1*time.Second)
		require.NoError(t, err)
		defer unlock1()

		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		_, err2 := lm.Acquire(ctx, "session-4", 2*time.Second)
		require.Error(t, err2)
		assert.True(t, errors.Is(err2, concurrency.ErrLockCanceled))
	})

	t.Run("MultipleIndependentSessionsDoNotBlockEachOther", func(t *testing.T) {
		lm := concurrency.NewSessionLockManager()
		ctx := context.Background()

		unlockA, errA := lm.Acquire(ctx, "session-A", 100*time.Millisecond)
		require.NoError(t, errA)
		defer unlockA()

		unlockB, errB := lm.Acquire(ctx, "session-B", 100*time.Millisecond)
		require.NoError(t, errB)
		defer unlockB()

		assert.Equal(t, 2, lm.ActiveLockCount())
	})
}
