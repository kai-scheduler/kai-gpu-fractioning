package metrics

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/store"
)

const DefaultPath = "/metrics"

type metricsController struct {
	mu               sync.RWMutex
	collector        GPUProcessCollector
	pods             podSource
	interval         time.Duration
	smUtilWindow     time.Duration
	smUtilWindowSize int
	smUtilBuf        map[podGPUKey][]float64
	log              *slog.Logger
	deviceUUIDs      map[int]string
	snapshot         Snapshot
}

func newMetricsController(collector GPUProcessCollector, resolver PIDCgroupResolver, reader store.Reader, interval, smUtilWindow time.Duration, logger *slog.Logger) *metricsController {
	return newMetricsControllerWithPodSource(collector, newCgroupPodSource(resolver, reader, logger), interval, smUtilWindow, logger)
}

func newMetricsControllerWithPodSource(collector GPUProcessCollector, pods podSource, interval, smUtilWindow time.Duration, logger *slog.Logger) *metricsController {
	if logger == nil {
		logger = slog.Default()
	}
	// Window size in number of samples: max(1, window/interval). Windowing is
	// active only when windowSize > 1, i.e. when smUtilWindow > interval.
	windowSize := 1
	if interval > 0 && smUtilWindow > interval {
		windowSize = int(smUtilWindow / interval)
	}
	var smUtilBuf map[podGPUKey][]float64
	if windowSize > 1 {
		smUtilBuf = map[podGPUKey][]float64{}
	}
	return &metricsController{
		collector:        collector,
		pods:             pods,
		interval:         interval,
		smUtilWindow:     smUtilWindow,
		smUtilWindowSize: windowSize,
		smUtilBuf:        smUtilBuf,
		log:              logger,
		deviceUUIDs:      map[int]string{},
	}
}

