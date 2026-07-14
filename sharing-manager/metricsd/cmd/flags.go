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
	"flag"
	"time"

	"github.com/run-ai/gpu-sharing-operator/pkg/env"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/metrics"
)

const (
	defaultLogLevel = "info"
	defaultAddress  = ":2112"
	defaultProcRoot = "/proc"
)

type cliFlags struct {
	// mapDir is the shared dir where sharingd writes the container->pod mapping.
	mapDir string
	// logLevel is the log level: debug, info, warn, error.
	logLevel string
	// metricsEnabled controls whether to run the exporter at all.
	metricsEnabled bool
	// address is the Prometheus listen address.
	address string
	// path is the Prometheus metrics HTTP path.
	path string
	// procRoot is the /proc root used to resolve PID->cgroup.
	procRoot string
	// interval is the NVML sampling interval (floored by the metrics package).
	interval time.Duration
	// smUtilWindow is the sliding-window averaging for SM util (0 disables).
	smUtilWindow time.Duration
}

// parseFlags reads the metricsd configuration from CLI flags, each with an env
// fallback and a default. There is no config file; overrides are made
// just-in-time via DaemonSet args/env.
func parseFlags() cliFlags {
	var f cliFlags
	flag.StringVar(&f.mapDir, "map-dir", env.String("MAP_DIR", fsstore.DefaultMapDir), "shared directory sharingd writes the container->pod mapping to")
	flag.StringVar(&f.logLevel, "log-level", env.String("LOG_LEVEL", defaultLogLevel), "log level: debug, info, warn, or error")
	flag.BoolVar(&f.metricsEnabled, "metrics-enabled", env.Bool("METRICS_ENABLED", true), "run the Prometheus metrics exporter")
	flag.StringVar(&f.address, "metrics-address", env.String("METRICS_ADDRESS", defaultAddress), "Prometheus exporter listen address")
	flag.StringVar(&f.path, "metrics-path", env.String("METRICS_PATH", metrics.DefaultPath), "Prometheus metrics HTTP path")
	flag.StringVar(&f.procRoot, "proc-root", env.String("PROC_ROOT", defaultProcRoot), "/proc root used to resolve GPU process PIDs to cgroups")
	flag.DurationVar(&f.interval, "metrics-interval", env.Duration("METRICS_INTERVAL", metrics.DefaultCollectInterval), "NVML sampling interval (floored at 2s)")
	flag.DurationVar(&f.smUtilWindow, "sm-util-window", env.Duration("SM_UTIL_WINDOW", 0), "sliding-window averaging for SM utilization; 0 disables")
	flag.Parse()
	return f
}

