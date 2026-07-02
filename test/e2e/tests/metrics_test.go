//go:build e2e

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/plugin"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/portforward"
)

// TestE2E_GPUSharingPluginMetricsEndpointHealthy is a fast smoke test: the
// gpu-sharing-plugin DaemonSet pods serve a valid Prometheus /metrics
// endpoint, independent of any test workload.
func TestE2E_GPUSharingPluginMetricsEndpointHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := s.Client
	pluginPods, err := pods.ListByLabel(ctx, client, client.Config.PluginNamespace, plugin.LabelSelector)
	if err != nil {
		t.Fatalf("list gpu-sharing-plugin pods: %v", err)
	}
	if len(pluginPods) == 0 {
		t.Fatalf("no gpu-sharing-plugin pods found in namespace %s", client.Config.PluginNamespace)
	}

	pod := pluginPods[0]
	fw, err := portforward.ToPod(client, pod.Namespace, pod.Name, 2112)
	if err != nil {
		t.Fatalf("port-forward to %s/%s: %v", pod.Namespace, pod.Name, err)
	}
	defer fw.Close()

	// Scrape itself already validates a 200 status, a text/plain Content-Type,
	// and a parseable body. It's the health signal this smoke test cares
	// about — the GPU gauges only emit samples once a GPU-attributed pod has
	// been observed, so an empty family set here (no GPU workload running)
	// doesn't mean the endpoint is unhealthy.
	if _, err := metrics.Scrape(fw.LocalPort, "/metrics"); err != nil {
		t.Fatalf("scrape metrics from %s/%s: %v", pod.Namespace, pod.Name, err)
	}
}
