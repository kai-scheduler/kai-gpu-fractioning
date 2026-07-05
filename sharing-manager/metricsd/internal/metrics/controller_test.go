package metrics

import (
	"context"
	"log/slog"
	"testing"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/store"
)

type fakeCgroupResolver map[uint32][]string

func (r fakeCgroupResolver) CgroupPaths(pid uint32) ([]string, error) {
	return r[pid], nil
}

func TestMetricsControllerSkipsCollectionWhenStoreIsEmpty(t *testing.T) {
	collector := &fakeCollector{}
	controller := newMetricsController(collector, fakeCgroupResolver{}, store.FakeStore{}, 0, 0, slog.Default())

	controller.collect(context.Background())

	if collector.snapshots != 0 {
		t.Fatalf("expected collector not to be sampled for empty store, got %d snapshots", collector.snapshots)
	}
}

func TestMetricsControllerCollectsAfterFirstContainerIsStored(t *testing.T) {
	collector := &fakeCollector{}
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{{
		ContainerID: "container-id",
		Pod:         "pod",
		Namespace:   "default",
		PodUID:      "pod-uid",
		GPUDevices:  []store.GPUDevice{{Index: 0}},
	}}}
	controller := newMetricsController(collector, fakeCgroupResolver{}, containerStore, 0, 0, slog.Default())

	controller.collect(context.Background())

	if collector.snapshots != 1 {
		t.Fatalf("expected collector to be sampled once, got %d snapshots", collector.snapshots)
	}
}

func TestMetricsControllerEnrichesGPUProcessesWithPodMetadata(t *testing.T) {
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{{
		ContainerID: "container-id",
		Container:   "container",
		Pod:         "pod",
		Namespace:   "default",
		PodUID:      "pod-uid",
		CgroupPath:  "/kubepods.slice/pod.slice/container.scope",
		GPUDevices:  []store.GPUDevice{{Index: 0}},
	}}}

	controller := newMetricsController(nil, fakeCgroupResolver{
		1234: []string{"/kubepods.slice/pod.slice/container.scope/deeper"},
	}, containerStore, 0, 0, slog.Default())

	metrics, unmatched := controller.enrich(context.Background(), []GPUProcessMetric{
		{
			PID:                  1234,
			GPUUUID:              "GPU-1",
			GPUIndex:             0,
			UsedGPUMemoryBytes:   1024,
			SMUtilizationPercent: 25,
		},
	}, controller.pods.Snapshot())
	if unmatched != 0 {
		t.Fatalf("expected no unmatched processes, got %d", unmatched)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected one pod metric, got %d", len(metrics))
	}
	metric := metrics[0]
	if metric.Namespace != "default" || metric.Pod != "pod" || metric.PodUID != "pod-uid" {
		t.Fatalf("unexpected pod metadata: %#v", metric)
	}
	if metric.MemoryBytes != 1024 || metric.SMUtilizationPercent != 25 {
		t.Fatalf("unexpected metric values: %#v", metric)
	}
}

func TestMetricsControllerAggregatesContainersByPodAndGPU(t *testing.T) {
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{
		{
			ContainerID: "container-a",
			Container:   "container-a",
			Pod:         "pod",
			Namespace:   "default",
			PodUID:      "pod-uid",
			CgroupPath:  "/kubepods.slice/pod.slice/container-a.scope",
			GPUDevices:  []store.GPUDevice{{Index: 0}},
		},
		{
			ContainerID: "container-b",
			Container:   "container-b",
			Pod:         "pod",
			Namespace:   "default",
			PodUID:      "pod-uid",
			CgroupPath:  "/kubepods.slice/pod.slice/container-b.scope",
			GPUDevices:  []store.GPUDevice{{Index: 0}},
		},
	}}

	controller := newMetricsController(nil, fakeCgroupResolver{
		1001: []string{"/kubepods.slice/pod.slice/container-a.scope/deeper"},
		1002: []string{"/kubepods.slice/pod.slice/container-b.scope/deeper"},
	}, containerStore, 0, 0, slog.Default())

	metrics, unmatched := controller.enrich(context.Background(), []GPUProcessMetric{
		{PID: 1001, GPUUUID: "GPU-1", GPUIndex: 0, UsedGPUMemoryBytes: 1024, SMUtilizationPercent: 10},
		{PID: 1002, GPUUUID: "GPU-1", GPUIndex: 0, UsedGPUMemoryBytes: 2048, SMUtilizationPercent: 20},
	}, controller.pods.Snapshot())
	if unmatched != 0 {
		t.Fatalf("expected no unmatched processes, got %d", unmatched)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected one pod/device metric, got %d", len(metrics))
	}
	metric := metrics[0]
	if metric.MemoryBytes != 3072 || metric.SMUtilizationPercent != 30 {
		t.Fatalf("expected accumulated pod/device metric, got %#v", metric)
	}
}

