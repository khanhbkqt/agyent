package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Options holds logger configuration settings.
type Options struct {
	Level     string    // "debug" | "info" | "warn" | "error"
	Format    string    // "text" | "json"
	AddSource bool      // If true, logs include file:line source information
	Output    io.Writer // Destination writer (defaults to os.Stdout)
}

// ParseLevel converts a level string into a slog.Level.
func ParseLevel(lvl string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Init configures and sets the global default slog logger based on provided options.
func Init(opts Options) *slog.Logger {
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	level := ParseLevel(opts.Level)
	addSource := opts.AddSource || level == slog.LevelDebug

	var handler slog.Handler
	handlerOpts := &slog.HandlerOptions{
		Level:     level,
		AddSource: addSource,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.Format("2006-01-02 15:04:05.000"))
				}
			}
			return a
		},
	}

	switch strings.ToLower(strings.TrimSpace(opts.Format)) {
	case "json":
		handler = slog.NewJSONHandler(out, handlerOpts)
	default:
		handler = slog.NewTextHandler(out, handlerOpts)
	}

	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}

// Named returns a sub-logger tagged with a module name.
func Named(module string) *slog.Logger {
	return slog.Default().With(slog.String("module", module))
}
