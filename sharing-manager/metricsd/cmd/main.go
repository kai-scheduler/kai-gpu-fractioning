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

package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/config"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/envutil"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/metrics"
)

const defaultLogLevel = "info"

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// metricsd is the metrics-only sidecar: it reads the container→pod mapping
// written by the sharingd NRI plugin (via the shared MapDir handoff) and exports
// per-pod GPU metrics. It contains no NRI plugin — the mapping is produced by the
// sharingd container it is co-scheduled with in the same DaemonSet pod.
func main() {
	var (
		configPath string
		logLevel   string
	)

	flag.StringVar(&configPath, "config", envutil.StringFromEnv("CONFIG_PATH", config.DefaultConfigPath), "path to the metrics configuration file")
	flag.StringVar(&logLevel, "log-level", envutil.StringFromEnv("LOG_LEVEL", defaultLogLevel), "log level: debug, info, warn, or error")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(logLevel),
	}))

	logger.Info("starting gpu-sharing-metrics",
		"version", version,
		"commit", commit,
		"date", date,
		"config", configPath,
	)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("container→pod mapping handoff directory", "mapDir", cfg.MapDir)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !cfg.Metrics.Enabled {
		logger.Info("GPU metrics exporter disabled; nothing to do")
		return
	}

	// The sharingd NRI plugin writes the container→pod mapping to cfg.MapDir (a
	// shared volume); this sidecar reads it back from the same directory to
	// attribute NVML-reported GPU processes to Kubernetes pods.
	mappingReader := fsstore.NewReader(cfg.MapDir, logger)

	metricsRuntime, err := metrics.New(ctx, cfg.Metrics.RuntimeConfig(), mappingReader, logger)
	if err != nil {
		logger.Error("failed to build metrics exporter", "error", err)
		os.Exit(1)
	}
	metricsRuntime.Start(ctx)
	defer metricsRuntime.Stop(context.Background(), logger)

	<-ctx.Done()
	logger.Info("stopped gpu-sharing-metrics")
}

func parseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
