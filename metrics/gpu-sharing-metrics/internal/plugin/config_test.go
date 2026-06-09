package plugin

import (
	"testing"
	"time"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatalf("ParseConfig(nil) returned error: %v", err)
	}
	if !cfg.LogPodEvents {
		t.Fatalf("expected logPodEvents to default to true")
	}
	if !cfg.Metrics.Enabled {
		t.Fatalf("expected metrics.enabled to default to true")
	}
	if cfg.Metrics.Address != DefaultMetricsAddress {
		t.Fatalf("expected metrics address %q, got %q", DefaultMetricsAddress, cfg.Metrics.Address)
	}
	if cfg.Metrics.Interval != DefaultMetricsInterval {
		t.Fatalf("expected metrics interval %q, got %q", DefaultMetricsInterval, cfg.Metrics.Interval)
	}
	if cfg.MapDir != DefaultMapDir {
		t.Fatalf("expected map dir %q, got %q", DefaultMapDir, cfg.MapDir)
	}
}

func TestParseConfigIgnoresLegacyFields(t *testing.T) {
	cfg, err := ParseConfig([]byte(`
enableAnnotation: gpu-sharing-plugin.nvidia.com/enabled
gpuMemoryRequestAnnotation: example.com/request
gpuMemoryLimitAnnotation: example.com/limit
injectEnvironment: true
logPodEvents: false
metrics:
  enabled: false
  address: :9999
  interval: 5s
  procRoot: /host/proc
`))
	if err != nil {
		t.Fatalf("ParseConfig returned error for legacy fields: %v", err)
	}
	if cfg.LogPodEvents {
		t.Fatalf("expected configured logPodEvents=false")
	}
	if cfg.Metrics.Enabled {
		t.Fatalf("expected configured metrics.enabled=false")
	}
	if cfg.Metrics.Address != ":9999" {
		t.Fatalf("expected configured metrics address, got %q", cfg.Metrics.Address)
	}
	if cfg.Metrics.CollectInterval() != 5*time.Second {
		t.Fatalf("expected configured metrics interval 5s, got %s", cfg.Metrics.CollectInterval())
	}
	if cfg.Metrics.ProcRoot != "/host/proc" {
		t.Fatalf("expected configured metrics proc root, got %q", cfg.Metrics.ProcRoot)
	}
}

func TestRuntimeConfigSMUtilizationWindow(t *testing.T) {
	tests := []struct {
		name   string
		window string
		want   time.Duration
	}{
		{"empty disables windowing", "", 0},
		{"invalid string disables windowing", "not-a-duration", 0},
		{"zero disables windowing", "0s", 0},
		{"valid duration propagated", "30s", 30 * time.Second},
		{"minute duration propagated", "1m", time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := MetricsConfig{SMUtilizationWindow: tt.window}
			if got := cfg.RuntimeConfig().SMUtilizationWindow; got != tt.want {
				t.Fatalf("SMUtilizationWindow(%q) = %v, want %v", tt.window, got, tt.want)
			}
		})
	}
}

func TestCollectIntervalFloorAndDefault(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		want     time.Duration
	}{
		{"unset uses default", "", 5 * time.Second},
		{"invalid uses default", "nonsense", 5 * time.Second},
		{"zero uses default", "0s", 5 * time.Second},
		{"below floor clamps to 2s", "500ms", 2 * time.Second},
		{"at floor kept", "2s", 2 * time.Second},
		{"above floor kept", "10s", 10 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (MetricsConfig{Interval: tt.interval}).CollectInterval(); got != tt.want {
				t.Fatalf("CollectInterval(%q) = %v, want %v", tt.interval, got, tt.want)
			}
		})
	}
}
