//go:build e2e

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_SingleFractionalPodAttribution is TC-1 from
// test/e2e/docs/metricsd-e2e-test-plan.md: a pod carrying a fractional GPU
// annotation is attributed under the namespace/pod/pod_uid it actually has, on
// the GPU whose UUID we pinned in nvml-mock — and the exported memory gauge
// equals the value we made NVML report for that pod's process.
//
// Flow: create the annotated pod, resolve its container's host PID, tell
// nvml-mock that PID is a GPU process using wantBytes on a known device UUID,
// then assert the plugin attributes exactly that to the pod's series.
func TestE2E_SingleFractionalPodAttribution(t *testing.T) {
	// Two nvml-mock + plugin DaemonSet rollouts (inside SetProcesses) plus a
	// collection cycle — give it well over the single-rollout budget.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	c := s.Client
	spec := workload.FractionalPod{
		Namespace:           attributionTestNamespace,
		Name:                "tc1-single-fractional-pod",
		ContainerName:       "trainer",
		GPUMemoryLimitMiB:   "2048",
		GPUMemoryRequestMiB: "2048",
	}

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
	pid, err := nvmlmock.HostPID(ctx, c, pod.Spec.NodeName, marker)
	if err != nil {
		t.Fatalf("resolve host PID for %s/%s: %v", spec.Namespace, spec.Name, err)
	}

	const (
		gpuUUID   = nvmlmock.Device0UUID
		wantBytes = 8 * 1024 * 1024 * 1024 // 8 GiB
	)
	procs := []nvmlmock.Proc{{UUID: gpuUUID, PID: pid, UsedGPUMemory: wantBytes}}
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

	matchLabels := map[string]string{
		"namespace": spec.Namespace,
		"pod":       spec.Name,
		"pod_uid":   string(pod.UID),
		"gpu_uuid":  gpuUUID,
	}

	// Memory is the value we control end-to-end: assert it exactly.
	memSeries, err := waitForSeries(ctx, c, memMetricName, matchLabels)
	if err != nil {
		t.Fatalf("%s: %v", memMetricName, err)
	}
	if got := metrics.GaugeValue(memSeries); got != wantBytes {
		t.Errorf("%s = %v, want %v (the memory we pinned in nvml-mock)", memMetricName, got, wantBytes)
	}

	// The same attributed process also produces the SM-utilization series for
	// this pod/GPU. Assert co-attribution (presence); the per-process util value
	// isn't controlled here, so we don't assert a specific number.
	if _, err := waitForSeries(ctx, c, smMetricName, matchLabels); err != nil {
		t.Errorf("%s: %v", smMetricName, err)
	}
}
