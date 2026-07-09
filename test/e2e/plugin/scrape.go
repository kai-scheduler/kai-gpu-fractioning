package plugin

import (
	"context"
	"fmt"

	dto "github.com/prometheus/client_model/go"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/portforward"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
)

// LabelSelector matches every sharingd DaemonSet pod — the metricsd metrics
// exporter runs as a sidecar container in those pods (not its own DaemonSet),
// so the /metrics endpoint under test is served from here.
const LabelSelector = "app.kubernetes.io/managed-by=gpu-sharing-operator,app.kubernetes.io/component=sharingd"

// metricsPort is the Prometheus port the metricsd sidecar serves on.
const metricsPort = 2112

// ScrapeAll scrapes /metrics from every sharingd pod (metricsd sidecar), keyed
// by pod name. A workload pod under test can land on any GPU node, so
// attribution checks must search across all sharingd pods rather than assume a
// single one (unlike the workload-independent health check in
// TestE2E_GPUSharingPluginMetricsEndpointHealthy, which only needs any one
// pod to be reachable).
func ScrapeAll(ctx context.Context, c *cluster.Client) (map[string]map[string]*dto.MetricFamily, error) {
	sharingdPods, err := pods.ListByLabel(ctx, c, c.Config.OperatorNamespace, LabelSelector)
	if err != nil {
		return nil, fmt.Errorf("list sharingd pods: %w", err)
	}
	if len(sharingdPods) == 0 {
		return nil, fmt.Errorf("no sharingd pods found in namespace %s", c.Config.OperatorNamespace)
	}

	result := make(map[string]map[string]*dto.MetricFamily, len(sharingdPods))
	for _, pod := range sharingdPods {
		fw, err := portforward.ToPod(c, pod.Namespace, pod.Name, metricsPort)
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
