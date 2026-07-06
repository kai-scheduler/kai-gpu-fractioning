package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/containerd/nri/pkg/stub"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/env"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal"
)

const defaultNRISocketPath = "/var/run/nri/nri.sock"

type cliFlags struct {
	pluginName       string        // NRI plugin registration name
	pluginIdx        string        // NRI plugin index; controls hook invocation order
	socketPath       string        // NRI runtime socket path
	annotationPrefix string        // annotation prefix for GPU memory config
	mpsPipeDir       string        // MPS pipe directory path
	failOpen         bool          // skip container on parse error instead of blocking
	mapDir           string        // shared dir for the container→pod mapping handoff
	gpuFraction      string        // annotation key for the requested GPU fraction
	logPodEvents     bool          // log each recorded/removed mapping event
	logLevel         string        // log level (debug, info, warn, error)
	retryInterval    time.Duration // initial wait between NRI connection retries
	stableThreshold  time.Duration // how long a connection must last to be considered stable (resets retry budget)
	maxRetries       int           // max NRI connection retries (0 = unlimited)
}

func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.pluginName, "plugin-name", internal.DefaultPluginName, "NRI plugin registration name")
	flag.StringVar(&f.pluginIdx, "plugin-idx", internal.DefaultPluginIdx, "NRI plugin index; controls hook invocation order")
	flag.StringVar(&f.socketPath, "socket-path", env.String("NRI_SOCKET_PATH", defaultNRISocketPath), "path to the NRI runtime socket")
	flag.StringVar(&f.annotationPrefix, "annotation-prefix", configuration.DefaultAnnotationPrefix, "annotation prefix for GPU memory config")
	flag.StringVar(&f.mpsPipeDir, "pipe-dir", configuration.DefaultMPSPipeDirectory, "MPS pipe directory path")
	flag.BoolVar(&f.failOpen, "fail-open", false, "if true, annotation parse errors skip the container instead of blocking it")
	flag.StringVar(&f.mapDir, "map-dir", env.String("MAP_DIR", internal.DefaultMapDir), "shared directory for the container→pod mapping handoff read by the metrics sidecar")
	flag.StringVar(&f.gpuFraction, "gpu-fraction-annotation", env.String("GPU_FRACTION_ANNOTATION", internal.DefaultGPUFractionAnnotation), "pod annotation key whose value is the requested GPU fraction (empty disables)")
	flag.BoolVar(&f.logPodEvents, "log-pod-events", env.Bool("LOG_POD_EVENTS", false), "log each recorded/removed container→pod mapping event")
	flag.StringVar(&f.logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	flag.DurationVar(&f.retryInterval, "retry-interval", 5*time.Second, "initial wait between NRI connection retries")
	flag.DurationVar(&f.stableThreshold, "stable-threshold", 5*time.Minute, "connection duration considered stable (resets retry budget)")
	flag.IntVar(&f.maxRetries, "max-retries", 0, "max NRI connection retries (0 = unlimited)")
	flag.Parse()
	return f
}

func main() {
	flags := parseFlags()

	logger := configuration.NewLogger(flags.logLevel)

	plugin := internal.NewPlugin(internal.Config{
		AnnotationPrefix:      flags.annotationPrefix,
		MPSPipeDirectory:      flags.mpsPipeDir,
		FailOpen:              flags.failOpen,
		MapDir:                flags.mapDir,
		GPUFractionAnnotation: flags.gpuFraction,
		LogPodEvents:          flags.logPodEvents,
		Log:                   logger,
	})

	logger.Info("container→pod mapping handoff directory", "mapDir", flags.mapDir)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	err := runWithRetry(ctx, logger, plugin, flags)

	// Drain any queued mapping writes before exiting so the last events reach the
	// shared directory.
	plugin.Flush()

	if err != nil {
		logger.Error("sharingd exiting with error", "error", err)
		os.Exit(1)
	}

	logger.Info("sharingd shutdown complete")
}

// runWithRetry connects to the NRI runtime and runs the plugin. If the
// connection drops (e.g. containerd restarts or the NRI socket is not yet
// available at boot), it retries with exponential backoff (capped at 60s).
//
// Two intervals control behaviour:
//   - retryInterval: how long to wait before retrying (doubles each failure, capped)
//   - stableThreshold: how long a connection must stay up to be considered stable;
//     once stable, the attempt counter and backoff reset so a process that ran for
//     years doesn't exhaust its retry budget from earlier boot failures.
func runWithRetry(ctx context.Context, logger *slog.Logger, plugin *internal.Plugin, flags cliFlags) error {
	const maxBackoff = 60 * time.Second

	attempt := 0
	backoff := flags.retryInterval

	for {
		attempt++
		logger.Info("connecting to NRI runtime", "attempt", attempt, "plugin", flags.pluginName, "idx", flags.pluginIdx, "socket", flags.socketPath)

		s, err := stub.New(plugin,
			stub.WithPluginName(flags.pluginName),
			stub.WithPluginIdx(flags.pluginIdx),
			stub.WithSocketPath(flags.socketPath),
		)
		if err != nil {
			return fmt.Errorf("creating NRI stub: %w", err)
		}

		startTime := time.Now()
		err = s.Run(ctx)

		// ctx was created via signal.NotifyContext with no deadline, so the
		// only reason ctx.Err() can be non-nil is context.Canceled — meaning
		// SIGTERM or SIGINT was received. Log and exit cleanly.
		if ctx.Err() != nil {
			logger.Info("received shutdown signal, exiting cleanly")
			return nil
		}

		uptime := time.Since(startTime)

		// If the connection was stable (lasted longer than stableThreshold),
		// reset the retry budget and backoff.
		if uptime >= flags.stableThreshold {
			logger.Info("connection was stable, resetting retry budget", "uptime", uptime)
			attempt = 0
			backoff = flags.retryInterval
		}

		if flags.maxRetries > 0 && attempt >= flags.maxRetries {
			return fmt.Errorf("NRI connection failed after %d attempts: %w", attempt, err)
		}

		connState := "connection lost"
		if uptime < flags.retryInterval {
			connState = "connection failed"
		}

		logger.Warn(connState+", retrying",
			"error", err,
			"attempt", attempt,
			"uptime", uptime,
			"retryIn", backoff,
		)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		// Exponential backoff: double interval, cap at maxBackoff.
		backoff = min(backoff*2, maxBackoff)
	}
}
