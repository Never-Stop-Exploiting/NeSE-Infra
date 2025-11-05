package logging

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger constructs a slog.Logger based on textual level input.
func NewLogger(level string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: parseLevel(level),
	}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
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
