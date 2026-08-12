// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/common/configuration"
	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/mpsd/internal"
	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/mpsd/internal/driverlabel"
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

	// Label the node before starting MPS so operator dependency diagnostics use
	// the driver version mpsd sees locally through NVML.
	if err := driverlabel.LabelCurrentNode(ctx, logger); err != nil {
		logger.Error("label node with NVIDIA driver major version", "error", err)
		os.Exit(1)
	}

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
