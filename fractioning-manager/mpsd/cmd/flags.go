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
	"flag"
	"time"

	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/common/configuration"
	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/mpsd/internal"
	"github.com/kai-scheduler/kai-gpu-fractioning/pkg/env"
)

type cliFlags struct {
	// mpsBinary is the path to the nvidia-cuda-mps-control binary.
	mpsBinary string
	// controlPort is the -p value for nvidia-cuda-mps-control.
	controlPort string
	// configPath is the -a MPS config file path (generated at startup).
	configPath string
	// memacctAuditLog enables features.memacct.audit_log in the generated config.
	memacctAuditLog bool
	// pipeDir is CUDA_MPS_PIPE_DIRECTORY, shared with containers.
	pipeDir string
	// logDir is CUDA_MPS_LOG_DIRECTORY, the daemon log output.
	logDir string
	// logLevel is the slog level: debug, info, warn, error.
	logLevel string
	// backoff is the initial delay before restarting after an unexpected exit.
	backoff time.Duration
	// maxRetries is the max restart attempts (0 = unlimited).
	maxRetries int
	// stableThreshold is how long a run must last to reset the retry budget.
	stableThreshold time.Duration
	// gracefulStopDelay is the time to wait for SIGTERM before SIGKILL.
	gracefulStopDelay time.Duration
}

func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.mpsBinary, "mps-binary",
		env.String("MPS_CONTROL_BINARY", internal.DefaultMPSBinary),
		"path to nvidia-cuda-mps-control binary")
	flag.StringVar(&f.controlPort, "mps-control-port",
		env.String("MPS_CONTROL_PORT", internal.DefaultMPSControlPort),
		"value passed to nvidia-cuda-mps-control -p (empty to omit)")
	flag.StringVar(&f.configPath, "mps-config-path",
		env.String("MPS_CONFIG_PATH", internal.DefaultMPSConfigPath),
		"MPS control-daemon config file passed via -a (empty to omit)")
	flag.BoolVar(&f.memacctAuditLog, "memacct-audit-log",
		env.Bool("MPS_MEMACCT_AUDIT_LOG", internal.DefaultMemacctAuditLog),
		"enable features.memacct.audit_log in the generated MPS config")
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
