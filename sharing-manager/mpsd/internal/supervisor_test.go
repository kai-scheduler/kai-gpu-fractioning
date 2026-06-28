/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package internal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSupervisor_RunsUntilContextExpires(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-mps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	s := NewSupervisor(SupervisorConfig{
		MPSBinary: script,
		PipeDir:   filepath.Join(t.TempDir(), "pipe"),
		LogDir:    filepath.Join(t.TempDir(), "log"),
		Backoff:   100 * time.Millisecond,
		Stdout:    io.Discard,
		Stderr:    io.Discard,
	}, testLogger())

	err := s.Run(ctx)
	if err != nil {
		t.Errorf("Run() returned error: %v", err)
	}
}

func TestSupervisor_RunOnceThenStop(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-mps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := NewSupervisor(SupervisorConfig{
		MPSBinary:       script,
		PipeDir:         filepath.Join(t.TempDir(), "pipe"),
		LogDir:          filepath.Join(t.TempDir(), "log"),
		Backoff:         10 * time.Millisecond,
		MaxRetries:      1,
		StableThreshold: time.Hour,
		Stdout:          io.Discard,
		Stderr:          io.Discard,
	}, testLogger())

	err := s.Run(ctx)
	if err == nil {
		t.Fatal("expected error after single retry, got nil")
	}
	if !strings.Contains(err.Error(), "1 attempts") {
		t.Errorf("expected error to mention '1 attempts', got: %v", err)
	}
}

func TestSupervisor_GracefulShutdown(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-mps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := NewSupervisor(SupervisorConfig{
		MPSBinary:         script,
		PipeDir:           filepath.Join(t.TempDir(), "pipe"),
		LogDir:            filepath.Join(t.TempDir(), "log"),
		Backoff:           100 * time.Millisecond,
		GracefulStopDelay: 2 * time.Second,
		Stdout:            io.Discard,
		Stderr:            io.Discard,
	}, testLogger())

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() returned error on graceful shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s after cancel")
	}
}

func TestSupervisor_GracefulShutdown_SIGKILLAfterDelay(t *testing.T) {
	// Script traps SIGTERM and ignores it — only SIGKILL (after WaitDelay) can stop it.
	// Uses a busy loop (shell built-in) instead of `sleep` to avoid orphaned child
	// processes that hold stdout/stderr pipes open after SIGKILL.
	script := filepath.Join(t.TempDir(), "fake-mps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap '' TERM\nwhile true; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	gracePeriod := 1 * time.Second
	s := NewSupervisor(SupervisorConfig{
		MPSBinary:         script,
		PipeDir:           filepath.Join(t.TempDir(), "pipe"),
		LogDir:            filepath.Join(t.TempDir(), "log"),
		Backoff:           100 * time.Millisecond,
		GracefulStopDelay: gracePeriod,
		Stdout:            io.Discard,
		Stderr:            io.Discard,
	}, testLogger())

	done := make(chan error, 1)
	go func() {
		done <- s.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
		if elapsed < gracePeriod {
			t.Errorf("process exited in %v, expected at least %v (SIGKILL after WaitDelay)", elapsed, gracePeriod)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return within 10s — SIGKILL path may be broken")
	}
}

func TestSupervisor_MaxRetriesExhausted(t *testing.T) {
	// Script that automatically fails
	script := filepath.Join(t.TempDir(), "fake-mps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s := NewSupervisor(SupervisorConfig{
		MPSBinary:       script,
		PipeDir:         filepath.Join(t.TempDir(), "pipe"),
		LogDir:          filepath.Join(t.TempDir(), "log"),
		Backoff:         10 * time.Millisecond,
		MaxRetries:      3,
		StableThreshold: time.Hour,
		Stdout:          io.Discard,
		Stderr:          io.Discard,
	}, testLogger())

	err := s.Run(ctx)
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("expected error to mention '3 attempts', got: %v", err)
	}
}

func TestSupervisor_StableThresholdResetsRetryBudget(t *testing.T) {
	// Script sleeps 100ms then exits — always exceeds the 50ms StableThreshold,
	// so the retry budget resets every time and MaxRetries is never exhausted.
	script := filepath.Join(t.TempDir(), "fake-mps")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.1\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s := NewSupervisor(SupervisorConfig{
		MPSBinary:       script,
		PipeDir:         filepath.Join(t.TempDir(), "pipe"),
		LogDir:          filepath.Join(t.TempDir(), "log"),
		Backoff:         10 * time.Millisecond,
		MaxRetries:      2,
		StableThreshold: 50 * time.Millisecond,
		Stdout:          io.Discard,
		Stderr:          io.Discard,
	}, testLogger())

	err := s.Run(ctx)
	if err != nil {
		t.Errorf("expected nil (context timeout) because stable runs should reset the retry budget, got: %v", err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
