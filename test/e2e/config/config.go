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

	// PluginNamespace is where the gpu-sharing-plugin DaemonSet is deployed.
	PluginNamespace string

	// GPUNodeSelector selects nodes expected to advertise GPUs.
	GPUNodeSelector string

	// GPUNodeCount, if > 0, asserts the exact count of nodes matching
	// GPUNodeSelector; 0 skips the assertion (and the node listing) entirely.
	// Set it to the number of GPU nodes the target cluster is expected to have,
	// to catch a misconfigured/partially-up cluster before any test runs.
	GPUNodeCount int

	// PluginImage overrides the gpu-sharing-plugin image in the DaemonSet
	// manifest. Empty keeps whatever the manifest already specifies. Set this
	// (together with PluginImagePullPolicy) when running against a cluster with
	// a locally built+loaded image.
	PluginImage string

	// PluginImagePullPolicy overrides the image pull policy when PluginImage
	// is set. Defaults to "Never" in that case (locally loaded images have no
	// registry to pull from); the manifest's own "Always" is used otherwise.
	PluginImagePullPolicy string

	DaemonSetReadyTimeout time.Duration
	PollInterval          time.Duration
}

// Load builds a Config from environment variables.
func Load() Config {
	pluginImage := os.Getenv("E2E_PLUGIN_IMAGE")
	pullPolicy := os.Getenv("E2E_PLUGIN_IMAGE_PULL_POLICY")
	if pluginImage != "" && pullPolicy == "" {
		pullPolicy = "Never"
	}

	return Config{
		Kubeconfig:            envOr("E2E_KUBECONFIG", defaultKubeconfig()),
		PluginNamespace:       envOr("E2E_PLUGIN_NAMESPACE", "gpu-sharing"),
		GPUNodeSelector:       envOr("E2E_GPU_NODE_SELECTOR", "nvidia.com/gpu.present=true"),
		GPUNodeCount:          envIntOr("E2E_GPU_NODE_COUNT", 0),
		PluginImage:           pluginImage,
		PluginImagePullPolicy: pullPolicy,
		DaemonSetReadyTimeout: envDurationOr("E2E_DAEMONSET_READY_TIMEOUT", 3*time.Minute),
		PollInterval:          envDurationOr("E2E_POLL_INTERVAL", 2*time.Second),
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
