package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

const nvmlValueNotAvailable = ^uint64(0)

type nvmlProcessCollector struct {
	mu           sync.RWMutex
	snapshot     GPUProcessSnapshot
	interval     time.Duration
	lastPollTime time.Time
	deviceUUIDs  map[int]string
	log          *slog.Logger
}

type gpuProcessKey struct {
	GPUUUID string
	PID     uint32
}

func newNVMLProcessCollector(interval time.Duration, logger *slog.Logger) (*nvmlProcessCollector, error) {
	ret := nvml.Init()
	if !errors.Is(ret, nvml.SUCCESS) && !errors.Is(ret, nvml.ERROR_ALREADY_INITIALIZED) {
		return nil, fmt.Errorf("initialize NVML: %s", ret.Error())
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &nvmlProcessCollector{
		interval:    interval,
		deviceUUIDs: map[int]string{},
		log:         logger,
	}, nil
}

func (c *nvmlProcessCollector) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.collect(time.Now())

	for {
		select {
		case <-ticker.C:
			c.collect(time.Now())
		case <-ctx.Done():
			return
		}
	}
}

func (c *nvmlProcessCollector) Snapshot() GPUProcessSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneGPUProcessSnapshot(c.snapshot)
}

func (c *nvmlProcessCollector) Close() error {
	ret := nvml.Shutdown()
	if !errors.Is(ret, nvml.SUCCESS) && !errors.Is(ret, nvml.ERROR_UNINITIALIZED) {
		return fmt.Errorf("shutdown NVML: %s", ret.Error())
	}
	return nil
}

func (c *nvmlProcessCollector) collect(now time.Time) {
	last := c.lastPollTime
	if last.IsZero() {
		last = now.Add(-c.interval)
	}
	c.lastPollTime = now
	lastSeenTimestamp := uint64(last.UnixMicro())

	count, ret := nvml.DeviceGetCount()
	if !errors.Is(ret, nvml.SUCCESS) {
		// Total failure — keep previous snapshot rather than blanking it out.
		c.log.Debug("NVML DeviceGetCount failed; retaining previous snapshot", "nvml_return", ret)
		return
	}

	joined := map[gpuProcessKey]GPUProcessMetric{}
	deviceUUIDs := map[int]string{}
	deviceMinorToNVMLIndex := map[int]int{}
	deviceTotalMemory := map[int]uint64{}
	var errs []error

	for index := range count {
		device, ret := nvml.DeviceGetHandleByIndex(index)
		if !errors.Is(ret, nvml.SUCCESS) {
			errs = append(errs, fmt.Errorf("get NVML device %d: %s", index, ret.Error()))
			continue
		}
		uuid, err := c.deviceUUID(index, device)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		deviceUUIDs[index] = uuid
		if minorNumber, ret := device.GetMinorNumber(); errors.Is(ret, nvml.SUCCESS) {
			deviceMinorToNVMLIndex[minorNumber] = index
		}

		if total, err := deviceTotalMemoryBytes(device); err != nil {
			errs = append(errs, fmt.Errorf("get memory info for GPU %s: %w", uuid, err))
		} else {
			deviceTotalMemory[index] = total
		}

		if err := collectProcessUtilization(joined, uuid, index, lastSeenTimestamp, device, c.log); err != nil {
			errs = append(errs, err)
		}
		if err := collectProcessMemory(joined, uuid, index, device, c.log); err != nil {
			errs = append(errs, err)
		}
	}

	processes := make([]GPUProcessMetric, 0, len(joined))
	for _, metric := range joined {
		processes = append(processes, metric)
	}

	c.setSnapshot(GPUProcessSnapshot{
		Processes:              processes,
		DeviceUUIDs:            deviceUUIDs,
		DeviceMinorToNVMLIndex: deviceMinorToNVMLIndex,
		DeviceTotalMemoryBytes: deviceTotalMemory,
		DeviceErrors:           errors.Join(errs...),
	})
}

// deviceTotalMemoryBytes returns the device's total GPU memory in bytes. It uses
// the v2 memory info call, which reports the full device memory (matching what
// the driver advertises), so the derived GPU fraction is stable across MIG and
// non-MIG devices.
func deviceTotalMemoryBytes(device nvml.Device) (uint64, error) {
	mem, ret := device.GetMemoryInfo()
	if !errors.Is(ret, nvml.SUCCESS) {
		return 0, errors.New(ret.Error())
	}
	return mem.Total, nil
}

func (c *nvmlProcessCollector) setSnapshot(s GPUProcessSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshot = s
}

func (c *nvmlProcessCollector) deviceUUID(index int, device nvml.Device) (string, error) {
	if uuid := c.deviceUUIDs[index]; uuid != "" {
		return uuid, nil
	}
	uuid, ret := device.GetUUID()
	if !errors.Is(ret, nvml.SUCCESS) {
		return "", fmt.Errorf("get NVML device %d UUID: %s", index, ret.Error())
	}
	c.deviceUUIDs[index] = uuid
	return uuid, nil
}

