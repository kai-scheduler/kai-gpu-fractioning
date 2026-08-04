// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	DefaultMPSBinary       = "/usr/bin/nvidia-cuda-mps-control"
	DefaultLogDir          = "/var/log/nvidia-mps"
	DefaultBackoff         = 5 * time.Second  // initial wait before first restart
	DefaultMaxRetries      = 5                // after 5 consecutive restarts, the supervisor exits the process. 0 means unlimited restart attempts
	DefaultStableThreshold = 5 * time.Minute  // uptime required to reset the retry budget
	maxBackoff             = 60 * time.Second // upper bound for exponential backoff
	dirPerm                = 0o755            // rwxr-xr-x — used for runtime directories (pipe, log)
	// mpsControlSocket is the fixed filename nvidia-cuda-mps-control creates
	// inside CUDA_MPS_PIPE_DIRECTORY. It is not user-configurable.
	mpsControlSocket         = "control"
	DefaultGracefulStopDelay = 60 * time.Second // time to wait after "quit" before SIGKILL

	DefaultMPSControlPort = "3" // protocol version 3

	DefaultMPSConfigPath = "/etc/nvidia-mps/mps-control.toml" // default path for the MPS config file
	// Fixed MPS feature toggles. memacct is always on and context-share always off by design.
	DefaultMemacctEnabled      = true
	DefaultContextShareEnabled = false
	DefaultMemacctAuditLog     = true // can be overridden by environment variable
)

// SupervisorConfig holds all settings for the MPS daemon supervisor.
type SupervisorConfig struct {
	MPSBinary         string        // path to the nvidia-cuda-mps-control binary
	ControlPort       string        // value for the -p flag; empty omits -p
	ConfigPath        string        // MPS config file for the -a flag; empty omits -a
	ConfigContent     string        // TOML written to ConfigPath at setup; empty skips writing
	PipeDir           string        // CUDA_MPS_PIPE_DIRECTORY — shared with containers
	LogDir            string        // CUDA_MPS_LOG_DIRECTORY — daemon log output
	Backoff           time.Duration // initial delay before restarting after an unexpected exit
	MaxRetries        int           // max restart attempts before giving up (0 = unlimited)
	StableThreshold   time.Duration // how long the daemon must run to be considered stable (resets retry budget)
	GracefulStopDelay time.Duration // time to wait after "quit" before SIGKILL
	Stdout            io.Writer     // subprocess stdout; nil defaults to os.Stdout
	Stderr            io.Writer     // subprocess stderr; nil defaults to os.Stderr
}

// Supervisor manages the nvidia-cuda-mps-control process lifecycle.
// It starts the daemon as a subprocess and restarts it on unexpected exits
// with a configurable backoff.
type Supervisor struct {
	cfg    SupervisorConfig
	logger *slog.Logger
}

// NewSupervisor creates a new Supervisor. Nil Stdout/Stderr default to os.Stdout/os.Stderr.
func NewSupervisor(cfg SupervisorConfig, logger *slog.Logger) *Supervisor {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	return &Supervisor{cfg: cfg, logger: logger}
}

// Run starts the MPS daemon and supervises it until the context is cancelled.
// If the daemon exits unexpectedly, it retries with exponential backoff
// (capped at 60s). The attempt counter resets after a stable run (one that
// lasted longer than StableThreshold), so a daemon that ran for years and
// then crashes gets a fresh retry budget.
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.setup(); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	attempt := 0
	backoff := s.cfg.Backoff

	for {
		attempt++
		startTime := time.Now()
		err := s.runMPS(ctx)

		// ctx.Err() != nil means someone explicitly cancelled our context
		// (e.g. SIGTERM/SIGINT) — this is a legitimate shutdown request,
		// not a daemon crash. Exit without restarting.
		if ctx.Err() != nil {
			s.logger.Info("received shutdown signal, stopping MPS daemon")
			return nil
		}

		uptime := time.Since(startTime)

		if err != nil {
			s.logger.Warn("MPS daemon exited with error, restarting",
				"error", err,
				"attempt", attempt,
				"uptime", uptime,
				"retryIn", backoff,
			)
		} else {
			s.logger.Warn("MPS daemon exited cleanly but unexpectedly, restarting",
				"attempt", attempt,
				"uptime", uptime,
				"retryIn", backoff,
			)
		}

		// If the daemon was stable (ran longer than stableThreshold),
		// reset the retry budget and backoff. Done after logging so the
		// log shows the actual attempt count before the reset.
		if uptime >= s.cfg.StableThreshold {
			s.logger.Info("daemon was stable, resetting retry budget", "uptime", uptime)
			attempt = 0
			backoff = s.cfg.Backoff
		}

		if s.cfg.MaxRetries > 0 && attempt >= s.cfg.MaxRetries {
			return fmt.Errorf("MPS daemon failed after %d attempts: %w", attempt, err)
		}

		s.removeStaleSocket()

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		backoff = min(backoff*2, maxBackoff)
	}
}

