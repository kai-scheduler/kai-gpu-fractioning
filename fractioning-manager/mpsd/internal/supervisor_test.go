// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
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

func TestBuildMPSArgs(t *testing.T) {
	tests := []struct {
		name        string
		controlPort string
		configPath  string
		multiuser   bool
		want        []string
	}{
		{
			name:        "port, config and multiuser (production invocation)",
			controlPort: DefaultMPSControlPort,
			configPath:  DefaultMPSConfigPath,
			multiuser:   true,
			want:        []string{"-p", "3", "-m", "-f", "-a", "/etc/nvidia-mps/mps-control.toml"},
		},
		{
			// sm-sharing disabled: the shared server is gone, so -m must go
			// with it, restoring the exact pre-feature invocation.
			name:        "sm-sharing disabled omits -m",
			controlPort: DefaultMPSControlPort,
			configPath:  DefaultMPSConfigPath,
			multiuser:   false,
			want:        []string{"-p", "3", "-f", "-a", "/etc/nvidia-mps/mps-control.toml"},
		},
		{
			name:        "empty port omits -p",
			controlPort: "",
			configPath:  DefaultMPSConfigPath,
			multiuser:   true,
			want:        []string{"-m", "-f", "-a", "/etc/nvidia-mps/mps-control.toml"},
		},
		{
			name:        "empty config omits -a",
			controlPort: DefaultMPSControlPort,
			configPath:  "",
			multiuser:   true,
			want:        []string{"-p", "3", "-m", "-f"},
		},
		{
			name:        "both empty leaves only -m -f",
			controlPort: "",
			configPath:  "",
			multiuser:   true,
			want:        []string{"-m", "-f"},
		},
		{
			name:        "everything off leaves only -f",
			controlPort: "",
			configPath:  "",
			multiuser:   false,
			want:        []string{"-f"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildMPSArgs(tt.controlPort, tt.configPath, tt.multiuser)
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildMPSArgs(%q, %q, %t) = %v, want %v", tt.controlPort, tt.configPath, tt.multiuser, got, tt.want)
			}
		})
	}
}

func TestSupervisor_SetupWritesConfig(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "nested", "mps-control.toml")
	content := MPSConfig{MemacctEnabled: true, MemacctAuditLog: true}.TOML()

	s := NewSupervisor(SupervisorConfig{
		MPSBinary:     "/bin/true",
		ConfigPath:    configPath,
		ConfigContent: content,
		PipeDir:       filepath.Join(t.TempDir(), "pipe"),
		LogDir:        filepath.Join(t.TempDir(), "log"),
		Stdout:        io.Discard,
		Stderr:        io.Discard,
	}, testLogger())

	if err := s.setup(); err != nil {
		t.Fatalf("setup() error: %v", err)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	if string(got) != content {
		t.Errorf("config file =\n%q\nwant\n%q", got, content)
	}
}

func TestSupervisor_SetupSkipsConfigWhenEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "mps-control.toml")

	s := NewSupervisor(SupervisorConfig{
		MPSBinary:  "/bin/true",
		ConfigPath: configPath,
		// ConfigContent empty -> no file written
		PipeDir: filepath.Join(t.TempDir(), "pipe"),
		LogDir:  filepath.Join(t.TempDir(), "log"),
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	}, testLogger())

	if err := s.setup(); err != nil {
		t.Fatalf("setup() error: %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("expected no config file, stat err = %v", err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