func TestMetricsControllerNormalizesSMUtilByRequestedFraction(t *testing.T) {
	tests := []struct {
		name           string
		fraction       float64
		smUtil         uint32
		wantNormalized float64
	}{
		{name: "half request fully used", fraction: 0.5, smUtil: 30, wantNormalized: 60},
		{name: "exactly at cap", fraction: 0.5, smUtil: 50, wantNormalized: 100},
		{name: "over-utilized is capped at 100", fraction: 0.5, smUtil: 80, wantNormalized: 100},
		{name: "quarter request", fraction: 0.25, smUtil: 10, wantNormalized: 40},
		{name: "unknown request falls back to raw utilization", fraction: 0, smUtil: 40, wantNormalized: 40},
		{name: "unknown request still capped at 100", fraction: 0, smUtil: 100, wantNormalized: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerStore := store.FakeStore{Containers: []store.ContainerInfo{{
				ContainerID:          "container-id",
				Container:            "container",
				Pod:                  "pod",
				Namespace:            "default",
				PodUID:               "pod-uid",
				CgroupPath:           "/kubepods.slice/pod.slice/container.scope",
				GPUDevices:           []store.GPUDevice{{Index: 0}},
				RequestedGPUFraction: tt.fraction,
			}}}

			controller := newMetricsController(nil, fakeCgroupResolver{
				1234: []string{"/kubepods.slice/pod.slice/container.scope/deeper"},
			}, containerStore, 0, 0, slog.Default())

			metrics, unmatched := controller.enrich(context.Background(), []GPUProcessMetric{
				{PID: 1234, GPUUUID: "GPU-1", GPUIndex: 0, SMUtilizationPercent: tt.smUtil},
			}, controller.pods.Snapshot())
			controller.normalizeSMUtil(metrics)

			if unmatched != 0 {
				t.Fatalf("expected no unmatched processes, got %d", unmatched)
			}
			if len(metrics) != 1 {
				t.Fatalf("expected one pod metric, got %d", len(metrics))
			}
			m := metrics[0]
			if m.RequestedGPUFraction != tt.fraction {
				t.Fatalf("expected RequestedGPUFraction = %g, got %g", tt.fraction, m.RequestedGPUFraction)
			}
			if m.SMUtilizationPercentNormalized != tt.wantNormalized {
				t.Fatalf("expected normalized SM util = %g, got %g", tt.wantNormalized, m.SMUtilizationPercentNormalized)
			}
		})
	}
}

