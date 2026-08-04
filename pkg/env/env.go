// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package env provides helpers for reading typed values from environment
// variables with fallback defaults.
package env

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// String returns the value of the environment variable named by key,
// or fallback if the variable is not set or empty.
func String(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Bool returns the boolean value of the environment variable named by key,
// or fallback if the variable is not set or cannot be parsed.
func Bool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: invalid value %q for env var %s (expected bool), using default %v\n", v, key, fallback)
		return fallback
	}
	return b
}

// Int returns the integer value of the environment variable named by key,
// or fallback if the variable is not set or cannot be parsed.
func Int(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: invalid value %q for env var %s (expected integer), using default %d\n", v, key, fallback)
		return fallback
	}
	return i
}

// Duration returns the duration value of the environment variable named
// by key, or fallback if the variable is not set or cannot be parsed.
func Duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: invalid value %q for env var %s (expected duration like \"30s\"), using default %s\n", v, key, fallback)
		return fallback
	}
	return d
}
