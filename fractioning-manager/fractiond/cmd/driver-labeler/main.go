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

	major, version, err := driverlabeler.Run(ctx, driverlabeler.ConfigFromEnv())
	if err != nil {
		slog.Error("failed to label node with NVIDIA driver major version", "err", err)
		os.Exit(1)
	}

	slog.Info("labeled node with NVIDIA driver major version", "driverVersion", version, "driverMajor", major)
}
