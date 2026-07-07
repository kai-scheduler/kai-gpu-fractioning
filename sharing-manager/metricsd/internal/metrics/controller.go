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

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/store"
)

const DefaultPath = "/metrics"

const (
	// maxSMUtilPercent is the upper bound for a well-formed SM utilization
	// percentage; sums and normalized values are clamped to it.
	maxSMUtilPercent = 100
	// fullGPUFraction is the fallback used when a pod's requested fraction is
	// unknown, treating it as if it requested the whole GPU.
	fullGPUFraction = 1
)

type metricsController struct {
	mu               sync.RWMutex            // protects snapshot
	collector        GPUProcessCollector     // NVML or noop source of per-process GPU metrics
	pods             podSource               // resolves GPU processes to pod/container identity
	interval         time.Duration           // how often collect() fires
	smUtilWindow     time.Duration           // smoothing window duration for SM utilisation
	smUtilWindowSize int                     // window in number of samples (derived: smUtilWindow/interval)
	smUtilBuf        map[podGPUKey][]float64 // rolling sample buffer per pod×GPU; nil when windowSize == 1
	log              *slog.Logger
	deviceUUIDs      map[int]string // GPU index → UUID learned from NVML; fills UUID for NRI-sourced devices
	snapshot         Snapshot       // latest published snapshot, read by Snapshot()
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
func (s *metricsController) Snapshot(_ context.Context) (Snapshot, error) {
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
	s.normalizeSMUtil(metrics)
	s.setSnapshot(metrics, activePodUIDs)
	s.log.DebugContext(ctx, "completed GPU metrics collect", "podMetrics", len(metrics), "unmatchedGPUProcesses", unmatched)
	for _, m := range metrics {
		s.log.DebugContext(ctx, "pod GPU metric",
			"pod", m.Namespace+"/"+m.Pod,
			"gpu_uuid", m.GPUUUID,
			"gpu_index", m.GPUIndex,
			"memory_bytes", m.MemoryBytes,
			"sm_utilization_percent", m.SMUtilizationPercent,
			"sm_utilization_percent_normalized", m.SMUtilizationPercentNormalized,
			"requested_gpu_fraction", m.RequestedGPUFraction,
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
	// requestedByKey sums each pod×GPU's requested GPU fraction across its
	// containers, deduped by container ID so multiple GPU processes of one
	// container are not counted more than once.
	requestedByKey := map[podGPUKey]map[string]float64{}
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
		recordRequestedFraction(requestedByKey, key, container.ContainerID, container.RequestedGPUFraction)
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
			recordRequestedFraction(requestedByKey, key, container.ContainerID, container.RequestedGPUFraction)
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
	for key, metric := range byPodGPU {
		// SM utilization is summed from per-process NVML samples, each a 0-100
		// time fraction. Concurrent processes (the common MPS case) can push the
		// sum above 100, so clamp it to keep the exported percentage well-formed.
		if metric.SMUtilizationPercent > maxSMUtilPercent {
			metric.SMUtilizationPercent = maxSMUtilPercent
		}
		metric.RequestedGPUFraction = sumRequestedFraction(requestedByKey[key])
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

// normalizeSMUtil sets each metric's SMUtilizationPercentNormalized to its SM
// utilization divided by the requested GPU fraction, capped at 100.
func (s *metricsController) normalizeSMUtil(metrics []PodGPUMetric) {
	for i := range metrics {
		metrics[i].SMUtilizationPercentNormalized = normalizedSMUtil(
			metrics[i].SMUtilizationPercent,
			metrics[i].RequestedGPUFraction,
		)
	}
}

// normalizedSMUtil computes smUtil ÷ fraction, capped at 100. When the requested
// fraction is unknown (0) it falls back to a fraction of 1 — i.e. the pod is
// treated as if it requested the whole GPU, so the normalized value equals the
// raw SM utilization rather than a misleading 0. Negative fractions are already
// filtered to 0 at ingest (adapter.requestedGPUFraction), so they cannot reach here.
func normalizedSMUtil(smUtil, fraction float64) float64 {
	if fraction == 0 {
		fraction = fullGPUFraction
	}
	normalized := smUtil / fraction
	if normalized > maxSMUtilPercent {
		return maxSMUtilPercent
	}
	return normalized
}

// recordRequestedFraction notes containerID's requested GPU fraction under key,
// deduped by container ID so repeated observations of the same container (one
// per GPU process) do not inflate the pod's requested total.
func recordRequestedFraction(requestedByKey map[podGPUKey]map[string]float64, key podGPUKey, containerID string, fraction float64) {
	if fraction <= 0 || containerID == "" {
		return
	}
	byContainer := requestedByKey[key]
	if byContainer == nil {
		byContainer = map[string]float64{}
		requestedByKey[key] = byContainer
	}
	byContainer[containerID] = fraction
}

// sumRequestedFraction totals the per-container requested GPU fraction recorded
// for a pod×GPU key.
func sumRequestedFraction(byContainer map[string]float64) float64 {
	var sum float64
	for _, fraction := range byContainer {
		sum += fraction
	}
	return sum
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
