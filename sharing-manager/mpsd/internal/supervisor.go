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
)

// SupervisorConfig holds all settings for the MPS daemon supervisor.
type SupervisorConfig struct {
	MPSBinary         string        // path to the nvidia-cuda-mps-control binary
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
	// Remove any stale control socket left by a previous instance (e.g. after a pod
	// restart). Without this, the first runMPS call would fail to bind the socket
	// and waste one attempt before the retry loop cleans it up.
	s.removeStaleSocket()
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

// runMPS starts the MPS daemon and blocks until it exits or ctx is cancelled.
// Configuration is applied via environment variables (CUDA_MPS_PIPE_DIRECTORY,
// CUDA_MPS_LOG_DIRECTORY) set on the command.
func (s *Supervisor) runMPS(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.cfg.MPSBinary, "-f")
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
		"pipeDir", s.cfg.PipeDir,
		"logDir", s.cfg.LogDir,
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("MPS daemon process: %w", err)
	}
	return nil
}
