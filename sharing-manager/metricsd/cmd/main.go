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

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/metrics"
	gpuext "github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/plugin"

	"github.com/containerd/nri/pkg/stub"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	var (
		configPath    string
		socketPath    string
		pluginName    string
		pluginIndex   string
		logLevel      string
		retryInterval time.Duration
	)

	flag.StringVar(&configPath, "config", stringFromEnv("CONFIG_PATH", gpuext.DefaultConfigPath), "path to the plugin configuration file")
	flag.StringVar(&socketPath, "socket", stringFromEnv("NRI_SOCKET_PATH", gpuext.DefaultNRISocketPath), "path to the NRI runtime socket")
	flag.StringVar(&pluginName, "plugin-name", stringFromEnv("PLUGIN_NAME", gpuext.DefaultPluginName), "NRI plugin name")
	flag.StringVar(&pluginIndex, "plugin-index", stringFromEnv("PLUGIN_INDEX", gpuext.DefaultPluginIndex), "NRI plugin index used for ordering")
	flag.StringVar(&logLevel, "log-level", stringFromEnv("LOG_LEVEL", "info"), "log level: debug, info, warn, or error")
	flag.DurationVar(&retryInterval, "retry-interval", durationFromEnv("RETRY_INTERVAL", 5*time.Second), "delay before reconnecting after the NRI connection exits")
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
	// shared filesystem so the components can be split into two containers without
	mappingReader := fsstore.NewReader(cfg.MapDir, logger)
	metricsRuntime, err := metrics.New(ctx, cfg.Metrics.RuntimeConfig(), mappingReader, logger)
	if err != nil {
		logger.Error("failed to build metrics exporter", "error", err)
		os.Exit(1)
	}
	if metricsRuntime != nil {
		metricsRuntime.Start(ctx)
		defer metricsRuntime.Stop(context.Background(), logger)
	}

	for {
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
			return
		}

		logger.Error("NRI stub exited", "error", err, "retryInterval", retryInterval.String())
		select {
		case <-time.After(retryInterval):
		case <-ctx.Done():
			logger.Info("stopped gpu-sharing-plugin")
			return
		}
	}
}

func stringFromEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
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