// Two containers of the same pod share one GPU; their fractions sum, and each
// container's fraction is counted once even when it has multiple GPU processes.
func TestMetricsControllerSumsRequestedFractionAcrossContainers(t *testing.T) {
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{
		{
			ContainerID:          "container-a",
			Pod:                  "pod",
			Namespace:            "default",
			PodUID:               "pod-uid",
			CgroupPath:           "/kubepods.slice/pod.slice/container-a.scope",
			GPUDevices:           []store.GPUDevice{{Index: 0}},
			RequestedGPUFraction: 0.25,
		},
		{
			ContainerID:          "container-b",
			Pod:                  "pod",
			Namespace:            "default",
			PodUID:               "pod-uid",
			CgroupPath:           "/kubepods.slice/pod.slice/container-b.scope",
			GPUDevices:           []store.GPUDevice{{Index: 0}},
			RequestedGPUFraction: 0.25,
		},
	}}

	controller := newMetricsController(nil, fakeCgroupResolver{
		1001: []string{"/kubepods.slice/pod.slice/container-a.scope/deeper"},
		1002: []string{"/kubepods.slice/pod.slice/container-a.scope/deeper"}, // second process, same container
		1003: []string{"/kubepods.slice/pod.slice/container-b.scope/deeper"},
	}, containerStore, 0, 0, slog.Default())

	metrics, _ := controller.enrich(context.Background(), []GPUProcessMetric{
		{PID: 1001, GPUUUID: "GPU-1", GPUIndex: 0, SMUtilizationPercent: 10},
		{PID: 1002, GPUUUID: "GPU-1", GPUIndex: 0, SMUtilizationPercent: 10},
		{PID: 1003, GPUUUID: "GPU-1", GPUIndex: 0, SMUtilizationPercent: 10},
	}, controller.pods.Snapshot())

	if len(metrics) != 1 {
		t.Fatalf("expected one pod/device metric, got %d", len(metrics))
	}
	// container-a counted once (0.25) + container-b (0.25) = 0.5, not 0.75.
	if metrics[0].RequestedGPUFraction != 0.5 {
		t.Fatalf("expected summed fraction 0.5, got %g", metrics[0].RequestedGPUFraction)
	}
}

func TestMetricsControllerPublishesIdleMetricForActiveGPUContainer(t *testing.T) {
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{{
		ContainerID: "container-id",
		Container:   "container",
		Pod:         "pod",
		Namespace:   "default",
		PodUID:      "pod-uid",
		GPUDevices:  []store.GPUDevice{{Index: 0}},
	}}}

	controller := newMetricsController(nil, fakeCgroupResolver{}, containerStore, 0, 0, slog.Default())
	controller.rememberDeviceUUIDs(map[int]string{0: "GPU-0"})
	metrics, unmatched := controller.enrich(context.Background(), nil, controller.pods.Snapshot())
	if unmatched != 0 {
		t.Fatalf("expected no unmatched processes, got %d", unmatched)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected one idle pod metric, got %d", len(metrics))
	}
	metric := metrics[0]
	if metric.PodUID != "pod-uid" || metric.GPUUUID != "GPU-0" || metric.GPUIndex != 0 {
		t.Fatalf("unexpected idle metric identity: %#v", metric)
	}
	if metric.MemoryBytes != 0 || metric.SMUtilizationPercent != 0 {
		t.Fatalf("expected idle metric values to be zero, got %#v", metric)
	}
}

func TestMetricsControllerSkipsIdleMetricForAnnotatedContainerWithoutGPUDevices(t *testing.T) {
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{{
		ContainerID: "container-id",
		Container:   "container",
		Pod:         "pod",
		Namespace:   "default",
		PodUID:      "pod-uid",
	}}}

	controller := newMetricsController(nil, fakeCgroupResolver{}, containerStore, 0, 0, slog.Default())
	metrics, unmatched := controller.enrich(context.Background(), nil, controller.pods.Snapshot())
	if unmatched != 0 {
		t.Fatalf("expected no unmatched processes, got %d", unmatched)
	}
	if len(metrics) != 0 {
		t.Fatalf("expected no idle metric without GPU devices, got %d", len(metrics))
	}
}

