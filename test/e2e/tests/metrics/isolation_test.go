//go:build e2e

package metrics

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_MultipleGPUsOnOneNodeAreIsolated verifies that two fractional pods
// placed on different physical GPUs on the same node each carry their own
// gpu_uuid / gpu_index label — no series for pod A must ever appear under
// pod B's device, and vice versa.
//
// This is the inverse of TestE2E_TwoFractionalPodsShareOneGPU (which tests
// co-location on the same device): this test proves the plugin does not
// conflate different physical devices.
//
// The test requires GPUCountPerNode >= 2 (set E2E_GPU_COUNT_PER_NODE=2 for
// nvml-mock clusters, which always expose Device0 and Device1). It skips
// rather than fabricates topology when only one GPU is available.
//
// In the nvml-mock environment there is no device plugin, so GPU assignment
// is explicit: pidA is pinned to Device0UUID and pidB to Device1UUID via
// nvmlmock.SetProcesses. The suite then asserts:
//
//  1. Positive: pod-a's series carries gpu_uuid=Device0UUID; pod-b's carries
//     gpu_uuid=Device1UUID (with the expected SMUtil values).
//  2. Negative: pod-a's pod_uid never appears under Device1UUID; pod-b's
//     pod_uid never appears under Device0UUID.
func TestE2E_MultipleGPUsOnOneNodeAreIsolated(t *testing.T) {
	if s.Client.Config.GPUCountPerNode < 2 {
		t.Skipf("requires GPUCountPerNode >= 2, have %d (set E2E_GPU_COUNT_PER_NODE=2 for nvml-mock clusters)",
			s.Client.Config.GPUCountPerNode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	c := s.Client

	gpuNodes, err := nodes.ListGPUNodes(ctx, c)
	if err != nil {
		t.Fatalf("list GPU nodes: %v", err)
	}
	if len(gpuNodes) == 0 {
		t.Fatalf("no GPU nodes found matching selector %q (is the cluster up?)", c.Config.GPUNodeSelector)
	}
	targetNode := gpuNodes[0].Name

	halfMemMiB := strconv.Itoa(c.Config.GPUMemoryMiB / 2)
	nodeSel := map[string]string{"kubernetes.io/hostname": targetNode}

	specA := workload.FractionalPod{
		Namespace:           attributionTestNamespace,
		Name:                "tc3-pod-a",
		ContainerName:       "trainer",
		GPUMemoryLimitMiB:   halfMemMiB,
		GPUMemoryRequestMiB: halfMemMiB,
		NodeSelector:        nodeSel,
	}
	specB := workload.FractionalPod{
		Namespace:           attributionTestNamespace,
		Name:                "tc3-pod-b",
		ContainerName:       "trainer",
		GPUMemoryLimitMiB:   halfMemMiB,
		GPUMemoryRequestMiB: halfMemMiB,
		NodeSelector:        nodeSel,
	}

	podA, err := workload.Apply(ctx, c, specA)
	if err != nil {
		t.Fatalf("create pod-a: %v", err)
	}
	t.Cleanup(func() {
		if err := workload.Delete(context.Background(), c, specA.Namespace, specA.Name); err != nil {
			t.Errorf("cleanup pod-a: %v", err)
		}
	})

	podB, err := workload.Apply(ctx, c, specB)
	if err != nil {
		t.Fatalf("create pod-b: %v", err)
	}
	t.Cleanup(func() {
		if err := workload.Delete(context.Background(), c, specB.Namespace, specB.Name); err != nil {
			t.Errorf("cleanup pod-b: %v", err)
		}
	})

	markerA := workload.DefaultMarker(specA.Namespace, specA.Name)
	pidA, err := nvmlmock.HostPID(ctx, c, targetNode, markerA)
	if err != nil {
		t.Fatalf("resolve host PID for pod-a: %v", err)
	}

	markerB := workload.DefaultMarker(specB.Namespace, specB.Name)
	pidB, err := nvmlmock.HostPID(ctx, c, targetNode, markerB)
	if err != nil {
		t.Fatalf("resolve host PID for pod-b: %v", err)
	}

	// Pin each pod to a distinct device: pod-a on Device0, pod-b on Device1.
	// This is the deterministic multi-device assignment in nvml-mock — in a
	// real cluster the device plugin would assign different physical GPUs.
	procs := []nvmlmock.Proc{
		{UUID: nvmlmock.Device0UUID, PID: pidA, UsedGPUMemory: uint64(c.Config.GPUMemoryMiB/2) * 1024 * 1024, SMUtil: 70},
		{UUID: nvmlmock.Device1UUID, PID: pidB, UsedGPUMemory: uint64(c.Config.GPUMemoryMiB/2) * 1024 * 1024, SMUtil: 30},
	}
	if err := nvmlmock.SetProcesses(ctx, c, nvmlmock.A100, procs); err != nil {
		t.Fatalf("configure nvml-mock processes: %v", err)
	}
	t.Cleanup(func() {
		if err := nvmlmock.SetProcesses(context.Background(), c, nvmlmock.A100, nil); err != nil {
			t.Errorf("reset nvml-mock to idle: %v", err)
		}
	})

	// matchA / matchB include gpu_uuid and gpu_index so waitForSeries only
	// succeeds when the series is on the right device with the correct index label.
	matchA := map[string]string{
		"namespace": specA.Namespace,
		"pod":       specA.Name,
		"pod_uid":   string(podA.UID),
		"gpu_uuid":  nvmlmock.Device0UUID,
		"gpu_index": "0",
	}
	matchB := map[string]string{
		"namespace": specB.Namespace,
		"pod":       specB.Name,
		"pod_uid":   string(podB.UID),
		"gpu_uuid":  nvmlmock.Device1UUID,
		"gpu_index": "1",
	}

	// 1. Positive assertion: each pod's series appears on its assigned device.
	for _, metricName := range bothMetricNames {
		seriesA, err := waitForSeries(ctx, c, metricName, matchA)
		if err != nil {
			t.Errorf("%s: pod-a series on Device0 never appeared: %v", metricName, err)
			continue
		}
		gotA := metrics.GaugeValue(seriesA)
		t.Logf("%s pod-a (pid=%d, device=0): value=%.2f", metricName, pidA, gotA)

		seriesB, err := waitForSeries(ctx, c, metricName, matchB)
		if err != nil {
			t.Errorf("%s: pod-b series on Device1 never appeared: %v", metricName, err)
			continue
		}
		gotB := metrics.GaugeValue(seriesB)
		t.Logf("%s pod-b (pid=%d, device=1): value=%.2f", metricName, pidB, gotB)

		if metricName == smMetricName {
			if gotA != 70 {
				t.Errorf("%s pod-a: want 70, got %.2f", metricName, gotA)
			}
			if gotB != 30 {
				t.Errorf("%s pod-b: want 30, got %.2f", metricName, gotB)
			}
		}
	}

	// 2. Negative assertion: no cross-device contamination.
	// pod-a's pod_uid must never appear under Device1UUID, and vice versa.
	crossA := map[string]string{
		"pod_uid":  string(podA.UID),
		"gpu_uuid": nvmlmock.Device1UUID,
	}
	crossB := map[string]string{
		"pod_uid":  string(podB.UID),
		"gpu_uuid": nvmlmock.Device0UUID,
	}
	for _, metricName := range bothMetricNames {
		if err := waitForAbsence(ctx, c, metricName, crossA); err != nil {
			t.Errorf("%s: pod-a (pod_uid=%s) bled onto Device1: %v", metricName, podA.UID, err)
		}
		if err := waitForAbsence(ctx, c, metricName, crossB); err != nil {
			t.Errorf("%s: pod-b (pod_uid=%s) bled onto Device0: %v", metricName, podB.UID, err)
		}
	}
}
