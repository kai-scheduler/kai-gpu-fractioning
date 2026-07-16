//go:build e2e

package metrics

import (
	"context"
	"fmt"
	"strings"
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

// waitForSeriesVerbose is like waitForSeries but logs, on every poll, ALL
// series found for metricName across every pod (regardless of label match).
// This surfaces label mismatches and attribution gaps while waiting, rather
// than only revealing them on timeout via debugScrapeAll.
func waitForSeriesVerbose(ctx context.Context, t *testing.T, c *cluster.Client, metricName string, match map[string]string) (*dto.Metric, error) {
	t.Helper()
	var found *dto.Metric

	err := waiter.PollUntil(ctx, c.Config.PodReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("metric series %s matching %v", metricName, match),
		func(ctx context.Context) (bool, error) {
			allFamilies, err := plugin.ScrapeFrom(ctx, c, s.PluginPods)
			if err != nil {
				t.Logf("[poll] ScrapeAll error: %v", err)
				return false, err
			}

			var anyForMetric bool
			for podName, families := range allFamilies {
				fam, ok := families[metricName]
				if !ok {
					continue
				}
				for _, m := range fam.GetMetric() {
					anyForMetric = true
					var lbls []string
					for _, lp := range m.GetLabel() {
						lbls = append(lbls, lp.GetName()+"="+lp.GetValue())
					}
					t.Logf("[poll] pod %s: %s{%s}=%v", podName, metricName, strings.Join(lbls, ","), m.GetGauge().GetValue())
				}
			}
			if !anyForMetric {
				t.Logf("[poll] no %s series found across all pods", metricName)
			}

			var matched []*dto.Metric
			for _, families := range allFamilies {
				matched = append(matched, metrics.FindSeries(families, metricName, match)...)
			}
			if len(matched) > 0 {
				found = matched[0]
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

func findSeriesAcrossPluginPods(ctx context.Context, c *cluster.Client, metricName string, match map[string]string) ([]*dto.Metric, error) {
	allFamilies, err := plugin.ScrapeFrom(ctx, c, s.PluginPods)
	if err != nil {
		return nil, err
	}
	var out []*dto.Metric
	for _, families := range allFamilies {
		out = append(out, metrics.FindSeries(families, metricName, match)...)
	}
	return out, nil
}

// debugScrapeAll scrapes every metricsd pod and logs all gpu_sharing_ metric
// families and their label sets. Call after SetProcesses to diagnose attribution
// failures: the output shows whether metricsd is reachable, whether it has any
// gpu_sharing_ series at all, and what labels those series carry.
func debugScrapeAll(ctx context.Context, t *testing.T, c *cluster.Client) {
	t.Helper()
	allFamilies, err := plugin.ScrapeAll(ctx, c)
	if err != nil {
		t.Logf("[debug] ScrapeAll error: %v", err)
		return
	}
	if len(allFamilies) == 0 {
		t.Logf("[debug] ScrapeAll returned 0 pods")
		return
	}
	for podName, families := range allFamilies {
		totalSeries := 0
		for _, fam := range families {
			totalSeries += len(fam.GetMetric())
		}
		var gpuFamilies []string
		for name, fam := range families {
			if !strings.HasPrefix(name, "gpu_sharing") {
				continue
			}
			for _, m := range fam.GetMetric() {
				var labels []string
				for _, lp := range m.GetLabel() {
					labels = append(labels, lp.GetName()+"="+lp.GetValue())
				}
				gpuFamilies = append(gpuFamilies, fmt.Sprintf("  %s{%s}=%v", name, strings.Join(labels, ","), m.GetGauge().GetValue()))
			}
		}
		if len(gpuFamilies) == 0 {
			t.Logf("[debug] pod %s: no gpu_sharing_ series (total series in /metrics: %d)", podName, totalSeries)
		} else {
			t.Logf("[debug] pod %s: %d gpu_sharing_ series (total: %d)", podName, len(gpuFamilies), totalSeries)
			for _, s := range gpuFamilies {
				t.Log(s)
			}
		}
	}
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
