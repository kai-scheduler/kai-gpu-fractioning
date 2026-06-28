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

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/env"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/mpsd/internal"
)

func main() {
	flags := parseFlags()

	logger := configuration.NewLogger(flags.logLevel)
	logger.Info("starting mpsd",
		"pipeDir", flags.pipeDir,
		"logDir", flags.logDir,
		"mpsBinary", flags.mpsBinary,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	supervisor := internal.NewSupervisor(internal.SupervisorConfig{
		MPSBinary:         flags.mpsBinary,
		PipeDir:           flags.pipeDir,
		LogDir:            flags.logDir,
		Backoff:           flags.backoff,
		MaxRetries:        flags.maxRetries,
		StableThreshold:   flags.stableThreshold,
		GracefulStopDelay: flags.gracefulStopDelay,
	}, logger)

	if err := supervisor.Run(ctx); err != nil {
		logger.Error("mpsd exiting with error", "error", err)
		os.Exit(1)
	}

	logger.Info("mpsd shutdown complete")
}

type cliFlags struct {
	mpsBinary         string        // path to nvidia-cuda-mps-control binary
	pipeDir           string        // CUDA_MPS_PIPE_DIRECTORY — shared with containers
	logDir            string        // CUDA_MPS_LOG_DIRECTORY — daemon log output
	logLevel          string        // slog level: debug, info, warn, error
	backoff           time.Duration // initial delay before restarting after unexpected exit
	maxRetries        int           // max restart attempts (0 = unlimited)
	stableThreshold   time.Duration // how long a run must last to reset the retry budget
	gracefulStopDelay time.Duration // time to wait for SIGTERM before SIGKILL
}

func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.mpsBinary, "mps-binary",
		env.String("MPS_CONTROL_BINARY", internal.DefaultMPSBinary),
		"path to nvidia-cuda-mps-control binary")
	flag.StringVar(&f.pipeDir, "pipe-dir",
		env.String("CUDA_MPS_PIPE_DIRECTORY", configuration.DefaultMPSPipeDirectory),
		"CUDA MPS pipe directory")
	flag.StringVar(&f.logDir, "log-dir",
		env.String("CUDA_MPS_LOG_DIRECTORY", internal.DefaultLogDir),
		"CUDA MPS log directory")
	flag.StringVar(&f.logLevel, "log-level",
		env.String("LOG_LEVEL", "info"),
		"log level (debug, info, warn, error)")
	flag.DurationVar(&f.backoff, "backoff",
		env.Duration("MPS_RESTART_BACKOFF", internal.DefaultBackoff),
		"initial delay before restarting MPS daemon after crash")
	flag.IntVar(&f.maxRetries, "max-retries",
		env.Int("MPS_MAX_RETRIES", internal.DefaultMaxRetries),
		"max restart attempts before giving up (0 = unlimited)")
	flag.DurationVar(&f.stableThreshold, "stable-threshold",
		env.Duration("MPS_STABLE_THRESHOLD", internal.DefaultStableThreshold),
		"how long the daemon must run to be considered stable (resets retry budget)")
	flag.DurationVar(&f.gracefulStopDelay, "graceful-stop-delay",
		env.Duration("MPS_GRACEFUL_STOP_DELAY", internal.DefaultGracefulStopDelay),
		"time to wait for SIGTERM before SIGKILL on shutdown")
	flag.Parse()
	return f
}
