//go:build e2e

package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/plugin"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_PodDeletionMidCollectionDoesNotBreakExporter verifies that deleting a
// tracked pod while scrapes are actively in flight must not panic or hang
// the exporter (exporter.go's pruneDeletedPods/deleteSeries mutate shared
// registry state under a mutex concurrently with scrapes — easy to get
// right today, easy to break in a future refactor), and the deleted pod's
// series must eventually disappear rather than leak forever.
func TestE2E_PodDeletionMidCollectionDoesNotBreakExporter(t *testing.T) {
	// Two DaemonSet rollouts (inside SetProcesses) plus a collection cycle and
	// the post-delete prune wait — give it well over the single-rollout budget,
	// matching the attribution test budget.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	c := s.Client

	before, err := pluginPodRestartCounts(ctx, c)
	if err != nil {
		t.Fatalf("get baseline plugin restart counts: %v", err)
	}

	spec := workload.FractionalPod{
		Namespace:           attributionTestNamespace,
		Name:                "tc9-delete-mid-collection-pod",
		ContainerName:       "trainer",
		GPUMemoryLimitMiB:   "2048",
		GPUMemoryRequestMiB: "2048",
	}

	// Remove any pod left behind by a previous failed run; workload.Delete is
	// a no-op on a non-existent pod.
	_ = workload.Delete(ctx, c, spec.Namespace, spec.Name)

	pod, err := workload.Apply(ctx, c, spec)
	if err != nil {
		t.Fatalf("create fractional pod: %v", err)
	}
	// Safety-net cleanup: the test deletes the pod itself at line ~107 as the
	// scenario under test, but if it fatals before reaching that point (e.g.
	// waitForSeries times out) this ensures the pod doesn't leak.
	t.Cleanup(func() {
		_ = workload.Delete(context.Background(), c, spec.Namespace, spec.Name)
	})

	// Make NVML report this pod's container as a GPU process so the plugin
	// exports a series for it — the deletion race can only be exercised once
	// there is a series to prune. Same PID-pinning dance as attribution_test.go.
	marker := workload.DefaultMarker(spec.Namespace, spec.Name)
	pid, err := nvmlmock.HostPID(ctx, c, pod.Spec.NodeName, marker)
	if err != nil {
		t.Fatalf("resolve host PID for %s/%s: %v", spec.Namespace, spec.Name, err)
	}
	procs := []nvmlmock.Proc{{UUID: nvmlmock.Device0UUID, PID: pid, UsedMemoryMiB: 8 * 1024}}
	if err := nvmlmock.SetProcesses(ctx, c, nvmlmock.A100, procs); err != nil {
		t.Fatalf("configure nvml-mock processes: %v", err)
	}
	t.Cleanup(func() {
		// Reset the mock to idle so a stale process entry (pointing at a PID that
		// no longer exists once the pod is gone) doesn't leak into later tests.
		if err := nvmlmock.SetProcesses(context.Background(), c, nvmlmock.A100, nil); err != nil {
			t.Errorf("reset nvml-mock to idle: %v", err)
		}
	})

	match := map[string]string{
		"namespace": spec.Namespace,
		"pod":       spec.Name,
		"pod_uid":   string(pod.UID),
	}
	if _, err := waitForSeries(ctx, c, memMetricName, match); err != nil {
		t.Fatalf("series never appeared before deletion: %v", err)
	}

	// Scrape continuously in the background while the delete is in flight,
	// so at least some scrapes race with the exporter observing/pruning the
	// deletion. Any scrape error (including a panic recovered by the test
	// binary as a crash) fails the test.
	stop := make(chan struct{})
	var scrapeErrs []error
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := plugin.ScrapeAll(ctx, c); err != nil {
				mu.Lock()
				scrapeErrs = append(scrapeErrs, err)
				mu.Unlock()
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	deleteErr := workload.Delete(ctx, c, spec.Namespace, spec.Name)
	close(stop)
	wg.Wait()

	if deleteErr != nil {
		t.Fatalf("delete pod mid-collection: %v", deleteErr)
	}
	mu.Lock()
	for _, err := range scrapeErrs {
		t.Errorf("scrape failed during deletion race: %v", err)
	}
	mu.Unlock()

	for _, metricName := range bothMetricNames {
		if err := waitForAbsence(ctx, c, metricName, match); err != nil {
			t.Errorf("%s: series for deleted pod never pruned: %v", metricName, err)
		}
	}

	after, err := pluginPodRestartCounts(ctx, c)
	if err != nil {
		t.Fatalf("get plugin restart counts after deletion race: %v", err)
	}
	for podName, count := range after {
		if count != before[podName] {
			t.Errorf("plugin pod %s restart count changed %d -> %d — deletion race likely crashed it",
				podName, before[podName], count)
		}
	}
}
