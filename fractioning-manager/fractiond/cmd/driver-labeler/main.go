// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/fractiond/internal/driverlabeler"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// This helper is intentionally one-shot. Init-container restart/backoff is the
	// retry mechanism if NVML or the Kubernetes API is temporarily unavailable.
	major, version, err := driverlabeler.Run(ctx, slog.Default())
	if err != nil {
		slog.Error("failed to label node with NVIDIA driver major version", "err", err)
		os.Exit(1)
	}

	slog.Info("labeled node with NVIDIA driver major version", "driverVersion", version, "driverMajor", major)
}
