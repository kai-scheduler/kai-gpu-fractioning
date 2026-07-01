//go:build e2e

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/portforward"
)

const pluginLabelSelector = "app=gpu-sharing-plugin"

// TestE2E_GPUSharingPluginMetricsEndpointHealthy is a fast smoke test: the
// gpu-sharing-plugin DaemonSet pods serve a valid Prometheus /metrics
// endpoint, independent of any test workload.
func TestE2E_GPUSharingPluginMetricsEndpointHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := s.Client
	pluginPods, err := pods.ListByLabel(ctx, client, client.Config.PluginNamespace, pluginLabelSelector)
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

	families, err := metrics.Scrape(fw.LocalPort, "/metrics")
	if err != nil {
		t.Fatalf("scrape metrics from %s/%s: %v", pod.Namespace, pod.Name, err)
	}
	if len(families) == 0 {
		t.Fatalf("expected at least one metric family from %s/%s", pod.Namespace, pod.Name)
	}
}
