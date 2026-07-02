package plugin

import (
	"context"
	"fmt"

	dto "github.com/prometheus/client_model/go"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/portforward"
)

// LabelSelector matches every gpu-sharing-plugin DaemonSet pod.
const LabelSelector = "app=gpu-sharing-plugin"

// ScrapeAll scrapes /metrics from every gpu-sharing-plugin pod, keyed by pod
// name. A workload pod under test can land on any GPU node, so attribution
// checks must search across all plugin pods rather than assume a single one
// (unlike the workload-independent health check in
// TestE2E_GPUSharingPluginMetricsEndpointHealthy, which only needs any one
// pod to be reachable).
func ScrapeAll(ctx context.Context, c *cluster.Client) (map[string]map[string]*dto.MetricFamily, error) {
	pluginPods, err := pods.ListByLabel(ctx, c, c.Config.PluginNamespace, LabelSelector)
	if err != nil {
		return nil, fmt.Errorf("list gpu-sharing-plugin pods: %w", err)
	}
	if len(pluginPods) == 0 {
		return nil, fmt.Errorf("no gpu-sharing-plugin pods found in namespace %s", c.Config.PluginNamespace)
	}

	result := make(map[string]map[string]*dto.MetricFamily, len(pluginPods))
	for _, pod := range pluginPods {
		fw, err := portforward.ToPod(c, pod.Namespace, pod.Name, 2112)
		if err != nil {
			return nil, fmt.Errorf("port-forward to %s/%s: %w", pod.Namespace, pod.Name, err)
		}
		families, err := metrics.Scrape(fw.LocalPort, "/metrics")
		fw.Close()
		if err != nil {
			return nil, fmt.Errorf("scrape metrics from %s/%s: %w", pod.Namespace, pod.Name, err)
		}
		result[pod.Name] = families
	}
	return result, nil
}