func collectProcessUtilization(joined map[gpuProcessKey]GPUProcessMetric, uuid string, index int, lastSeenTimestamp uint64, device nvml.Device, logger *slog.Logger) error {
	utilization, ret := device.GetProcessUtilization(lastSeenTimestamp)
	if !errors.Is(ret, nvml.SUCCESS) && !errors.Is(ret, nvml.ERROR_NOT_SUPPORTED) && !errors.Is(ret, nvml.ERROR_NOT_FOUND) {
		return fmt.Errorf("get process utilization for GPU %s: %s", uuid, ret.Error())
	}
	logger.Debug("NVML process utilization samples", "gpu_uuid", uuid, "count", len(utilization), "nvml_return", ret)

	for _, sample := range utilization {
		if shouldSkipProcessUtilizationSample(sample, lastSeenTimestamp) {
			continue
		}
		logger.Debug("NVML process utilization", "gpu_uuid", uuid, "pid", sample.Pid, "sm_util", sample.SmUtil, "timestamp", sample.TimeStamp)
		key := gpuProcessKey{GPUUUID: uuid, PID: sample.Pid}
		metric := joined[key]
		metric.PID = sample.Pid
		metric.GPUUUID = uuid
		metric.GPUIndex = index
		metric.SMUtilizationPercent = sample.SmUtil
		metric.ProcessUtilizationTimestamp = sample.TimeStamp
		joined[key] = metric
	}
	return nil
}

func collectProcessMemory(joined map[gpuProcessKey]GPUProcessMetric, uuid string, index int, device nvml.Device, logger *slog.Logger) error {
	if err := mergeRunningProcesses(joined, uuid, index, "compute", device.GetComputeRunningProcesses, logger); err != nil {
		return fmt.Errorf("get compute processes for GPU %s: %w", uuid, err)
	}
	if err := mergeRunningProcesses(joined, uuid, index, "graphics", device.GetGraphicsRunningProcesses, logger); err != nil {
		return fmt.Errorf("get graphics processes for GPU %s: %w", uuid, err)
	}
	return nil
}

// shouldSkipProcessUtilizationSample filters out three classes of NVML quirks:
//   - PID == 0: sentinel entry returned when no process is running on the device.
//   - Util > 100: NVML returns 0xFFFFFFFF as an error sentinel when utilization
//     data is unavailable for a process; these are not real percentages.
//   - TimeStamp <= lastSeenTimestamp: GetProcessUtilization returns a rolling ring
//     buffer of recent samples, not just new ones. Without this guard every collect
//     would reprocess the same historical samples and double-count SM utilization.
func shouldSkipProcessUtilizationSample(sample nvml.ProcessUtilizationSample, lastSeenTimestamp uint64) bool {
	return sample.Pid == 0 ||
		sample.SmUtil > 100 ||
		sample.MemUtil > 100 ||
		sample.EncUtil > 100 ||
		sample.DecUtil > 100 ||
		sample.TimeStamp <= lastSeenTimestamp
}

func mergeRunningProcesses(joined map[gpuProcessKey]GPUProcessMetric, uuid string, index int, kind string, list func() ([]nvml.ProcessInfo, nvml.Return), logger *slog.Logger) error {
	processes, ret := list()
	logger.Debug("NVML running processes", "gpu_uuid", uuid, "kind", kind, "count", len(processes), "nvml_return", ret)
	if !errors.Is(ret, nvml.SUCCESS) && !errors.Is(ret, nvml.ERROR_NOT_SUPPORTED) && !errors.Is(ret, nvml.ERROR_NOT_FOUND) {
		return errors.New(ret.Error())
	}
	for _, process := range processes {
		logger.Debug("NVML process", "gpu_uuid", uuid, "kind", kind, "pid", process.Pid, "used_gpu_memory_bytes", process.UsedGpuMemory)
		key := gpuProcessKey{GPUUUID: uuid, PID: process.Pid}
		metric := joined[key]
		metric.PID = process.Pid
		metric.GPUUUID = uuid
		metric.GPUIndex = index
		if process.UsedGpuMemory != nvmlValueNotAvailable && process.UsedGpuMemory > metric.UsedGPUMemoryBytes {
			metric.UsedGPUMemoryBytes = process.UsedGpuMemory
		}
		joined[key] = metric
	}
	return nil
}

func cloneGPUProcessSnapshot(in GPUProcessSnapshot) GPUProcessSnapshot {
	deviceUUIDs := map[int]string{}
	for k, v := range in.DeviceUUIDs {
		deviceUUIDs[k] = v
	}
	minorToNVMLIndex := map[int]int{}
	for k, v := range in.DeviceMinorToNVMLIndex {
		minorToNVMLIndex[k] = v
	}
	deviceTotalMemory := map[int]uint64{}
	for k, v := range in.DeviceTotalMemoryBytes {
		deviceTotalMemory[k] = v
	}
	return GPUProcessSnapshot{
		Processes:              append([]GPUProcessMetric(nil), in.Processes...),
		DeviceUUIDs:            deviceUUIDs,
		DeviceMinorToNVMLIndex: minorToNVMLIndex,
		DeviceTotalMemoryBytes: deviceTotalMemory,
		DeviceErrors:           in.DeviceErrors,
	}
}

// newCollector tries to initialise an NVML-backed collector. When NVML is not
// available we fall back to a no-op collector instead of failing hard, because
// this binary also runs on fake-GPU test clusters that have no driver installed.
// On those nodes the exporter still starts and emits pod-label metrics with zero
// GPU counters, which is useful for integration tests. On real production nodes
// NVML is always present, so the fallback path is exercised only in CI.
func newCollector(ctx context.Context, interval time.Duration, logger *slog.Logger) collector {
	if c, err := newNVMLProcessCollector(interval, logger); err == nil {
		logger.InfoContext(ctx, "NVML initialized; GPU process metrics enabled")
		return c
	} else {
		logger.WarnContext(ctx, "NVML unavailable; GPU process metrics will be zero (pod labels still reported)", "error", err)
		return &NoopCollector{}
	}
}
