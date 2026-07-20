//go:build e2e

package metrics

import (
	"context"
	"testing"

	"github.com/kai-scheduler/gpu-sharing/test/e2e/k8s/pods"
	"github.com/kai-scheduler/gpu-sharing/test/e2e/k8s/portforward"
	"github.com/kai-scheduler/gpu-sharing/test/e2e/metrics"
)

// metricsd runs as a sidecar in the operator-managed sharingd DaemonSet pods,
// which the operator labels with component=sharingd (metricsd is a container in
// that pod, not its own workload). Its /metrics endpoint listens on 2112.
const (
	sharingdLabelSelector = "app.kubernetes.io/managed-by=gpu-sharing-operator,app.kubernetes.io/component=sharingd"
	metricsPort           = 2112
)

// TestE2E_GPUSharingPluginMetricsEndpointHealthy is a fast smoke test: the
// metricsd sidecar in the sharingd DaemonSet pods serves a valid Prometheus
// /metrics endpoint, independent of any test workload.
func TestE2E_GPUSharingPluginMetricsEndpointHealthy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsPresenceTimeout)
	defer cancel()

	client := s.Client
	sharingdPods, err := pods.ListByLabel(ctx, client, client.Config.OperatorNamespace, sharingdLabelSelector)
	if err != nil {
		t.Fatalf("list sharingd pods: %v", err)
	}
	if len(sharingdPods) == 0 {
		t.Fatalf("no sharingd pods found in namespace %s (is the operator deployed?)", client.Config.OperatorNamespace)
	}

	pod := sharingdPods[0]
	fw, err := portforward.ToPod(client, pod.Namespace, pod.Name, metricsPort)
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
