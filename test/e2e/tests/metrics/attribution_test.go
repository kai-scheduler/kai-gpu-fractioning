//go:build e2e

package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_SingleFractionalPodAttribution verifies that a pod carrying a fractional GPU
// annotation is attributed under the namespace/pod/pod_uid it actually has, on
// the GPU whose UUID we pinned in nvml-mock.
//
// Flow: create the annotated pod, resolve its container's host PID, tell
// nvml-mock that PID is a GPU process on a known device UUID, then assert the
// plugin attributes the pod's memory and SM-utilization series to it. See the
// NOTE at the assertion below on why the exact memory value isn't asserted.
func TestE2E_SingleFractionalPodAttribution(t *testing.T) {
	if !s.Client.Config.NVMLMock {
		t.Skip(skipNoNVMLMock)
	}
	// Two nvml-mock + plugin DaemonSet rollouts (inside SetProcesses) plus a
	// collection cycle — give it well over the single-rollout budget.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	c := s.Client

	gpuNodes, err := nodes.ListGPUNodes(ctx, c)
	if err != nil {
		t.Fatalf("list GPU nodes: %v", err)
	}
	if len(gpuNodes) == 0 {
		t.Fatalf("no GPU nodes found matching selector %q", c.Config.GPUNodeSelector)
	}
	targetNode := gpuNodes[0].Name

	spec := workload.FractionalPod{
		Namespace:           attributionTestNamespace,
		Name:                "tc1-single-fractional-pod",
		ContainerName:       "trainer",
		GPUMemoryLimitMiB:   "2048",
		GPUMemoryRequestMiB: "2048",
		NodeSelector:        map[string]string{"kubernetes.io/hostname": targetNode},
	}

	_ = workload.Delete(ctx, c, spec.Namespace, spec.Name)
	pod, err := workload.Apply(ctx, c, spec)
	if err != nil {
		t.Fatalf("create fractional pod: %v", err)
	}
	t.Cleanup(func() {
		if err := workload.Delete(context.Background(), c, spec.Namespace, spec.Name); err != nil {
			t.Errorf("cleanup pod %s/%s: %v", spec.Namespace, spec.Name, err)
		}
	})

	// Resolve the container's host-namespace PID and make NVML report it as a
	// GPU process using wantBytes of memory on gpuUUID.
	marker := workload.DefaultMarker(spec.Namespace, spec.Name)
	pid, err := nvmlmock.HostPID(ctx, c, targetNode, marker)
	if err != nil {
		t.Fatalf("resolve host PID for %s/%s: %v", spec.Namespace, spec.Name, err)
	}
	t.Logf("resolved host PID for %s/%s on node %s: pid=%d", spec.Namespace, spec.Name, targetNode, pid)

	const (
		gpuUUID       = nvmlmock.Device0UUID
		wantMemoryMiB = uint64(8 * 1024) // 8 GiB in MiB
		wantBytes     = wantMemoryMiB * 1024 * 1024
	)
	procs := []nvmlmock.Proc{{UUID: gpuUUID, PID: pid, UsedMemoryMiB: wantMemoryMiB}}
	t.Logf("configuring nvml-mock: pid=%d gpu_uuid=%s used_memory_mib=%d", pid, gpuUUID, wantMemoryMiB)
	if err := setProcesses(ctx, c, nvmlmock.A100, procs); err != nil {
		t.Fatalf("configure nvml-mock processes: %v", err)
	}
	t.Log("nvml-mock configured and metricsd restarted; scraping initial metrics state:")
	debugScrapeAll(ctx, t, c)
	t.Cleanup(func() {
		// Reset the mock to idle so a stale process entry (pointing at a PID that
		// no longer exists once the pod is gone) doesn't leak into later tests.
		if err := nvmlmock.SetProcesses(context.Background(), c, nvmlmock.A100, nil); err != nil {
			t.Errorf("reset nvml-mock to idle: %v", err)
		}
	})

	matchLabels := map[string]string{
		"namespace": spec.Namespace,
		"pod":       spec.Name,
		"pod_uid":   string(pod.UID),
		"gpu_uuid":  gpuUUID,
	}
	t.Logf("waiting for series %s with labels %v", memMetricName, matchLabels)

	// Assert the pod is attributed on the GPU whose UUID we pinned in nvml-mock,
	// and that the reported memory matches what we configured (used_memory_mib →
	// GetComputeRunningProcesses returns MiB*1024*1024 bytes).
	// waitForSeriesVerbose logs every series found for this metric on each poll
	// so label mismatches are visible during the wait, not only on timeout.
	m, err := waitForSeriesVerbose(ctx, t, c, memMetricName, matchLabels)
	if err != nil {
		t.Logf("series not found; dumping current metrics state for diagnosis:")
		debugScrapeAll(ctx, t, c)
		t.Fatalf("%s: %v", memMetricName, err)
	}
	if got := uint64(m.GetGauge().GetValue()); got != wantBytes {
		t.Errorf("%s: want %d bytes, got %d", memMetricName, wantBytes, got)
	}

	// The same attributed process also produces the SM-utilization series for
	// this pod/GPU. Assert co-attribution (presence); the per-process util value
	// isn't controlled here, so we don't assert a specific number.
	if _, err := waitForSeries(ctx, c, smMetricName, matchLabels); err != nil {
		t.Errorf("%s: %v", smMetricName, err)
	}
}
