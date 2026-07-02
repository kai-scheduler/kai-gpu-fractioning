//go:build e2e

package tests

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/metrics"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/workload"
)

// TestE2E_SingleFractionalPodAttribution is TC-1 from
// test/e2e/docs/metricsd-e2e-test-plan.md: a pod carrying a fractional GPU
// annotation is tracked under the namespace/pod/pod_uid it actually has, and
// the gpu_index/gpu_uuid label matches what the device plugin actually
// assigned it — not an assumed value.
func TestE2E_SingleFractionalPodAttribution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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

	deviceLabel, deviceValue := discoverAssignedDevice(ctx, t, c, spec)

	matchLabels := map[string]string{
		"namespace": spec.Namespace,
		"pod":       spec.Name,
		"pod_uid":   string(pod.UID),
	}

	for _, metricName := range []string{memMetricName, smMetricName} {
		series, err := waitForSeries(ctx, c, metricName, matchLabels)
		if err != nil {
			t.Fatalf("%s: %v", metricName, err)
		}

		if got := metrics.Label(series, deviceLabel); got != deviceValue {
			t.Errorf("%s: %s label = %q, want %q (from MOCK_NVIDIA_VISIBLE_DEVICES)",
				metricName, deviceLabel, got, deviceValue)
		}
	}
}

// discoverAssignedDevice reads MOCK_NVIDIA_VISIBLE_DEVICES from the running
// container to learn which GPU the device plugin actually assigned — this
// is injected at container-create time and isn't visible on the stored Pod
// spec, so it must be read from the live container.
// sharing-manager/metricsd/internal/plugin/fakegpu/fakegpu.go treats numeric
// tokens as a gpu_index and non-numeric tokens as a gpu_uuid; this mirrors
// that so the test doesn't assume which form the device plugin used.
func discoverAssignedDevice(ctx context.Context, t *testing.T, c *cluster.Client, spec workload.FractionalPod) (label, value string) {
	t.Helper()

	out, err := pods.Exec(ctx, c, spec.Namespace, spec.Name, spec.ContainerName,
		[]string{"sh", "-c", "printenv MOCK_NVIDIA_VISIBLE_DEVICES"})
	if err != nil {
		t.Fatalf("read MOCK_NVIDIA_VISIBLE_DEVICES from %s/%s: %v", spec.Namespace, spec.Name, err)
	}

	value = strings.TrimSpace(out)
	if value == "" {
		t.Fatalf("MOCK_NVIDIA_VISIBLE_DEVICES was empty in %s/%s — did the device plugin assign a GPU?", spec.Namespace, spec.Name)
	}

	if _, err := strconv.Atoi(value); err == nil {
		return "gpu_index", value
	}
	return "gpu_uuid", value
}
