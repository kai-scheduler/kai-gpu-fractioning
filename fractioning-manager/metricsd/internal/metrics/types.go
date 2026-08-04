// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import "context"

type GPUProcessMetric struct {
	PID                         uint32
	GPUUUID                     string
	GPUIndex                    int
	UsedGPUMemoryBytes          uint64
	SMUtilizationPercent        uint32
	ProcessUtilizationTimestamp uint64
}

type GPUProcessSnapshot struct {
	Processes   []GPUProcessMetric
	DeviceUUIDs map[int]string
	// DeviceMinorToNVMLIndex maps each GPU's Linux device minor number to its NVML
	// index. Populated by the NVML collector; empty on fake-GPU nodes where the noop
	// collector is used. The engine uses this to resolve NRI-sourced GPUDevices
	// (which carry the Linux minor number) to NVML indices so UUID lookup is correct
	// even when minor ≠ NVML index.
	DeviceMinorToNVMLIndex map[int]int
	// DeviceTotalMemoryBytes is each GPU's total memory in bytes, keyed by NVML
	// device index. The controller divides a pod's requested memory by this to
	// derive the GPU fraction used to normalize SM utilization. Empty for backends
	// that do not report it (e.g. the noop collector on fake-GPU nodes).
	DeviceTotalMemoryBytes map[int]uint64
	// DeviceErrors holds per-device collection failures. The snapshot is still
	// usable (partial) when this is non-nil.
	DeviceErrors error
}

type GPUProcessCollector interface {
	Snapshot() GPUProcessSnapshot
	Close() error
}

type PodGPUMetric struct {
	Namespace            string  `json:"namespace"`
	Pod                  string  `json:"pod"`
	PodUID               string  `json:"podUID"`
	GPUUUID              string  `json:"gpuUUID"`
	GPUIndex             int     `json:"gpuIndex"`
	MemoryBytes          uint64  `json:"memoryBytes"`
	SMUtilizationPercent float64 `json:"smUtilizationPercent"`
	// RequestedGPUFraction is the GPU fraction the pod holds on this device,
	// derived as (requested GPU memory ÷ device total memory) summed across its
	// containers. It is the divisor used to normalize SM utilization. Zero when no
	// request is known or the device's total memory is unknown.
	RequestedGPUFraction float64 `json:"requestedGpuFraction"`
	// SMUtilizationPercentNormalized is SMUtilizationPercent divided by the
	// requested GPU fraction and capped at 100. It expresses how fully the pod
	// uses what it asked for: a pod requesting 0.5 of a GPU and using 0.5 of it
	// reports 100. When the requested fraction is unknown it falls back to a
	// fraction of 1, so the value equals the raw SM utilization.
	SMUtilizationPercentNormalized float64 `json:"smUtilizationPercentNormalized"`
}

type Snapshot struct {
	Metrics       []PodGPUMetric      `json:"metrics"`
	ActivePodUIDs map[string]struct{} `json:"activePodUIDs"`
}

// SnapshotProvider is the seam between metric collection and metric exposition.
// The exporter depends on this interface, not on the concrete controller, so the
// two halves can be split into separate processes/pods later without code
// changes on the exporter side. The signature is intentionally network-shaped:
// it takes a context and may return an error, exactly as a remote client backed
// by HTTP or gRPC would. The in-process controller simply never errors.
type SnapshotProvider interface {
	Snapshot(ctx context.Context) (Snapshot, error)
}

type podGPUKey struct {
	Namespace string
	Pod       string
	PodUID    string
	GPUUUID   string
	GPUIndex  int
}
