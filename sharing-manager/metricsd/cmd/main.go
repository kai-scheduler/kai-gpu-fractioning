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
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/envutil"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/metrics"
	gpuext "github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/plugin"

	"github.com/containerd/nri/pkg/stub"
)

const (
	defaultLogLevel      = "info"
	defaultRetryInterval = 5 * time.Second
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	// TODO: once the NRI plugin is extracted into gpu-fractions-operator and metrics
	// runs as a sidecar, collapse these flag vars into a typed config struct and
	// remove the NRI-specific flags (socket, plugin-name, plugin-index).
	var (
		configPath    string
		socketPath    string
		pluginName    string
		pluginIndex   string
		logLevel      string
		retryInterval time.Duration
	)

	flag.StringVar(&configPath, "config", envutil.StringFromEnv("CONFIG_PATH", gpuext.DefaultConfigPath), "path to the plugin configuration file")
	flag.StringVar(&socketPath, "socket", envutil.StringFromEnv("NRI_SOCKET_PATH", gpuext.DefaultNRISocketPath), "path to the NRI runtime socket")
	flag.StringVar(&pluginName, "plugin-name", envutil.StringFromEnv("PLUGIN_NAME", gpuext.DefaultPluginName), "NRI plugin name")
	flag.StringVar(&pluginIndex, "plugin-index", envutil.StringFromEnv("PLUGIN_INDEX", gpuext.DefaultPluginIndex), "NRI plugin index used for ordering")
	flag.StringVar(&logLevel, "log-level", envutil.StringFromEnv("LOG_LEVEL", defaultLogLevel), "log level: debug, info, warn, or error")
	flag.DurationVar(&retryInterval, "retry-interval", envutil.DurationFromEnv("RETRY_INTERVAL", defaultRetryInterval), "delay before reconnecting after the NRI connection exits")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(logLevel),
	}))

	logger.Info("starting gpu-sharing-plugin",
		"version", version,
		"commit", commit,
		"date", date,
		"config", configPath,
		"socket", socketPath,
		"pluginName", pluginName,
		"pluginIndex", pluginIndex,
	)

	cfg, err := gpuext.LoadConfig(configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("container→pod mapping handoff directory", "mapDir", cfg.MapDir)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	plugin := gpuext.New(cfg, logger)

	// The NRI mapper (plugin) writes the container→pod mapping to cfg.MapDir; the
	// metrics component reads it back from the same directory. In this single
	// binary both sides run in-process, but the handoff still goes through the
	// shared filesystem so the components can be split into two
	// containers without any code change — only the deployment topology changes.
	mappingReader := fsstore.NewReader(cfg.MapDir, logger)
	var metricsRuntime *metrics.Runtime
	if cfg.Metrics.Enabled {
		metricsRuntime, err = metrics.New(ctx, cfg.Metrics.RuntimeConfig(), mappingReader, logger)
		if err != nil {
			logger.Error("failed to build metrics exporter", "error", err)
			os.Exit(1)
		}
		metricsRuntime.Start(ctx)
	} else {
		logger.Info("GPU metrics exporter disabled")
	}
	defer metricsRuntime.Stop(context.Background(), logger)

	// TODO: once the NRI plugin is removed from metricsd (next PR), delete runPlugin
	// and this loop entirely — metrics will read the mapping from the shared filesystem
	// written by the gpu-fractions-operator NRI plugin.
	// TODO: add a --max-retries flag (0 = unlimited) so operators can cap reconnect
	// attempts if indefinite retry is undesirable in their environment.
	for {
		if runPlugin(ctx, plugin, pluginName, pluginIndex, socketPath, logger) {
			return
		}
		logger.Error("NRI stub exited, retrying", "retryInterval", retryInterval.String())
		select {
		case <-time.After(retryInterval):
		case <-ctx.Done():
			logger.Info("stopped gpu-sharing-plugin")
			return
		}
	}
}

// runPlugin creates and runs one NRI stub connection. It returns true when the
// context is done (clean shutdown) and false when the connection dropped and the
// caller should retry.
func runPlugin(ctx context.Context, plugin stub.Plugin, pluginName, pluginIndex, socketPath string, logger *slog.Logger) (done bool) {
	nriStub, err := stub.New(plugin,
		stub.WithPluginName(pluginName),
		stub.WithPluginIdx(pluginIndex),
		stub.WithSocketPath(socketPath),
		stub.WithOnClose(func() {
			logger.Warn("NRI runtime connection closed")
		}),
	)
	if err != nil {
		logger.Error("failed to create NRI stub", "error", err)
		os.Exit(1)
	}

	err = nriStub.Run(ctx)
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		logger.Info("stopped gpu-sharing-plugin")
		return true
	}
	return false
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
