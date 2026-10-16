// Package logging configures structured slog output for the daemon and CLI.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Options control log level and format.
type Options struct {
	Level  string // debug|info|warn|error
	Format string // text|json
	Out    io.Writer
}

// New builds a slog.Logger. Defaults: info level, text format, stderr.
func New(opts Options) *slog.Logger {
	out := opts.Out
	if out == nil {
		out = os.Stderr
	}
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(opts.Level)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handlerOpts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(opts.Format, "json") {
		h = slog.NewJSONHandler(out, handlerOpts)
	} else {
		h = slog.NewTextHandler(out, handlerOpts)
	}
	return slog.New(h)
}
