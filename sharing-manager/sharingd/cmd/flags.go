package main

import (
	"flag"
	"time"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/env"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal"
)

const defaultNRISocketPath = "/var/run/nri/nri.sock"

type cliFlags struct {
	// pluginName is the NRI plugin registration name.
	pluginName string
	// pluginIdx is the NRI plugin index; controls hook invocation order.
	pluginIdx string
	// socketPath is the NRI runtime socket path.
	socketPath string
	// annotationPrefix is the annotation prefix for GPU memory config.
	annotationPrefix string
	// mpsPipeDir is the MPS pipe directory path.
	mpsPipeDir string
	// failOpen skips a container on parse error instead of blocking it.
	failOpen bool
	// mapDir is the shared dir for the container->pod mapping handoff.
	mapDir string
	// gpuFraction is the annotation key for the requested GPU fraction.
	gpuFraction string
	// logPodEvents logs each recorded/removed mapping event.
	logPodEvents bool
	// logLevel is the log level (debug, info, warn, error).
	logLevel string
	// retryInterval is the initial wait between NRI connection retries.
	retryInterval time.Duration
	// stableThreshold is how long a connection must last to be considered stable (resets retry budget).
	stableThreshold time.Duration
	// maxRetries is the max NRI connection retries (0 = unlimited).
	maxRetries int
}

func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.pluginName, "plugin-name", internal.DefaultPluginName, "NRI plugin registration name")
	flag.StringVar(&f.pluginIdx, "plugin-idx", internal.DefaultPluginIdx, "NRI plugin index; controls hook invocation order")
	flag.StringVar(&f.socketPath, "socket-path", env.String("NRI_SOCKET_PATH", defaultNRISocketPath), "path to the NRI runtime socket")
	flag.StringVar(&f.annotationPrefix, "annotation-prefix", configuration.DefaultAnnotationPrefix, "annotation prefix for GPU memory config")
	flag.StringVar(&f.mpsPipeDir, "pipe-dir", configuration.DefaultMPSPipeDirectory, "MPS pipe directory path")
	flag.BoolVar(&f.failOpen, "fail-open", false, "if true, annotation parse errors skip the container instead of blocking it")
	flag.StringVar(&f.mapDir, "map-dir", env.String("MAP_DIR", fsstore.DefaultMapDir), "shared directory for the container->pod mapping handoff read by the metrics sidecar")
	flag.StringVar(&f.gpuFraction, "gpu-fraction-annotation", env.String("GPU_FRACTION_ANNOTATION", internal.DefaultGPUFractionAnnotation), "pod annotation key whose value is the requested GPU fraction (empty disables)")
	flag.BoolVar(&f.logPodEvents, "log-pod-events", env.Bool("LOG_POD_EVENTS", false), "log each recorded/removed container->pod mapping event")
	flag.StringVar(&f.logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	flag.DurationVar(&f.retryInterval, "retry-interval", 5*time.Second, "initial wait between NRI connection retries")
	flag.DurationVar(&f.stableThreshold, "stable-threshold", 5*time.Minute, "connection duration considered stable (resets retry budget)")
	flag.IntVar(&f.maxRetries, "max-retries", 0, "max NRI connection retries (0 = unlimited)")
	flag.Parse()
	return f
}
