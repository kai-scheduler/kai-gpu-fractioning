//go:build e2e

package tests

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/plugin"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_PodDeletionMidCollectionDoesNotBreakExporter is TC-9: deleting a
// tracked pod while scrapes are actively in flight must not panic or hang
// the exporter (exporter.go's pruneDeletedPods/deleteSeries mutate shared
// registry state under a mutex concurrently with scrapes — easy to get
// right today, easy to break in a future refactor), and the deleted pod's
// series must eventually disappear rather than leak forever.
func TestE2E_PodDeletionMidCollectionDoesNotBreakExporter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

	pod, err := workload.Apply(ctx, c, spec)
	if err != nil {
		t.Fatalf("create fractional pod: %v", err)
	}
	// Not t.Cleanup: this test deletes the pod itself as part of the
	// scenario under test; cleanup here would just be a redundant delete of
	// an already-gone pod, which workload.Delete tolerates but adds nothing.

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
