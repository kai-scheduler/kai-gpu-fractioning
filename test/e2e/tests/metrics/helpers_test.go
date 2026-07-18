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
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/plugin"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/waiter"
)

// attributionTestNamespace hosts every workload pod these tests create —
// kept separate from any Run:ai project namespace so the tests don't depend
// on scaffolding this e2e cluster may not have.
const attributionTestNamespace = "metricsd-e2e"

const skipNoNVMLMock = "requires nvml-mock (set E2E_NVML_MOCK=1)"

const (
	memMetricName  = "gpu_sharing_gpu_memory_used_bytes"
	smMetricName   = "gpu_sharing_gpu_sm_utilization_percent"
	normMetricName = "gpu_sharing_gpu_sm_utilization_percent_normalized"
)

// allMetricNames is the full set of per-pod series every test must cover —
// mem, raw SM, and normalized SM. All three share the same label set and
// lifecycle: they are emitted and pruned together by the exporter.
var allMetricNames = []string{memMetricName, smMetricName, normMetricName}

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
			if ctx.Err() != nil {
				return
			}
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

// scrapeFreshFamilies scrapes /metrics from the cached sharingd pods. If all
// cached pods are unreachable (empty result — e.g. pods were replaced by a
// nvmlmock.SetProcesses restart and s.PluginPods is stale), it re-lists pods
// once, updates s.PluginPods for subsequent polls, and retries. This keeps
// the happy-path poll-loop free of ListByLabel calls while self-healing after
// pod restarts without overloading the API server with per-poll list requests.
func scrapeFreshFamilies(ctx context.Context, c *cluster.Client) (map[string]map[string]*dto.MetricFamily, error) {
	allFamilies, err := plugin.ScrapeFrom(ctx, c, s.PluginPods)
	if err != nil {
		return nil, err
	}
	// len == 0 means every cached pod's port-forward failed: the list is stale.
	// Re-list once and update the cache so subsequent polls skip this branch.
	if len(allFamilies) == 0 && len(s.PluginPods) > 0 {
		freshPods, listErr := pods.ListByLabel(ctx, c, c.Config.OperatorNamespace, plugin.LabelSelector)
		if listErr == nil && len(freshPods) > 0 {
			s.PluginPods = freshPods
			allFamilies, err = plugin.ScrapeFrom(ctx, c, freshPods)
			if err != nil {
				return nil, err
			}
		}
	}
	return allFamilies, nil
}

func findSeriesAcrossPluginPods(ctx context.Context, c *cluster.Client, metricName string, match map[string]string) ([]*dto.Metric, error) {
	allFamilies, err := scrapeFreshFamilies(ctx, c)
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

// setProcesses wraps nvmlmock.SetProcesses and immediately refreshes
// s.PluginPods to the current sharingd pods after the restart. Without this,
// s.PluginPods still points to the terminating pre-restart pods. In CI those
// pods stay reachable long enough that scrapeFromPods returns non-empty results
// (bypassing the automatic re-list) and every poll sees 0 gpu_sharing_ series
// until the old pods finally terminate. Cleanup calls that use
// context.Background() should call nvmlmock.SetProcesses directly — they run
// after assertions and do not need a fresh pod cache.
func setProcesses(ctx context.Context, c *cluster.Client, gpu string, procs []nvmlmock.Proc) error {
	if err := nvmlmock.SetProcesses(ctx, c, gpu, procs); err != nil {
		return err
	}
	freshPods, err := pods.ListByLabel(ctx, c, c.Config.OperatorNamespace, plugin.LabelSelector)
	if err != nil {
		return fmt.Errorf("list sharingd pods after restart: %w", err)
	}
	s.PluginPods = freshPods
	return nil
}
