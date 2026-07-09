package main

import (
	"log/slog"
	"strings"
)

var logLevels = map[string]slog.Level{
	"debug":   slog.LevelDebug,
	"info":    slog.LevelInfo,
	"warn":    slog.LevelWarn,
	"warning": slog.LevelWarn,
	"error":   slog.LevelError,
}

// parseLogLevel maps a log-level string to a slog.Level, defaulting to defaultLogLevel.
func parseLogLevel(level string) slog.Level {
	if l, ok := logLevels[strings.ToLower(strings.TrimSpace(level))]; ok {
		return l
	}
	return logLevels[defaultLogLevel]
}
