//go:build e2e

package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_SMUtilizationClampedAtOneHundred verifies that when two processes from
// the same pod both report SM utilization on the same GPU device, the engine
// sums their contributions (70 + 60 = 130) and clamps the result to 100 —
// testing engine.go lines 236-238 (the maxSMUtilPercent guard).
//
// Two containers in one pod each carry a distinct marker so nvmlmock.HostPID
// can resolve each container's host-namespace PID independently. Both PIDs are
// injected onto Device0UUID; their SMUtil values (70 and 60) intentionally sum
// above 100. The test asserts the exported metric reads exactly 100.
//
// Requires: E2E_NVML_MOCK=1 (set for nvml-mock clusters where per-process SM
// utilization injection is available).
func TestE2E_SMUtilizationClampedAtOneHundred(t *testing.T) {
	if !s.Client.Config.NVMLMock {
		t.Skip("requires nvml-mock (set E2E_NVML_MOCK=1)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	c := s.Client
	const podName = "tc5-clamp-pod"

	// Each marker is unique to its own container — no overlap in any process's
	// /proc/<pid>/cmdline, so HostPID returns exactly one match per marker.
	marker1 := fmt.Sprintf("gpumock-%s-%s-c1", attributionTestNamespace, podName)
	marker2 := fmt.Sprintf("gpumock-%s-%s-c2", attributionTestNamespace, podName)

	gpuNodes, err := nodes.ListGPUNodes(ctx, c)
	if err != nil {
		t.Fatalf("list GPU nodes: %v", err)
	}
	if len(gpuNodes) == 0 {
		t.Fatalf("no GPU nodes found matching selector %q", c.Config.GPUNodeSelector)
	}
	targetNode := gpuNodes[0].Name

	if err := workload.EnsureNamespace(ctx, c, attributionTestNamespace); err != nil {
		t.Fatalf("ensure namespace %s: %v", attributionTestNamespace, err)
	}

	// Clean up any pod left from a previous failed run before creating.
	_ = workload.Delete(ctx, c, attributionTestNamespace, podName)

	tc5Pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: attributionTestNamespace,
			Annotations: map[string]string{
				"nvidia.com/gpu-memory.container.proc1.limit":   "2048Mi",
				"nvidia.com/gpu-memory.container.proc1.request": "2048Mi",
				"nvidia.com/gpu-memory.container.proc2.limit":   "2048Mi",
				"nvidia.com/gpu-memory.container.proc2.request": "2048Mi",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector:  map[string]string{"kubernetes.io/hostname": targetNode},
			Containers: []corev1.Container{
				{
					Name:    "proc1",
					Image:   workload.DefaultImage,
					Command: []string{"sh", "-c", "sleep 86400 & wait # " + marker1},
				},
				{
					Name:    "proc2",
					Image:   workload.DefaultImage,
					Command: []string{"sh", "-c", "sleep 86400 & wait # " + marker2},
				},
			},
		},
	}

	if err := c.Ctrl.Create(ctx, tc5Pod); err != nil {
		t.Fatalf("create tc5 pod: %v", err)
	}
	t.Cleanup(func() {
		_ = workload.Delete(context.Background(), c, attributionTestNamespace, podName)
	})

	pod, err := workload.WaitRunning(ctx, c, attributionTestNamespace, podName, c.Config.PodReadyTimeout, c.Config.PollInterval)
	if err != nil {
		t.Fatalf("pod never reached Running: %v", err)
	}

	pid1, err := nvmlmock.HostPID(ctx, c, targetNode, marker1)
	if err != nil {
		t.Fatalf("resolve host PID for proc1: %v", err)
	}
	pid2, err := nvmlmock.HostPID(ctx, c, targetNode, marker2)
	if err != nil {
		t.Fatalf("resolve host PID for proc2: %v", err)
	}

	// 70 + 60 = 130 > 100 — engine must clamp to 100.
	procs := []nvmlmock.Proc{
		{UUID: nvmlmock.Device0UUID, PID: pid1, SMUtil: 70},
		{UUID: nvmlmock.Device0UUID, PID: pid2, SMUtil: 60},
	}
	if err := nvmlmock.SetProcesses(ctx, c, nvmlmock.A100, procs); err != nil {
		t.Fatalf("configure nvml-mock processes: %v", err)
	}
	t.Cleanup(func() {
		if err := nvmlmock.SetProcesses(context.Background(), c, nvmlmock.A100, nil); err != nil {
			t.Errorf("reset nvml-mock to idle: %v", err)
		}
	})

	match := map[string]string{
		"namespace": attributionTestNamespace,
		"pod":       podName,
		"pod_uid":   string(pod.UID),
		"gpu_uuid":  nvmlmock.Device0UUID,
	}
	series, err := waitForSeries(ctx, c, smMetricName, match)
	if err != nil {
		t.Fatalf("%s series never appeared: %v", smMetricName, err)
	}
	got := metrics.GaugeValue(series)
	t.Logf("%s (pid1=%d sm_util=70 + pid2=%d sm_util=60 = 130): clamped value=%.2f", smMetricName, pid1, pid2, got)
	if got != 100 {
		t.Errorf("%s: want 100 (clamped from 130), got %.2f", smMetricName, got)
	}
}
