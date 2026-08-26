package sqlite_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionLockManager_ExhaustiveSuite(t *testing.T) {
	t.Run("Mutual Exclusion and Counter Correctness", func(t *testing.T) {
		lm := sqlite.NewSessionLockManager()
		const goroutines = 50
		const increments = 20
		var counter int
		var wg sync.WaitGroup
		wg.Add(goroutines)

		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				for j := 0; j < increments; j++ {
					unlock, err := lm.Acquire(context.Background(), "session_hot_key", 5*time.Second)
					require.NoError(t, err)
					// Critical Section
					counter++
					time.Sleep(50 * time.Microsecond)
					unlock()
				}
			}()
		}
		wg.Wait()
		assert.Equal(t, goroutines*increments, counter)
		assert.Equal(t, 0, lm.ActiveLockCount(), "Internal locks map must drain to 0 (Zero Memory Leak)")
	})

	t.Run("Independent Keys Do Not Block Each Other", func(t *testing.T) {
		lm := sqlite.NewSessionLockManager()
		ctx := context.Background()

		unlockA, errA := lm.Acquire(ctx, "session_A", 1*time.Second)
		require.NoError(t, errA)

		acquiredB := make(chan bool, 1)
		go func() {
			unlockB, errB := lm.Acquire(ctx, "session_B", 1*time.Second)
			assert.NoError(t, errB)
			acquiredB <- true
			unlockB()
		}()

		select {
		case <-acquiredB:
			// Session B acquired independently
		case <-time.After(200 * time.Millisecond):
			t.Fatal("session_B was improperly blocked by session_A")
		}

		unlockA()
		assert.Equal(t, 0, lm.ActiveLockCount())
	})

	t.Run("Watchdog Timeout Triggering", func(t *testing.T) {
		lm := sqlite.NewSessionLockManager()
		ctx := context.Background()

		unlock1, err := lm.Acquire(ctx, "timeout_key", 1*time.Second)
		require.NoError(t, err)

		start := time.Now()
		_, err = lm.Acquire(ctx, "timeout_key", 100*time.Millisecond)
		elapsed := time.Since(start)

		assert.Error(t, err)
		assert.True(t, errors.Is(err, sqlite.ErrLockTimeout) || strings.Contains(err.Error(), "timeout"), "Must return lock timeout error")
		assert.True(t, elapsed >= 90*time.Millisecond, "Should wait until timeout threshold")

		unlock1()
		require.Eventually(t, func() bool {
			return lm.ActiveLockCount() == 0
		}, 1*time.Second, 10*time.Millisecond, "Reference count must clean up after timeout failure")
	})

	t.Run("Context Cancellation in Queue", func(t *testing.T) {
		lm := sqlite.NewSessionLockManager()
		ctx, cancel := context.WithCancel(context.Background())

		unlock1, err := lm.Acquire(context.Background(), "cancel_key", 1*time.Second)
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() {
			_, err := lm.Acquire(ctx, "cancel_key", 2*time.Second)
			done <- err
		}()

		time.Sleep(50 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			assert.Error(t, err)
			assert.True(t, errors.Is(err, sqlite.ErrLockCanceled) || errors.Is(err, context.Canceled))
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Acquire did not unblock on context cancellation")
		}

		unlock1()
		require.Eventually(t, func() bool {
			return lm.ActiveLockCount() == 0
		}, 1*time.Second, 10*time.Millisecond, "Must cleanly prune entry after context cancel")
	})

	t.Run("Double Unlock Safety", func(t *testing.T) {
		lm := sqlite.NewSessionLockManager()
		unlock, err := lm.Acquire(context.Background(), "double_unlock_key", 1*time.Second)
		require.NoError(t, err)

		assert.NotPanics(t, func() {
			unlock()
			unlock() // Second call must not panic or corrupt ref count
		})
		assert.Equal(t, 0, lm.ActiveLockCount())
	})
}
