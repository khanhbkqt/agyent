package concurrency

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
)

// PanicHandler is a callback invoked when a goroutine wrapped by SafeGo panics.
type PanicHandler func(r any, stack []byte)

var defaultPanicHandler PanicHandler = func(r any, stack []byte) {
	fmt.Fprintf(os.Stderr, "[PANIC RECOVERED in SafeGo] %v\nStack Trace:\n%s\n", r, string(stack))
}

// SetDefaultPanicHandler overrides the global panic handler for testing or custom logging.
func SetDefaultPanicHandler(h PanicHandler) {
	if h == nil {
		defaultPanicHandler = func(r any, stack []byte) {
			fmt.Fprintf(os.Stderr, "[PANIC RECOVERED in SafeGo] %v\nStack Trace:\n%s\n", r, string(stack))
		}
		return
	}
	defaultPanicHandler = h
}

// SafeGo spawns a goroutine with top-level panic recovery and stack trace capture.
// If the goroutine panics, the global panic handler is called, preventing process crash.
func SafeGo(fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				if defaultPanicHandler != nil {
					defaultPanicHandler(r, stack)
				}
			}
		}()
		fn()
	}()
}

// SafeGoWithContext spawns a context-aware goroutine with top-level panic recovery.
func SafeGoWithContext(ctx context.Context, fn func(ctx context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				if defaultPanicHandler != nil {
					defaultPanicHandler(r, stack)
				}
			}
		}()
		fn(ctx)
	}()
}
