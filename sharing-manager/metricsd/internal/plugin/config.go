// Package plugin TODO: NRI-specific config constants (DefaultNRISocketPath, DefaultPluginName,
// DefaultPluginIndex) will be removed when this package is deleted. Only the
// metrics-related config (MapDir, Metrics) survives and moves to a standalone
// config package for the sidecar binary.
package plugin

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/metrics"

	"sigs.k8s.io/yaml"
)

const (
	DefaultPluginName      = "gpu-sharing-plugin"
	DefaultPluginIndex     = "10"
	DefaultNRISocketPath   = "/var/run/nri/nri.sock"
	DefaultConfigPath      = "/etc/gpu-sharing-plugin/config.yaml"
	DefaultMetricsAddress  = ":2112"
	DefaultMetricsPath     = "/metrics"
	DefaultMetricsInterval = "5s"
	DefaultMetricsProcRoot = "/proc"
	// DefaultMapDir is the shared-volume directory the NRI mapper writes
	// <containerID>.json files to and the metrics component reads.
	DefaultMapDir = "/var/run/gpu-sharing/map"
	// DefaultGPUFractionAnnotation is the pod annotation key whose value is the
	// requested GPU fraction (e.g. "0.5") used to normalize SM utilization.
	DefaultGPUFractionAnnotation = "gpu-fraction"
)

type Config struct {
	LogPodEvents bool `json:"logPodEvents"`
	// MapDir is the shared directory for the container→pod mapping handoff: the
	// NRI mapper writes one <containerID>.json file per container here and the
	// metrics component reads them.
	MapDir string `json:"mapDir"`
	// GPUFractionAnnotation is the pod annotation key whose value is the requested
	// GPU fraction used to normalize SM utilization. Empty disables normalization.
	GPUFractionAnnotation string `json:"gpuFractionAnnotation"`
	// Metrics configures the Prometheus GPU metrics exporter.
	Metrics MetricsConfig `json:"metrics"`
}

// MetricsConfig is the on-disk configuration for the metrics exporter. It holds
// the YAML-friendly representation (Interval is a string); RuntimeConfig converts
// it into the metrics package's runtime metrics.Config.
type MetricsConfig struct {
	Enabled  bool   `json:"enabled"`
	Address  string `json:"address"`
	Path     string `json:"path"`
	Interval string `json:"interval"`
	// SMUtilizationWindow enables sliding-window averaging of per-pod GPU SM
	// utilization when greater than Interval (e.g. "30s"). Empty = disabled.
	SMUtilizationWindow string              `json:"smUtilizationWindow"`
	ProcRoot            string              `json:"procRoot"`
	Names               metrics.MetricNames `json:"metricNames"`
}

func DefaultConfig() Config {
	return Config{
		LogPodEvents:          true,
		MapDir:                DefaultMapDir,
		GPUFractionAnnotation: DefaultGPUFractionAnnotation,
		Metrics:               DefaultMetricsConfig(),
	}
}

func DefaultMetricsConfig() MetricsConfig {
	return MetricsConfig{
		Enabled:  true,
		Address:  DefaultMetricsAddress,
		Path:     DefaultMetricsPath,
		Interval: DefaultMetricsInterval,
		ProcRoot: DefaultMetricsProcRoot,
		Names:    metrics.DefaultMetricNames(),
	}
}

func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultConfig(), nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg, err := ParseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	return cfg, nil
}

func ParseConfig(data []byte) (Config, error) {
	cfg := DefaultConfig()
	if strings.TrimSpace(string(data)) == "" {
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg.withDefaults(), nil
}

func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.MapDir) == "" {
		c.MapDir = DefaultMapDir
	}
	c.Metrics = c.Metrics.withDefaults()
	return c
}

func (m MetricsConfig) withDefaults() MetricsConfig {
	d := DefaultMetricsConfig()
	if strings.TrimSpace(m.Address) == "" {
		m.Address = d.Address
	}
	if strings.TrimSpace(m.Path) == "" {
		m.Path = d.Path
	}
	if strings.TrimSpace(m.Interval) == "" {
		m.Interval = d.Interval
	}
	if strings.TrimSpace(m.ProcRoot) == "" {
		m.ProcRoot = d.ProcRoot
	}
	m.Names = m.Names.WithDefaults()
	return m
}

// CollectInterval parses the configured interval, falling back to the default when
// unset or invalid and clamping to the floor (frequent NVML polling adds cost
// without resolution). The bounds are shared with the metrics package so the
// on-disk conversion and the runtime exporter agree.
func (m MetricsConfig) CollectInterval() time.Duration {
	duration, err := time.ParseDuration(strings.TrimSpace(m.Interval))
	if err != nil || duration <= 0 {
		return metrics.DefaultCollectInterval
	}
	if duration < metrics.MinCollectInterval {
		return metrics.MinCollectInterval
	}
	return duration
}

// RuntimeConfig converts the on-disk metrics settings into the metrics package's
// runtime configuration, parsing the interval string into a duration.
func (m MetricsConfig) RuntimeConfig() metrics.Config {
	var smUtilWindow time.Duration
	if s := strings.TrimSpace(m.SMUtilizationWindow); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			smUtilWindow = d
		}
	}
	return metrics.Config{
		Enabled:             m.Enabled,
		Address:             m.Address,
		Path:                m.Path,
		ProcRoot:            m.ProcRoot,
		Interval:            m.CollectInterval(),
		SMUtilizationWindow: smUtilWindow,
		Names:               m.Names,
	}
}
