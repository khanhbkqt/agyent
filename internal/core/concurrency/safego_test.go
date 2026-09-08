package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyent/internal/core/ports"
)

func TestSafeGo_NormalExecution(t *testing.T) {
	var executed atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)

	SafeGo(func() {
		defer wg.Done()
		executed.Store(true)
	})

	wg.Wait()
	if !executed.Load() {
		t.Fatalf("expected SafeGo function to execute")
	}
}

func TestSafeGo_PanicRecovery(t *testing.T) {
	var panicCaught atomic.Bool
	var recoveredValue atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)

	origHandler := defaultPanicHandler
	defer SetDefaultPanicHandler(origHandler)

	SetDefaultPanicHandler(func(r any, stack []byte) {
		panicCaught.Store(true)
		recoveredValue.Store(r)
		wg.Done()
	})

	SafeGo(func() {
		panic("simulated fatal error in worker")
	})

	wg.Wait()
	if !panicCaught.Load() {
		t.Fatalf("expected panic to be caught by panic handler")
	}
	if recoveredValue.Load() != "simulated fatal error in worker" {
		t.Fatalf("unexpected recovered value: %v", recoveredValue.Load())
	}
}

func TestSafeGoWithContext_NormalAndPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	var normalRan atomic.Bool
	SafeGoWithContext(ctx, func(c context.Context) {
		defer wg.Done()
		if c == ctx {
			normalRan.Store(true)
		}
	})

	origHandler := defaultPanicHandler
	defer SetDefaultPanicHandler(origHandler)

	var panicRan atomic.Bool
	SetDefaultPanicHandler(func(r any, stack []byte) {
		panicRan.Store(true)
		wg.Done()
	})

	SafeGoWithContext(ctx, func(c context.Context) {
		panic("context worker panic")
	})

	wg.Wait()
	if !normalRan.Load() {
		t.Errorf("expected normal worker to run")
	}
	if !panicRan.Load() {
		t.Errorf("expected panic worker to be recovered")
	}
}

func TestSessionLockManager_ForceUnlock(t *testing.T) {
	mgr := NewSessionLockManager()
	ctx := context.Background()

	// 1. ForceUnlock non-existent key
	if mgr.ForceUnlock("non_existent") {
		t.Errorf("expected ForceUnlock to return false for non-existent key")
	}

	// 2. Acquire lock
	unlock, err := mgr.Acquire(ctx, "session_1", 1*time.Second)
	if err != nil {
		t.Fatalf("unexpected acquire error: %v", err)
	}

	if mgr.ActiveLockCount() != 1 {
		t.Fatalf("expected 1 active lock, got %d", mgr.ActiveLockCount())
	}

	// 3. ForceUnlock active key
	if !mgr.ForceUnlock("session_1") {
		t.Errorf("expected ForceUnlock to return true for active key")
	}

	// 4. Subsequent acquire should succeed immediately
	unlock2, err := mgr.Acquire(ctx, "session_1", 1*time.Second)
	if err != nil {
		t.Fatalf("unexpected acquire error after force unlock: %v", err)
	}

	// Calling old unlock func should not panic, corrupt state, or revoke unlock2
	unlock()

	// unlock2 is still valid and releasing it should clean up lock count
	unlock2()
	if mgr.ActiveLockCount() != 0 {
		t.Fatalf("expected 0 active locks after all unlocks, got %d", mgr.ActiveLockCount())
	}
}

func TestSessionLockManager_ForceUnlock_WaitersHandedOff(t *testing.T) {
	mgr := NewSessionLockManager()
	ctx := context.Background()

	// 1. Holder acquires lock
	unlock1, err := mgr.Acquire(ctx, "session_wait", 5*time.Second)
	if err != nil {
		t.Fatalf("failed initial acquire: %v", err)
	}

	// 2. Start a concurrent waiter
	var (
		waiterErr error
		waiterUnlock ports.UnlockFunc
		wg   sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		waiterUnlock, waiterErr = mgr.Acquire(ctx, "session_wait", 5*time.Second)
	}()

	// Give goroutine time to block in Acquire
	time.Sleep(50 * time.Millisecond)

	// 3. ForceUnlock while waiter is blocked
	if !mgr.ForceUnlock("session_wait") {
		t.Fatalf("expected ForceUnlock to return true")
	}

	// Wait for waiter to unblock and acquire lock
	wg.Wait()

	// 4. Verify waiter successfully acquired the lock instead of failing with ErrLockCanceled
	if waiterErr != nil {
		t.Fatalf("expected waiter to acquire lock, got error: %v", waiterErr)
	}
	if waiterUnlock == nil {
		t.Fatal("expected waiter unlock func to be non-nil")
	}

	// 5. Old holder unlocks safely (must not release waiter's lock)
	unlock1()

	// 6. Waiter finishes and unlocks
	waiterUnlock()

	// 7. Fresh Acquire succeeds immediately
	unlockFresh, err := mgr.Acquire(ctx, "session_wait", 1*time.Second)
	if err != nil {
		t.Fatalf("expected fresh acquire to succeed, got %v", err)
	}
	unlockFresh()

	if mgr.ActiveLockCount() != 0 {
		t.Fatalf("expected 0 active locks at the end, got %d", mgr.ActiveLockCount())
	}
}
