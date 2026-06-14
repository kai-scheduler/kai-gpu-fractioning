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
