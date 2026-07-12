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
	"os"
	"os/signal"
	"syscall"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/mpsd/internal"
)

func main() {
	flags := parseFlags()

	logger := configuration.NewLogger(flags.logLevel)
	logger.Info("starting mpsd",
		"pipeDir", flags.pipeDir,
		"logDir", flags.logDir,
		"mpsBinary", flags.mpsBinary,
		"controlPort", flags.controlPort,
		"configPath", flags.configPath,
		"memacctAuditLog", flags.memacctAuditLog,
	)

	// Render the MPS control-daemon config. memacct is enabled and context-share
	// disabled by design; only the audit log is configurable (via Helm value ->
	// MPS_MEMACCT_AUDIT_LOG env). The supervisor writes this to configPath at setup.
	mpsConfig := internal.MPSConfig{
		MemacctEnabled:      internal.DefaultMemacctEnabled,
		MemacctAuditLog:     flags.memacctAuditLog,
		ContextShareEnabled: internal.DefaultContextShareEnabled,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	supervisor := internal.NewSupervisor(internal.SupervisorConfig{
		MPSBinary:         flags.mpsBinary,
		ControlPort:       flags.controlPort,
		ConfigPath:        flags.configPath,
		ConfigContent:     mpsConfig.TOML(),
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
