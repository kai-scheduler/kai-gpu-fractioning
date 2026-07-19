//go:build e2e

package metrics

import (
	"context"
	"strconv"
	"testing"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_SoloFractionalPodIsNotComputeThrottled verifies that a fractional GPU
// annotation only partitions memory in this system — it carries no SM
// utilization cap. A solo pod whose declared fraction is 25% of the device
// must still be able to report the full SM util that NVML observed.
//
// The test pins the pod's host PID in nvml-mock with sm_util=95
// (deliberately far above the 25% declared fraction) and asserts
// gpu_sharing_gpu_sm_utilization_percent == 95, proving the exporter
// reports what NVML says without any fraction-proportional adjustment.
//
// This protects against a future reader "fixing" the metric to cap it near
// the fraction: the fractional request only controls memory admission, not
// compute scheduling.
//
// Memory is asserted for presence only: nvml-mock's
// GetComputeRunningProcesses_v3 always returns usedGpuMemory=0 regardless of
// the configured value (documented nvml-mock limitation — see attribution_test.go NOTE).
//
// Requires: E2E_NVML_MOCK=1.
func TestE2E_SoloFractionalPodIsNotComputeThrottled(t *testing.T) {
	if !s.Client.Config.NVMLMock {
		t.Skip(skipNoNVMLMock)
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
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

	// 25% of total GPU memory — small fraction, deliberately below the sm_util
	// we will inject (95%), to make it obvious the two are independent.
	quarterMemMiB := strconv.Itoa(c.Config.GPUMemoryMiB / 4)

	spec := workload.FractionalPod{
		Namespace:           attributionTestNamespace,
		Name:                "tc10-no-compute-throttle-pod",
		ContainerName:       "trainer",
		GPUMemoryLimitMiB:   quarterMemMiB,
		GPUMemoryRequestMiB: quarterMemMiB,
		NodeSelector:        map[string]string{"kubernetes.io/hostname": targetNode},
	}

	_ = workload.Delete(ctx, c, spec.Namespace, spec.Name)
	pod, err := workload.Apply(ctx, c, spec)
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}
	t.Cleanup(func() {
		if err := workload.Delete(context.Background(), c, spec.Namespace, spec.Name); err != nil {
			t.Errorf("cleanup pod %s/%s: %v", spec.Namespace, spec.Name, err)
		}
	})

	marker := workload.DefaultMarker(spec.Namespace, spec.Name)
	pid, err := nvmlmock.HostPID(ctx, c, targetNode, marker)
	if err != nil {
		t.Fatalf("resolve host PID: %v", err)
	}

	const (
		gpuUUID    = nvmlmock.Device0UUID
		wantSMUtil = 95 // far above the 25% declared fraction
	)

	procs := []nvmlmock.Proc{{UUID: gpuUUID, PID: pid, SMUtil: wantSMUtil}}
	if err := setProcesses(ctx, c, nvmlmock.A100, procs); err != nil {
		t.Fatalf("configure nvml-mock: %v", err)
	}
	t.Cleanup(func() {
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

	// SM util must equal exactly 95 — the exporter must report what NVML
	// measured, not scale it down by the requested fraction (25%).
	smSeries, err := waitForSeries(ctx, c, smMetricName, matchLabels)
	if err != nil {
		t.Fatalf("%s: series never appeared: %v", smMetricName, err)
	}
	gotSMUtil := metrics.GaugeValue(smSeries)
	t.Logf("%s: got %.2f (declared fraction=25%%, injected sm_util=%d)", smMetricName, gotSMUtil, wantSMUtil)
	if gotSMUtil != wantSMUtil {
		t.Errorf("%s: want %d (no fraction cap), got %.2f", smMetricName, wantSMUtil, gotSMUtil)
	}

	// Memory: assert presence only (nvml-mock always returns usedGpuMemory=0).
	if _, err := waitForSeries(ctx, c, memMetricName, matchLabels); err != nil {
		t.Errorf("%s: series never appeared: %v", memMetricName, err)
	}
}