func TestMetricsControllerPublishesIdleMetricsForAnnotatedGPUIndexes(t *testing.T) {
	containerStore := store.FakeStore{Containers: []store.ContainerInfo{{
		ContainerID: "container-id",
		Container:   "container",
		Pod:         "pod",
		Namespace:   "default",
		PodUID:      "pod-uid",
		GPUDevices: []store.GPUDevice{
			{Index: 0},
			{Index: 1},
		},
	}}}

	controller := newMetricsController(nil, fakeCgroupResolver{}, containerStore, 0, 0, slog.Default())
	controller.rememberDeviceUUIDs(map[int]string{0: "GPU-0", 1: "GPU-1"})
	metrics, unmatched := controller.enrich(context.Background(), nil, controller.pods.Snapshot())
	if unmatched != 0 {
		t.Fatalf("expected no unmatched processes, got %d", unmatched)
	}
	if len(metrics) != 2 {
		t.Fatalf("expected two idle pod metrics, got %d", len(metrics))
	}
	seen := map[int]string{}
	for _, metric := range metrics {
		seen[metric.GPUIndex] = metric.GPUUUID
		if metric.MemoryBytes != 0 || metric.SMUtilizationPercent != 0 {
			t.Fatalf("expected idle metric values to be zero, got %#v", metric)
		}
	}
	if seen[0] != "GPU-0" || seen[1] != "GPU-1" {
		t.Fatalf("expected idle metrics for both GPU UUIDs, got %#v", seen)
	}
}

func TestMetricsControllerSkipsZeroPIDProcesses(t *testing.T) {
	containerStore := store.FakeStore{}
	resolver := fakeCgroupResolver{}
	controller := newMetricsController(nil, resolver, containerStore, 0, 0, slog.Default())

	metrics, unmatched := controller.enrich(context.Background(), []GPUProcessMetric{
		{
			PID:     0,
			GPUUUID: "GPU-1",
		},
	}, controller.pods.Snapshot())
	if unmatched != 0 {
		t.Fatalf("expected zero PID process to be skipped without counting as unmatched, got %d", unmatched)
	}
	if len(metrics) != 0 {
		t.Fatalf("expected no metrics for zero PID process, got %d", len(metrics))
	}
}

func TestWindowedSMUtilAveragesWithinWindow(t *testing.T) {
	type collect struct{ in, want float64 }
	tests := []struct {
		name       string
		windowSize int
		collects   []collect
	}{
		{
			name:       "single sample returns itself",
			windowSize: 4,
			collects:   []collect{{80, 80}},
		},
		{
			name:       "two samples averaged",
			windowSize: 4,
			collects:   []collect{{80, 80}, {20, 50}},
		},
		{
			name:       "oldest evicted when window full",
			windowSize: 4,
			collects: []collect{
				{80, 80},
				{20, 50},
				{60, (80 + 20 + 60) / 3.0},
				{40, 50},
				{100, (20 + 60 + 40 + 100) / 4.0}, // 80 evicted
			},
		},
		{
			name:       "window size 2 evicts after two samples",
			windowSize: 2,
			collects:   []collect{{80, 80}, {20, 50}, {60, 40}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &metricsController{
				smUtilWindowSize: tt.windowSize,
				smUtilBuf:        map[podGPUKey][]float64{},
				deviceUUIDs:      map[int]string{},
			}
			for i, sw := range tt.collects {
				got := s.windowedSMUtil([]PodGPUMetric{{PodUID: "p", GPUUUID: "G", SMUtilizationPercent: sw.in}})
				if got[0].SMUtilizationPercent != sw.want {
					t.Errorf("collect %d (in=%.1f): want %.4f, got %.4f", i+1, sw.in, sw.want, got[0].SMUtilizationPercent)
				}
			}
		})
	}
}

func TestWindowedSMUtilPrunesDeletedPodSeriesImmediately(t *testing.T) {
	controller := &metricsController{
		smUtilWindowSize: 3,
		smUtilBuf:        map[podGPUKey][]float64{},
		deviceUUIDs:      map[int]string{},
	}

	pod := []PodGPUMetric{{PodUID: "pod-uid", GPUUUID: "GPU-0", GPUIndex: 0, SMUtilizationPercent: 50}}
	controller.windowedSMUtil(pod)

	if len(controller.smUtilBuf) != 1 {
		t.Fatalf("expected 1 buffered series after first collect, got %d", len(controller.smUtilBuf))
	}

	// Pod disappears — next collect carries no metrics for it. Buffer entry must
	// be pruned immediately (no time-based expiry needed).
	controller.windowedSMUtil(nil)

	if len(controller.smUtilBuf) != 0 {
		t.Fatalf("expected stale series to be pruned immediately, got %d entries", len(controller.smUtilBuf))
	}
}
