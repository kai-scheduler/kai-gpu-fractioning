//go:build e2e

package metrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	yaml "go.yaml.in/yaml/v2"
)

const (
	e2eConfigMapNamespace = "gpu-operator"
	e2eConfigMapName      = "nvml-mock-config"
)

// e2eCollector reads GPU process data from the nvml-mock ConfigMap via the
// Kubernetes in-cluster API instead of calling NVML. Compiled only when the
// "e2e" build tag is set (Dockerfile.e2e); never present in the production binary.
type e2eCollector struct {
	mu       sync.RWMutex
	snapshot GPUProcessSnapshot
	interval time.Duration
	client   *http.Client
	token    string
	apiURL   string
	log      *slog.Logger
}

type nvmlMockMemory struct {
	TotalBytes uint64 `yaml:"total_bytes"`
}

type nvmlMockProcess struct {
	PID           uint32 `yaml:"pid"`
	UsedGPUMemory uint64 `yaml:"used_gpu_memory"`
	SMUtil        uint32 `yaml:"sm_util"`
}

type nvmlMockDevice struct {
	Index     int               `yaml:"index"`
	UUID      string            `yaml:"uuid"`
	Memory    nvmlMockMemory    `yaml:"memory"`
	Processes []nvmlMockProcess `yaml:"processes"`
}

type nvmlMockDeviceDefaults struct {
	Memory nvmlMockMemory `yaml:"memory"`
}

// nvmlMockConfigYAML mirrors the subset of the nvml-mock config.yaml that the
// e2e collector needs: device UUIDs, total memory, and per-process entries.
type nvmlMockConfigYAML struct {
	DeviceDefaults nvmlMockDeviceDefaults `yaml:"device_defaults"`
	Devices        []nvmlMockDevice       `yaml:"devices"`
}

// newCollector is the e2e build's implementation. It returns a ConfigMap-backed
// collector that polls the nvml-mock ConfigMap rather than calling NVML.
// Falls back to NoopCollector if the k8s API is unreachable (e.g. not in-cluster).
func newCollector(ctx context.Context, interval time.Duration, logger *slog.Logger) collector {
	c, err := newE2ECollector(interval, logger)
	if err != nil {
		logger.WarnContext(ctx, "e2e: ConfigMap collector unavailable; no GPU metrics will be reported", "error", err)
		return &NoopCollector{}
	}
	logger.InfoContext(ctx, "e2e: using ConfigMap-backed GPU process collector",
		"namespace", e2eConfigMapNamespace, "configmap", e2eConfigMapName)
	return c
}

func newE2ECollector(interval time.Duration, logger *slog.Logger) (*e2eCollector, error) {
	tokenBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}
	caBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("no valid certs found in /var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	}

	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("KUBERNETES_SERVICE_HOST/PORT env vars not set (not running in-cluster?)")
	}

	return &e2eCollector{
		apiURL:   fmt.Sprintf("https://%s:%s", host, port),
		token:    string(tokenBytes),
		interval: interval,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: pool},
			},
		},
		log: logger,
		snapshot: GPUProcessSnapshot{
			DeviceUUIDs:            map[int]string{},
			DeviceTotalMemoryBytes: map[int]uint64{},
		},
	}, nil
}

func (c *e2eCollector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.poll(ctx)

	for {
		select {
		case <-ticker.C:
			c.poll(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (c *e2eCollector) poll(ctx context.Context) {
	snap, err := c.fetchSnapshot(ctx)
	if err != nil {
		c.log.WarnContext(ctx, "e2e: failed to fetch nvml-mock ConfigMap", "error", err)
		return
	}
	c.mu.Lock()
	c.snapshot = snap
	c.mu.Unlock()
}

func (c *e2eCollector) Snapshot() GPUProcessSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneGPUProcessSnapshot(c.snapshot)
}

func (c *e2eCollector) Close() error { return nil }

func (c *e2eCollector) fetchSnapshot(ctx context.Context) (GPUProcessSnapshot, error) {
	url := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps/%s",
		c.apiURL, e2eConfigMapNamespace, e2eConfigMapName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return GPUProcessSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	if err != nil {
		return GPUProcessSnapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return GPUProcessSnapshot{}, fmt.Errorf("k8s API returned %d for %s", resp.StatusCode, url)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return GPUProcessSnapshot{}, fmt.Errorf("read response body: %w", err)
	}

	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &cm); err != nil {
		return GPUProcessSnapshot{}, fmt.Errorf("parse ConfigMap JSON: %w", err)
	}
	configYAML, ok := cm.Data["config.yaml"]
	if !ok {
		return GPUProcessSnapshot{}, fmt.Errorf("ConfigMap %s/%s missing config.yaml key",
			e2eConfigMapNamespace, e2eConfigMapName)
	}
	return parseNVMLMockConfig([]byte(configYAML))
}

func parseNVMLMockConfig(data []byte) (GPUProcessSnapshot, error) {
	var cfg nvmlMockConfigYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return GPUProcessSnapshot{}, fmt.Errorf("parse nvml-mock config YAML: %w", err)
	}

	snap := GPUProcessSnapshot{
		DeviceUUIDs:            make(map[int]string, len(cfg.Devices)),
		DeviceTotalMemoryBytes: make(map[int]uint64, len(cfg.Devices)),
	}
	defaultTotal := cfg.DeviceDefaults.Memory.TotalBytes

	for _, dev := range cfg.Devices {
		snap.DeviceUUIDs[dev.Index] = dev.UUID

		total := defaultTotal
		if dev.Memory.TotalBytes > 0 {
			total = dev.Memory.TotalBytes
		}
		if total > 0 {
			snap.DeviceTotalMemoryBytes[dev.Index] = total
		}

		for _, p := range dev.Processes {
			if p.PID == 0 {
				continue
			}
			snap.Processes = append(snap.Processes, GPUProcessMetric{
				PID:                  p.PID,
				GPUUUID:              dev.UUID,
				GPUIndex:             dev.Index,
				UsedGPUMemoryBytes:   p.UsedGPUMemory,
				SMUtilizationPercent: p.SMUtil,
			})
		}
	}
	return snap, nil
}
