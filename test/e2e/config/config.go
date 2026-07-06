// Package config loads e2e run configuration entirely from the environment
// so the suite can point at whatever cluster the caller specifies (no config
// file). Cluster creation itself is handled by test/e2e/hack/create-cluster.py,
// not by this package or by Go test code.
package config

import (
	"os"
	"path/filepath"
	"time"
)

// Config is read entirely from the environment.
type Config struct {
	// Kubeconfig is the path to the kubeconfig used to reach the cluster.
	Kubeconfig string

	// PluginNamespace is where the gpu-sharing-plugin DaemonSet is deployed.
	PluginNamespace string

	// GPUNodeSelector selects nodes expected to advertise (fake) GPUs.
	GPUNodeSelector string

	// ExpectedGPUNodes, if > 0, asserts the exact count of nodes matching
	// GPUNodeSelector; 0 skips the assertion (and the node listing) entirely.
	// Set it to the E2E_GPU_WORKER_NODES value passed to create-cluster.py to catch
	// a misconfigured/partially-up cluster before any test runs.
	ExpectedGPUNodes int

	// PluginImage overrides the gpu-sharing-plugin image in the DaemonSet
	// manifest. Empty keeps whatever the manifest already specifies. Set this
	// (together with PluginImagePullPolicy) when running against a k3d
	// cluster with a locally built+loaded image.
	PluginImage string

	// PluginImagePullPolicy overrides the image pull policy when PluginImage
	// is set. Defaults to "Never" in that case (k3d-imported images have no
	// registry to pull from); the manifest's own "Always" is used otherwise.
	PluginImagePullPolicy string

	PodReadyTimeout       time.Duration
	DaemonSetReadyTimeout time.Duration
	PollInterval          time.Duration
}

// Load builds a Config from environment variables, applying defaults suited
// to a fake-gpu-operator cluster set up per create-cluster.py.
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
		ExpectedGPUNodes:      envIntOr("E2E_EXPECTED_GPU_NODES", 0),
		PluginImage:           pluginImage,
		PluginImagePullPolicy: pullPolicy,
		PodReadyTimeout:       envDurationOr("E2E_POD_READY_TIMEOUT", 2*time.Minute),
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
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
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
