//go:build windows

package oslock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// AcquireOSFileLock attempts to acquire an exclusive lock on lockPath using Windows LockFileEx with context cancellation and timeout.
func AcquireOSFileLock(ctx context.Context, lockPath string, timeout time.Duration) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}

	if dir := filepath.Dir(lockPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("failed to create lock directory %s: %w", dir, err)
		}
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}

	handle := syscall.Handle(file.Fd())
	deadline := time.Now().Add(timeout)

	for {
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		default:
		}

		var overlapped syscall.Overlapped
		ret, _, _ := procLockFileEx.Call(
			uintptr(handle),
			uintptr(lockfileExclusiveLock|lockfileFailImmediately),
			0,
			1, 0,
			uintptr(unsafe.Pointer(&overlapped)),
		)
		if ret != 0 {
			// Lock acquired successfully
			return func() {
				var ov syscall.Overlapped
				_, _, _ = procUnlockFileEx.Call(uintptr(handle), 0, 1, 0, uintptr(unsafe.Pointer(&ov)))
				_ = file.Close()
			}, nil
		}

		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("timeout waiting for lock on %s after %v", lockPath, timeout)
		}

		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
