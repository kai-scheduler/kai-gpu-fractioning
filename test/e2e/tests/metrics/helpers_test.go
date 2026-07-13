//go:build e2e

package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/plugin"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/waiter"
)

// attributionTestNamespace hosts every workload pod these tests create —
// kept separate from any Run:ai project namespace so the tests don't depend
// on scaffolding this e2e cluster may not have.
const attributionTestNamespace = "metricsd-e2e"

const (
	memMetricName = "gpu_sharing_gpu_memory_used_bytes"
	smMetricName  = "gpu_sharing_gpu_sm_utilization_percent"
)

// bothMetricNames is the pair every attribution/exclusion test case checks —
// a pod is expected to appear (or not appear) on both, never just one.
var bothMetricNames = []string{memMetricName, smMetricName}

// waitForSeries polls every gpu-sharing-plugin pod's /metrics until a series
// for metricName matches every label in match, or times out.
func waitForSeries(ctx context.Context, c *cluster.Client, metricName string, match map[string]string) (*dto.Metric, error) {
	var found *dto.Metric

	err := waiter.PollUntil(ctx, c.Config.PodReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("metric series %s matching %v", metricName, match),
		func(ctx context.Context) (bool, error) {
			series, err := findSeriesAcrossPluginPods(ctx, c, metricName, match)
			if err != nil {
				return false, err
			}
			if len(series) > 0 {
				found = series[0]
				return true, nil
			}
			return false, nil
		})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// waitForAbsence polls until no gpu-sharing-plugin pod reports a series for
// metricName matching every label in match, or times out. The inverse of
// waitForSeries — used to confirm a deleted pod's series is eventually
// pruned (exporter.go's pruneDeletedPods).
func waitForAbsence(ctx context.Context, c *cluster.Client, metricName string, match map[string]string) error {
	return waiter.PollUntil(ctx, c.Config.PodReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("metric series %s matching %v to disappear", metricName, match),
		func(ctx context.Context) (bool, error) {
			series, err := findSeriesAcrossPluginPods(ctx, c, metricName, match)
			if err != nil {
				return false, err
			}
			return len(series) == 0, nil
		})
}

// assertNeverAppears scrapes repeatedly over window and fails the test the
// instant a series for metricName matches every label in match. Used for
// negative cases (full-GPU and malformed-annotation exclusion) where the expected state is deterministic and
// immediate (the adapter rejects/accepts a container synchronously at
// create time — there's no eventual-consistency to wait out), so this
// confirms the negative holds rather than just checking once, which could
// pass by scraping before the plugin has processed the container event at
// all.
func assertNeverAppears(ctx context.Context, t *testing.T, c *cluster.Client, metricName string, match map[string]string, window, interval time.Duration) {
	t.Helper()

	deadline := time.Now().Add(window)
	for {
		series, err := findSeriesAcrossPluginPods(ctx, c, metricName, match)
		if err != nil {
			t.Fatalf("scrape plugin pods: %v", err)
		}
		if len(series) > 0 {
			t.Fatalf("%s: found unexpected series matching %v: %v", metricName, match, series[0])
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func findSeriesAcrossPluginPods(ctx context.Context, c *cluster.Client, metricName string, match map[string]string) ([]*dto.Metric, error) {
	allFamilies, err := plugin.ScrapeAll(ctx, c)
	if err != nil {
		return nil, err
	}
	var out []*dto.Metric
	for _, families := range allFamilies {
		out = append(out, metrics.FindSeries(families, metricName, match)...)
	}
	return out, nil
}

// pluginPodRestartCounts returns each gpu-sharing-plugin pod's total
// container restart count, keyed by pod name. A crash (e.g. a panic from a
// malformed annotation or a delete-during-scrape race) shows up as an
// increase here — a more reliable resilience signal than "the test didn't
// time out," since a crashed pod still gets recreated and can pass a
// later scrape once it's back up.
func pluginPodRestartCounts(ctx context.Context, c *cluster.Client) (map[string]int32, error) {
	pluginPods, err := pods.ListByLabel(ctx, c, c.Config.OperatorNamespace, plugin.LabelSelector)
	if err != nil {
		return nil, fmt.Errorf("list gpu-sharing-plugin pods: %w", err)
	}

	counts := make(map[string]int32, len(pluginPods))
	for _, pod := range pluginPods {
		var total int32
		for _, cs := range pod.Status.ContainerStatuses {
			total += cs.RestartCount
		}
		counts[pod.Name] = total
	}
	return counts, nil
}