// setup creates the runtime directories needed by the MPS daemon and removes
// any stale control socket left by a previous instance (e.g. after a pod
// restart). Without this, the first runMPS call would fail to bind the socket
// and waste one attempt before the retry loop cleans it up.
func (s *Supervisor) setup() error {
	for _, dir := range []string{s.cfg.PipeDir, s.cfg.LogDir} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return fmt.Errorf("creating directory %q: %w", dir, err)
		}
	}
	// Generate the MPS control-daemon config on the fly (its content — e.g. the
	// memacct audit-log toggle — is driven by a Helm value the operator injects
	// as an env var). Regenerating each start lets a config change propagate on
	// the next pod rollout without rebuilding the image.
	if err := s.writeConfig(); err != nil {
		return err
	}
	// Remove any stale control socket left by a previous instance (e.g. after a pod
	// restart). Without this, the first runMPS call would fail to bind the socket
	// and waste one attempt before the retry loop cleans it up.
	s.removeStaleSocket()
	return nil
}

// writeConfig writes the rendered MPS config to ConfigPath. It is a no-op when
// either the path or the content is empty (e.g. tests, or -a explicitly disabled).
func (s *Supervisor) writeConfig() error {
	if s.cfg.ConfigPath == "" || s.cfg.ConfigContent == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.cfg.ConfigPath), dirPerm); err != nil {
		return fmt.Errorf("creating MPS config directory: %w", err)
	}
	if err := os.WriteFile(s.cfg.ConfigPath, []byte(s.cfg.ConfigContent), 0o644); err != nil {
		return fmt.Errorf("writing MPS config %q: %w", s.cfg.ConfigPath, err)
	}
	s.logger.Info("wrote MPS config", "path", s.cfg.ConfigPath)
	return nil
}

// removeStaleSocket removes the MPS control socket left by a crashed instance
// so the next restart can bind to the same path.
func (s *Supervisor) removeStaleSocket() {
	socket := filepath.Join(s.cfg.PipeDir, mpsControlSocket)
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		s.logger.Warn("failed to remove stale MPS control socket", "path", socket, "error", err)
	}
}

// buildMPSArgs builds the nvidia-cuda-mps-control argument list. The daemon is
// always run in the foreground (-f) so we can supervise it. -p (control port)
// and -a (config file) are included only when configured — a blank value acts
// as an escape hatch to drop the flag without rebuilding. Order mirrors the
// known-good production invocation: `-p <port> -f -a <config>`.
func buildMPSArgs(controlPort, configPath string) []string {
	args := make([]string, 0, 4)
	if controlPort != "" {
		args = append(args, "-p", controlPort)
	}
	args = append(args, "-f")
	if configPath != "" {
		args = append(args, "-a", configPath)
	}
	return args
}

// runMPS starts the MPS daemon and blocks until it exits or ctx is cancelled.
// Configuration is applied via environment variables (CUDA_MPS_PIPE_DIRECTORY,
// CUDA_MPS_LOG_DIRECTORY) set on the command, plus the -a config file.
func (s *Supervisor) runMPS(ctx context.Context) error {
	args := buildMPSArgs(s.cfg.ControlPort, s.cfg.ConfigPath)
	cmd := exec.CommandContext(ctx, s.cfg.MPSBinary, args...)
	cmd.Env = append(os.Environ(),
		"CUDA_MPS_PIPE_DIRECTORY="+s.cfg.PipeDir,
		"CUDA_MPS_LOG_DIRECTORY="+s.cfg.LogDir,
	)
	cmd.Stdout = s.cfg.Stdout
	cmd.Stderr = s.cfg.Stderr

	// nvidia-cuda-mps-control -f reads commands from stdin; if stdin is
	// nil (Go default) the process gets immediate EOF and exits. Provide
	// a pipe that stays open so the daemon blocks waiting for input.
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}
	defer func() {
		if err := stdinPipe.Close(); err != nil {
			s.logger.Warn("failed to close stdin pipe", "error", err)
		}
	}()

	// ── Graceful shutdown ───────────────────────────────────────────────
	// When the context is cancelled (pod SIGTERM), Go calls cmd.Cancel
	// which writes "quit" to stdin — the NVIDIA-documented way to stop
	// the MPS control daemon. It cleanly drains active GPU clients and
	// shuts down MPS servers. If the daemon doesn't exit within
	// WaitDelay, Go's exec package sends SIGKILL automatically.
	cmd.Cancel = func() error {
		_, err := fmt.Fprintln(stdinPipe, "quit")
		return err
	}
	cmd.WaitDelay = s.cfg.GracefulStopDelay

	s.logger.Info("starting MPS daemon",
		"binary", s.cfg.MPSBinary,
		"args", args,
		"pipeDir", s.cfg.PipeDir,
		"logDir", s.cfg.LogDir,
		"configPath", s.cfg.ConfigPath,
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("MPS daemon process: %w", err)
	}
	return nil
}
