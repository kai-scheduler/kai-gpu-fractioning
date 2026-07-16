//go:build e2e

package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/nvmlmock"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_MultiProcessPerPodAggregation verifies that when several GPU
// processes from different containers in the same pod are reported by NVML,
// metricsd sums them into a single per-pod metric series on the shared device.
//
// Pod A has 2 containers → 2 attributed processes on Device0UUID.
// Pod B has 3 containers → 3 attributed processes on Device0UUID.
//
// Each container is tracked independently by sharingd (separate cgroup, separate
// fsstore entry) but all containers in a pod share the same pod_uid, so the
// engine groups them under the same (pod_uid, gpu_uuid) key and sums:
//   - gpu_sharing_gpu_memory_used_bytes = Σ(per-container used_memory_mib) * 1MiB
//   - gpu_sharing_gpu_sm_utilization_percent = Σ(per-container sm_util), clamped at 100
func TestE2E_MultiProcessPerPodAggregation(t *testing.T) {
	if !s.Client.Config.NVMLMock {
		t.Skip("requires nvml-mock (set E2E_NVML_MOCK=1)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
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
	nodeSel := map[string]string{"kubernetes.io/hostname": targetNode}

	// Pod A: 2 containers, each attributed as a separate GPU process.
	specA := multiContainerPodSpec{
		Namespace:    attributionTestNamespace,
		Name:         "tc-agg-pod-a",
		Containers:   []string{"c1", "c2"},
		MemoryMiB:    "1024",
		NodeSelector: nodeSel,
	}
	podA, err := applyMultiContainerFractionalPod(ctx, c, specA)
	if err != nil {
		t.Fatalf("create pod-a: %v", err)
	}
	t.Cleanup(func() {
		if err := workload.Delete(context.Background(), c, specA.Namespace, specA.Name); err != nil {
			t.Errorf("cleanup pod-a: %v", err)
		}
	})

	// Pod B: 3 containers, each attributed as a separate GPU process.
	specB := multiContainerPodSpec{
		Namespace:    attributionTestNamespace,
		Name:         "tc-agg-pod-b",
		Containers:   []string{"c1", "c2", "c3"},
		MemoryMiB:    "512",
		NodeSelector: nodeSel,
	}
	podB, err := applyMultiContainerFractionalPod(ctx, c, specB)
	if err != nil {
		t.Fatalf("create pod-b: %v", err)
	}
	t.Cleanup(func() {
		if err := workload.Delete(context.Background(), c, specB.Namespace, specB.Name); err != nil {
			t.Errorf("cleanup pod-b: %v", err)
		}
	})

	// Resolve each container's host-namespace PID. Each container runs a shell
	// with a unique marker in argv so HostPID resolves exactly one PID per call.
	pidA1, err := nvmlmock.HostPID(ctx, c, targetNode, multiContainerMarker(attributionTestNamespace, "tc-agg-pod-a", "c1"))
	if err != nil {
		t.Fatalf("resolve pid a/c1: %v", err)
	}
	pidA2, err := nvmlmock.HostPID(ctx, c, targetNode, multiContainerMarker(attributionTestNamespace, "tc-agg-pod-a", "c2"))
	if err != nil {
		t.Fatalf("resolve pid a/c2: %v", err)
	}
	pidB1, err := nvmlmock.HostPID(ctx, c, targetNode, multiContainerMarker(attributionTestNamespace, "tc-agg-pod-b", "c1"))
	if err != nil {
		t.Fatalf("resolve pid b/c1: %v", err)
	}
	pidB2, err := nvmlmock.HostPID(ctx, c, targetNode, multiContainerMarker(attributionTestNamespace, "tc-agg-pod-b", "c2"))
	if err != nil {
		t.Fatalf("resolve pid b/c2: %v", err)
	}
	pidB3, err := nvmlmock.HostPID(ctx, c, targetNode, multiContainerMarker(attributionTestNamespace, "tc-agg-pod-b", "c3"))
	if err != nil {
		t.Fatalf("resolve pid b/c3: %v", err)
	}

	// Configure nvml-mock: 2 processes for pod-a, 3 for pod-b, all on Device0UUID.
	// Memory values are in MiB; the mock returns MiB*1024*1024 bytes via
	// GetComputeRunningProcesses. SM util is per-process percent (0–100).
	const gpuUUID = nvmlmock.Device0UUID
	const (
		memA1MiB = uint64(2 * 1024) // 2 GiB
		memA2MiB = uint64(3 * 1024) // 3 GiB
		memB1MiB = uint64(1 * 1024) // 1 GiB
		memB2MiB = uint64(1 * 1024) // 1 GiB
		memB3MiB = uint64(2 * 1024) // 2 GiB
		smA1     = uint32(20)
		smA2     = uint32(30)
		smB1     = uint32(10)
		smB2     = uint32(15)
		smB3     = uint32(20)
	)

	procs := []nvmlmock.Proc{
		{UUID: gpuUUID, PID: pidA1, UsedMemoryMiB: memA1MiB, SMUtil: smA1},
		{UUID: gpuUUID, PID: pidA2, UsedMemoryMiB: memA2MiB, SMUtil: smA2},
		{UUID: gpuUUID, PID: pidB1, UsedMemoryMiB: memB1MiB, SMUtil: smB1},
		{UUID: gpuUUID, PID: pidB2, UsedMemoryMiB: memB2MiB, SMUtil: smB2},
		{UUID: gpuUUID, PID: pidB3, UsedMemoryMiB: memB3MiB, SMUtil: smB3},
	}
	if err := nvmlmock.SetProcesses(ctx, c, nvmlmock.A100, procs); err != nil {
		t.Fatalf("configure nvml-mock: %v", err)
	}
	t.Cleanup(func() {
		if err := nvmlmock.SetProcesses(context.Background(), c, nvmlmock.A100, nil); err != nil {
			t.Errorf("reset nvml-mock: %v", err)
		}
	})

	matchA := map[string]string{
		"namespace": attributionTestNamespace,
		"pod":       "tc-agg-pod-a",
		"pod_uid":   string(podA.UID),
		"gpu_uuid":  gpuUUID,
	}
	matchB := map[string]string{
		"namespace": attributionTestNamespace,
		"pod":       "tc-agg-pod-b",
		"pod_uid":   string(podB.UID),
		"gpu_uuid":  gpuUUID,
	}

	// Expected aggregated SM values.
	wantSMA := float64(smA1 + smA2)         // 50 (no clamping)
	wantSMB := float64(smB1 + smB2 + smB3) // 45 (no clamping)

	// Memory: assert presence only — nvml-mock's GetComputeRunningProcesses_v3
	// always returns usedGpuMemory=0 regardless of the configured UsedMemoryMiB,
	// so exact-byte assertions would always fail. Exact-value coverage is deferred
	// until the mock gains per-process memory fidelity (see attribution_test.go NOTE).
	if _, err := waitForSeries(ctx, c, memMetricName, matchA); err != nil {
		t.Errorf("%s pod-a: series never appeared: %v", memMetricName, err)
	}
	if _, err := waitForSeries(ctx, c, memMetricName, matchB); err != nil {
		t.Errorf("%s pod-b: series never appeared: %v", memMetricName, err)
	}

	// SM utilization: engine sums sm_util across all processes per pod×GPU, capped at 100.
	sA, err := waitForSeries(ctx, c, smMetricName, matchA)
	if err != nil {
		t.Errorf("%s pod-a: %v", smMetricName, err)
	} else if got := sA.GetGauge().GetValue(); got != wantSMA {
		t.Errorf("%s pod-a: want %.0f (sum of 2 processes), got %.2f", smMetricName, wantSMA, got)
	}

	sB, err := waitForSeries(ctx, c, smMetricName, matchB)
	if err != nil {
		t.Errorf("%s pod-b: %v", smMetricName, err)
	} else if got := sB.GetGauge().GetValue(); got != wantSMB {
		t.Errorf("%s pod-b: want %.0f (sum of 3 processes), got %.2f", smMetricName, wantSMB, got)
	}
}

// multiContainerPodSpec describes a fractional pod with several GPU-tracked containers.
type multiContainerPodSpec struct {
	Namespace    string
	Name         string
	Containers   []string // container names; each gets its own GPU annotation + unique marker
	MemoryMiB    string   // GPU memory per container (same for all)
	NodeSelector map[string]string
}

// multiContainerMarker returns the unique cmdline token embedded in each
// container's shell argv so HostPID can resolve exactly one PID per container.
// The namespace+pod+container triple guarantees uniqueness across concurrent tests.
func multiContainerMarker(namespace, podName, containerName string) string {
	return fmt.Sprintf("gpumock-%s-%s-%s", namespace, podName, containerName)
}

// applyMultiContainerFractionalPod creates a pod whose containers each carry a
// GPU-memory annotation, making sharingd track each container independently.
// The pod runs on the node given by spec.NodeSelector. Callers must Delete the
// pod when done; use workload.Delete since the pod name/namespace is the key.
func applyMultiContainerFractionalPod(ctx context.Context, c *cluster.Client, spec multiContainerPodSpec) (*corev1.Pod, error) {
	if err := workload.EnsureNamespace(ctx, c, spec.Namespace); err != nil {
		return nil, fmt.Errorf("ensure namespace %s: %w", spec.Namespace, err)
	}
	if err := workload.Delete(ctx, c, spec.Namespace, spec.Name); err != nil {
		return nil, fmt.Errorf("pre-create cleanup of %s/%s: %w", spec.Namespace, spec.Name, err)
	}

	// One annotation pair per container so sharingd tracks every container.
	annotations := make(map[string]string, len(spec.Containers)*2)
	for _, name := range spec.Containers {
		annotations[fmt.Sprintf("nvidia.com/gpu-memory.container.%s.limit", name)] = spec.MemoryMiB + "Mi"
		annotations[fmt.Sprintf("nvidia.com/gpu-memory.container.%s.request", name)] = spec.MemoryMiB + "Mi"
	}

	containers := make([]corev1.Container, 0, len(spec.Containers))
	for _, name := range spec.Containers {
		marker := multiContainerMarker(spec.Namespace, spec.Name, name)
		containers = append(containers, corev1.Container{
			Name:  name,
			Image: workload.DefaultImage,
			// Marker rides in the shell argv (same trick as workload.Apply).
			// The backgrounded sleep keeps the shell alive with the marker in
			// its /proc/<pid>/cmdline so HostPID finds exactly one match.
			Command: []string{"sh", "-c", fmt.Sprintf("sleep 86400 & wait # %s", marker)},
		})
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   spec.Namespace,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector:  spec.NodeSelector,
			Containers:    containers,
		},
	}
	if err := c.Ctrl.Create(ctx, pod); err != nil {
		return nil, fmt.Errorf("create pod %s/%s: %w", spec.Namespace, spec.Name, err)
	}

	return workload.WaitRunning(ctx, c, spec.Namespace, spec.Name, c.Config.PodReadyTimeout, c.Config.PollInterval)
}
