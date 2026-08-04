// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package configuration

import (
	"fmt"
	"log/slog"
	"os"
)

// NewLogger creates a JSON slog.Logger at the given level string.
// If the level is invalid, a warning is printed to stderr and info is used.
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: invalid log level %q, defaulting to info\n", level)
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
