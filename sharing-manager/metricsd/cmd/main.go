// Copyright 2024 Run.ai Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command metricsd is the GPU metrics sidecar. It reads the container->pod
// mapping the sharingd NRI plugin writes to the shared map directory and exports
// per-pod GPU metrics on a Prometheus endpoint. It runs no NRI plugin of its own;
// the mapping is produced by the sharingd container it is co-scheduled with.
//
// Configuration is entirely flags/env with defaults (no config file), so it can
// be overridden just-in-time by editing the DaemonSet. See flags.go.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/metrics"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	flags := parseFlags()

	logger := configuration.NewLogger(flags.logLevel)

	logger.Info("starting gpu-sharing-metrics",
		"version", version,
		"commit", commit,
		"date", date,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !flags.metricsEnabled {
		logger.Info("GPU metrics exporter disabled; nothing to do")
		return
	}

	logger.Info("container->pod mapping handoff directory", "mapDir", flags.mapDir)

	// sharingd writes the container->pod mapping to mapDir (a shared volume); this
	// sidecar reads it back to attribute NVML-reported GPU processes to pods.
	reader := fsstore.NewReader(flags.mapDir, logger)

	runtime, err := metrics.New(ctx, metrics.Config{
		Enabled:             true,
		Address:             flags.address,
		Path:                flags.path,
		ProcRoot:            flags.procRoot,
		Interval:            flags.interval,
		SMUtilizationWindow: flags.smUtilWindow,
		Names:               flags.metricNames.WithDefaults(),
	}, reader, logger)
	if err != nil {
		logger.Error("failed to build metrics exporter", "error", err)
		os.Exit(1)
	}
	runtime.Start(ctx)
	defer runtime.Stop(context.Background(), logger)

	<-ctx.Done()
	logger.Info("stopped gpu-sharing-metrics")
}
