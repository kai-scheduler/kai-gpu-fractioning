//go:build e2e

package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

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
			allFamilies, err := scrapeFreshFamilies(ctx, c)
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
			t.Logf("[debug] pod %s: no gpu_sharing_ series (%d metric families, %d total data points in /metrics)", podName, len(families), totalSeries)
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

// debugClusterState runs all cluster diagnostic helpers when a metric-series
// poll times out. Call it from the test failure path to capture in the CI log:
// sharingd + metricsd container logs (NRI Synchronize events, fsstore snapshot
// counts), nvml-mock ConfigMap content, fsstore file listing on the workload
// node, and pod container states. nodeName is the k3d worker node where the
// workload ran; pass "" to skip the per-node fsstore listing.
func debugClusterState(ctx context.Context, t *testing.T, c *cluster.Client, nodeName string) {
	t.Helper()
	debugPodStates(ctx, t, c)
	debugConfigMap(ctx, t, c)
	if nodeName != "" {
		debugFSStore(ctx, t, c, nodeName)
	}
	debugSharingdLogs(ctx, t, c)
	debugMetricsdLogs(ctx, t, c)
}

// debugPodStates logs each sharingd pod's phase, node, and per-container state.
func debugPodStates(ctx context.Context, t *testing.T, c *cluster.Client) {
	t.Helper()
	sharingdPods, err := pods.ListByLabel(ctx, c, c.Config.OperatorNamespace, plugin.LabelSelector)
	if err != nil {
		t.Logf("[debug] list sharingd pods: %v", err)
		return
	}
	for _, pod := range sharingdPods {
		t.Logf("[debug] pod %s node=%s phase=%s", pod.Name, pod.Spec.NodeName, pod.Status.Phase)
		for _, cs := range pod.Status.ContainerStatuses {
			stateStr := containerStateString(cs)
			t.Logf("[debug]   container %s: ready=%v restarts=%d %s", cs.Name, cs.Ready, cs.RestartCount, stateStr)
		}
	}
}

func containerStateString(cs corev1.ContainerStatus) string {
	switch {
	case cs.State.Running != nil:
		return "Running(since=" + cs.State.Running.StartedAt.Format(time.RFC3339) + ")"
	case cs.State.Waiting != nil:
		return "Waiting(reason=" + cs.State.Waiting.Reason + " msg=" + cs.State.Waiting.Message + ")"
	case cs.State.Terminated != nil:
		return fmt.Sprintf("Terminated(reason=%s exit=%d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
	default:
		return "Unknown"
	}
}

// debugConfigMap logs the nvml-mock-config ConfigMap that SetProcesses writes
// and metricsd mounts as MOCK_NVML_CONFIG. Verifies the right process list
// (PID, UUID, memory) was applied before metricsd restarted.
func debugConfigMap(ctx context.Context, t *testing.T, c *cluster.Client) {
	t.Helper()
	var cm corev1.ConfigMap
	key := ctrlclient.ObjectKey{Namespace: nvmlmock.MetricsdNamespace, Name: nvmlmock.MetricsdConfigMapName}
	if err := c.Ctrl.Get(ctx, key, &cm); err != nil {
		t.Logf("[debug] get configmap %s/%s: %v", key.Namespace, key.Name, err)
		return
	}
	t.Logf("[debug] configmap %s/%s:\n%s", key.Namespace, key.Name, cm.Data["config.yaml"])
}

// debugFSStore execs into the nvml-mock pod on nodeName (hostPID=true, so
// /proc/1/root is the k3d node's root filesystem) and lists the fsstore
// directory sharingd writes containerID.json files into. An absent directory or
// empty listing means the hostPath volume did not preserve the mapping files
// across the sharingd pod restart — indicating the new sharingd never learned
// about pre-existing workload containers.
func debugFSStore(ctx context.Context, t *testing.T, c *cluster.Client, nodeName string) {
	t.Helper()
	mockPods, err := pods.ListByLabel(ctx, c, nvmlmock.MockNamespace, "app=nvml-mock")
	if err != nil {
		t.Logf("[debug] list nvml-mock pods: %v", err)
		return
	}
	var podName string
	for _, p := range mockPods {
		if p.Spec.NodeName == nodeName {
			podName = p.Name
			break
		}
	}
	if podName == "" {
		t.Logf("[debug] no nvml-mock pod found on node %s", nodeName)
		return
	}
	// /proc/1/root gives us the k3d node container's root filesystem from
	// inside a hostPID pod, surfacing the hostPath directory independent of
	// whether the current sharingd pod's volume mount is configured correctly.
	out, err := pods.Exec(ctx, c, nvmlmock.MockNamespace, podName, nvmlmock.Container,
		[]string{"sh", "-c", "ls -la /proc/1/root/var/run/gpu-sharing/map/ 2>&1 || echo '(directory not found)'"})
	if err != nil {
		t.Logf("[debug] fsstore listing on node %s via %s: %v", nodeName, podName, err)
		return
	}
	t.Logf("[debug] fsstore on node %s (via %s):\n%s", nodeName, podName, out)
}

// debugSharingdLogs fetches the last 100 lines of the sharingd container from
// each sharingd pod. With logLevel=debug the logs include NRI Synchronize
// entries showing how many containers were replayed and how many were recorded
// (only GPU-sharing containers get a JSON file written).
func debugSharingdLogs(ctx context.Context, t *testing.T, c *cluster.Client) {
	t.Helper()
	debugContainerLogs(ctx, t, c, c.Config.OperatorNamespace, plugin.LabelSelector, "sharingd")
}

// debugMetricsdLogs fetches the last 100 lines of the metricsd container from
// each sharingd pod. With logLevel=debug the logs include "collect: fsstore
// snapshot, activeContainers=N" showing whether the engine sees any attributed
// containers on each collection cycle.
func debugMetricsdLogs(ctx context.Context, t *testing.T, c *cluster.Client) {
	t.Helper()
	debugContainerLogs(ctx, t, c, c.Config.OperatorNamespace, plugin.LabelSelector, "metricsd")
}

// debugContainerLogs fetches the last 100 log lines from container in each pod
// matching labelSelector in namespace, and logs them via t.Logf.
func debugContainerLogs(ctx context.Context, t *testing.T, c *cluster.Client, namespace, labelSelector, container string) {
	t.Helper()
	podList, err := pods.ListByLabel(ctx, c, namespace, labelSelector)
	if err != nil {
		t.Logf("[debug] list pods (%s) for %s logs: %v", labelSelector, container, err)
		return
	}
	for _, pod := range podList {
		data, err := c.RESTClient().Get().
			Namespace(pod.Namespace).
			Resource("pods").
			Name(pod.Name).
			SubResource("log").
			Param("container", container).
			Param("tailLines", "100").
			DoRaw(ctx)
		if err != nil {
			t.Logf("[debug] get %s logs from pod %s: %v", container, pod.Name, err)
			continue
		}
		t.Logf("[debug] %s logs from pod %s (tail 100):\n%s", container, pod.Name, string(data))
	}
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
