// Package config loads e2e run configuration entirely from the environment so
// the suite can point at whatever cluster the caller provides (no config file,
// no assumption about how the cluster was created). The suite only connects to
// an existing cluster — it never provisions one.
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config is read entirely from the environment.
type Config struct {
	// Kubeconfig is the path to the kubeconfig used to reach the cluster.
	Kubeconfig string

	// OperatorNamespace is where the gpu-sharing-operator is installed and,
	// therefore, where the operator-managed DaemonSets (sharingd — which hosts
	// the metricsd sidecar — and mpsd) are created. The suite scrapes the
	// metricsd sidecar in the sharingd pods here.
	OperatorNamespace string

	// GPUNodeSelector selects nodes expected to advertise GPUs.
	GPUNodeSelector string

	// GPUNodeCount, if > 0, asserts the exact count of nodes matching
	// GPUNodeSelector; 0 skips the assertion (and the node listing) entirely.
	// Set it to the number of GPU nodes the target cluster is expected to have,
	// to catch a misconfigured/partially-up cluster before any test runs.
	GPUNodeCount int

	// PodReadyTimeout bounds how long workload.Apply waits for a test pod to
	// reach Running.
	PodReadyTimeout time.Duration

	// PollInterval is the poll cadence shared by the workload/nvmlmock waiters.
	PollInterval time.Duration

	// DaemonSetReadyTimeout bounds how long nvmlmock waits for a DaemonSet
	// rollout (nvml-mock or sharingd) to complete after a config change.
	DaemonSetReadyTimeout time.Duration
}

// Load builds a Config from environment variables.
func Load() Config {
	return Config{
		Kubeconfig:            envOr("E2E_KUBECONFIG", defaultKubeconfig()),
		OperatorNamespace:     envOr("E2E_OPERATOR_NAMESPACE", "gpu-sharing-operator"),
		GPUNodeSelector:       envOr("E2E_GPU_NODE_SELECTOR", "nvidia.com/gpu.present=true"),
		GPUNodeCount:          envIntOr("E2E_GPU_NODE_COUNT", 0),
		PodReadyTimeout:       envDurationOr("E2E_POD_READY_TIMEOUT", 2*time.Minute),
		PollInterval:          envDurationOr("E2E_POLL_INTERVAL", 2*time.Second),
		DaemonSetReadyTimeout: envDurationOr("E2E_DAEMONSET_READY_TIMEOUT", 3*time.Minute),
	}
}

func defaultKubeconfig() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

// Small local env helpers. Intentionally not shared with sharing-manager/common/env:
// that lives in the root module, and importing it would couple this separate e2e
// module to the operator's entire dependency graph just for a few wrappers.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