func (s *metricsController) Run(ctx context.Context) error {
	if s.collector == nil || s.pods == nil {
		return errors.New("metrics controller: collector or pod source is nil")
	}

	// Release backend resources (e.g. the PodResources gRPC connection) when the
	// control loop exits.
	defer func() {
		if closer, ok := s.pods.(io.Closer); ok {
			_ = closer.Close()
		}
	}()

	s.collect(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.collect(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// Snapshot satisfies SnapshotProvider. The in-process controller returns the latest
// locally cached snapshot and never errors; the context and error are part of
// the contract so a future out-of-process provider can honor them.
func (s *metricsController) Snapshot(ctx context.Context) (Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.snapshot), nil
}

func (s *metricsController) collect(ctx context.Context) {
	// Snapshot fetches the container mapping once; all queries in this cycle
	// hit the resulting in-memory store rather than the filesystem.
	pods := s.pods.Snapshot()
	activePodUIDs := pods.ActivePodUIDs()
	if len(pods.ActiveContainers()) == 0 {
		s.setSnapshot(nil, activePodUIDs)
		s.log.DebugContext(ctx, "skipped GPU metrics collection because no pods are using GPUs")
		return
	}

	snapshot := s.collector.Snapshot()
	if snapshot.DeviceErrors != nil {
		// Partial result: some devices failed but others succeeded. Surface the
		// failure but still publish what we have so healthy GPUs keep reporting.
		s.log.WarnContext(ctx, "publishing partial GPU metrics; some devices failed", "error", snapshot.DeviceErrors)
	}
	s.rememberDeviceUUIDs(snapshot.DeviceUUIDs)

	metrics, unmatched := s.enrich(ctx, snapshot.Processes, pods)
	if s.smUtilWindow > s.interval {
		metrics = s.windowedSMUtil(metrics)
	}
	s.setSnapshot(metrics, activePodUIDs)
	s.log.DebugContext(ctx, "completed GPU metrics collect", "podMetrics", len(metrics), "unmatchedGPUProcesses", unmatched)
	for _, m := range metrics {
		s.log.DebugContext(ctx, "pod GPU metric",
			"pod", m.Namespace+"/"+m.Pod,
			"gpu_uuid", m.GPUUUID,
			"gpu_index", m.GPUIndex,
			"memory_bytes", m.MemoryBytes,
			"sm_utilization_percent", m.SMUtilizationPercent,
		)
	}
}

func (s *metricsController) setSnapshot(metrics []PodGPUMetric, activePodUIDs map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Metrics = append([]PodGPUMetric(nil), metrics...)
	s.snapshot.ActivePodUIDs = maps.Clone(activePodUIDs)
}

func (s *metricsController) enrich(ctx context.Context, processes []GPUProcessMetric, pods podSource) ([]PodGPUMetric, int) {
	byPodGPU := map[podGPUKey]*PodGPUMetric{}
	observedPodDevices := map[string]struct{}{}
	unmatched := 0

	for _, process := range processes {
		if process.PID == 0 {
			s.log.DebugContext(ctx, "skipping GPU process with empty PID", "gpuUUID", process.GPUUUID)
			continue
		}

		container, ok := pods.ResolveProcess(process)
		if !ok {
			unmatched++
			s.log.DebugContext(ctx, "GPU process could not be attributed to a pod", "pid", process.PID, "gpuUUID", process.GPUUUID)
			continue
		}
		key := podGPUKey{
			Namespace: container.Namespace,
			Pod:       container.Pod,
			PodUID:    container.PodUID,
			GPUUUID:   process.GPUUUID,
			GPUIndex:  process.GPUIndex,
		}
		observePodDevice(observedPodDevices, key)
		metric := byPodGPU[key]
		if metric == nil {
			metric = &PodGPUMetric{
				Namespace: key.Namespace,
				Pod:       key.Pod,
				PodUID:    key.PodUID,
				GPUUUID:   key.GPUUUID,
				GPUIndex:  key.GPUIndex,
			}
			byPodGPU[key] = metric
		}

		metric.MemoryBytes += process.UsedGPUMemoryBytes
		metric.SMUtilizationPercent += float64(process.SMUtilizationPercent)
	}

	for _, container := range pods.ActiveContainers() {
		for _, device := range container.GPUDevices {
			key := s.idlePodGPUKey(container, device)
			if _, ok := byPodGPU[key]; ok {
				continue
			}
			if gpuObservedForPodDevice(observedPodDevices, key) {
				continue
			}
			byPodGPU[key] = idlePodGPUMetric(key)
		}
	}

	out := make([]PodGPUMetric, 0, len(byPodGPU))
	for _, metric := range byPodGPU {
		// SM utilization is summed from per-process NVML samples, each a 0-100
		// time fraction. Concurrent processes (the common MPS case) can push the
		// sum above 100, so clamp it to keep the exported percentage well-formed.
		if metric.SMUtilizationPercent > 100 {
			metric.SMUtilizationPercent = 100
		}
		out = append(out, *metric)
	}
	return out, unmatched
}

// windowedSMUtil replaces each metric's SMUtilizationPercent with the average
// of the last smUtilWindowSize samples (same approach as runai-container-toolkit:
// window_size = max(1, window/interval)). Called only when smUtilWindowSize > 1.
// Buffer entries for series no longer present are pruned immediately.
func (s *metricsController) windowedSMUtil(metrics []PodGPUMetric) []PodGPUMetric {
	current := make(map[podGPUKey]struct{}, len(metrics))
	for i, m := range metrics {
		key := podGPUKeyForMetric(m)
		current[key] = struct{}{}
		metrics[i].SMUtilizationPercent = s.pushSample(key, m.SMUtilizationPercent)
	}

	// Prune entries for series that disappeared (pod deleted, process exited).
	for key := range s.smUtilBuf {
		if _, ok := current[key]; !ok {
			delete(s.smUtilBuf, key)
		}
	}
	return metrics
}

// pushSample appends sample to the ring buffer for key, trims it to the window
// size, and returns the average of the retained samples.
func (s *metricsController) pushSample(key podGPUKey, sample float64) float64 {
	buf := append(s.smUtilBuf[key], sample)
	if len(buf) > s.smUtilWindowSize {
		buf = buf[len(buf)-s.smUtilWindowSize:]
	}
	s.smUtilBuf[key] = buf
	var sum float64
	for _, v := range buf {
		sum += v
	}
	return sum / float64(len(buf))
}

func (s *metricsController) rememberDeviceUUIDs(deviceUUIDs map[int]string) {
	for index, uuid := range deviceUUIDs {
		if uuid == "" {
			continue
		}
		s.deviceUUIDs[index] = uuid
	}
}

func (s *metricsController) idlePodGPUKey(container store.ContainerInfo, device store.GPUDevice) podGPUKey {
	// The NRI backend identifies a device by index and learns its UUID from the
	// NVML collect; the PodResources backend supplies the UUID directly (and we
	// recover the index from NVML when it is known) since it has no device index.
	uuid := device.UUID
	index := device.Index
	if uuid == "" {
		uuid = s.deviceUUIDs[device.Index]
	} else if resolved, ok := s.indexForUUID(uuid); ok {
		index = resolved
	}
	return podGPUKey{
		Namespace: container.Namespace,
		Pod:       container.Pod,
		PodUID:    container.PodUID,
		GPUUUID:   uuid,
		GPUIndex:  index,
	}
}

func (s *metricsController) indexForUUID(uuid string) (int, bool) {
	for index, deviceUUID := range s.deviceUUIDs {
		if deviceUUID == uuid {
			return index, true
		}
	}
	return 0, false
}

func podGPUKeyForMetric(m PodGPUMetric) podGPUKey {
	return podGPUKey{Namespace: m.Namespace, Pod: m.Pod, PodUID: m.PodUID, GPUUUID: m.GPUUUID, GPUIndex: m.GPUIndex}
}

func idlePodGPUMetric(key podGPUKey) *PodGPUMetric {
	return &PodGPUMetric{
		Namespace: key.Namespace,
		Pod:       key.Pod,
		PodUID:    key.PodUID,
		GPUUUID:   key.GPUUUID,
		GPUIndex:  key.GPUIndex,
	}
}

func observePodDevice(observed map[string]struct{}, key podGPUKey) {
	if key.GPUUUID != "" {
		observed[podDeviceUUIDKey(key)] = struct{}{}
	}
	observed[podDeviceIndexKey(key)] = struct{}{}
}

func gpuObservedForPodDevice(observed map[string]struct{}, key podGPUKey) bool {
	if key.GPUUUID != "" {
		if _, ok := observed[podDeviceUUIDKey(key)]; ok {
			return true
		}
	}
	_, ok := observed[podDeviceIndexKey(key)]
	return ok
}

func podDeviceUUIDKey(key podGPUKey) string {
	return key.Namespace + "/" + key.PodUID + "/uuid/" + key.GPUUUID
}

func podDeviceIndexKey(key podGPUKey) string {
	return key.Namespace + "/" + key.PodUID + "/index/" + strconv.Itoa(key.GPUIndex)
}

func cloneSnapshot(in Snapshot) Snapshot {
	return Snapshot{
		Metrics:       append([]PodGPUMetric(nil), in.Metrics...),
		ActivePodUIDs: maps.Clone(in.ActivePodUIDs),
	}
}
