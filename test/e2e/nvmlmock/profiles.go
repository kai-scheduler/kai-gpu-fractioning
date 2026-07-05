package nvmlmock

import (
	"sort"
	"strings"
)

// Profile keys — the short GPU names callers pass to SetProcesses.
const (
	A100 = "a100"
	// DefaultGPU is the profile key SetProcesses uses when a caller passes "".
	DefaultGPU = A100
)

// GPUProfile holds the device-level attributes nvml-mock reports for one GPU
// model — the product name/versions (nvmlDeviceGetName, nvmlSystemGet*Version)
// and the framebuffer figures for nvmlDeviceGetMemoryInfo (the v2
// total/reserved/free/used struct). These are device-wide values that keep
// nvidia-smi / device queries realistic; they do NOT drive per-pod attribution
// — that comes from each Proc.UsedGPUMemory, a per-process figure surfaced via
// nvmlDeviceGetComputeRunningProcesses.
//
// To support another GPU type, add an entry to Profiles keyed by a short name
// and pass that key to SetProcesses.
type GPUProfile struct {
	// Name is the NVML product name, e.g. "NVIDIA A100-SXM4-40GB".
	Name string

	// DriverVersion / NVMLVersion / CUDAVersion populate the config's system
	// block.
	DriverVersion string
	NVMLVersion   string
	CUDAVersion   string

	// MemoryTotalBytes is the full framebuffer (nvmlMemory.total).
	MemoryTotalBytes uint64
	// MemoryReservedBytes is FB reserved by the driver/ECC (v2 "reserved").
	MemoryReservedBytes uint64
	// MemoryFreeBytes + MemoryUsedBytes should sum to MemoryTotalBytes.
	MemoryFreeBytes uint64
	MemoryUsedBytes uint64
}

// Profiles map a short GPU key to its device-level profile. Keys are what
// callers pass to SetProcesses; extend this map to add GPU types.
var Profiles = map[string]GPUProfile{
	// NVIDIA A100-SXM4-40GB. Memory mirrors
	// sharing-manager/metricsd/deploy/fake-gpu-cluster/nvml-mock.yaml:
	//   total = 40 GiB (40 * 1024^3 = 42949672960)
	//   reserved = 583 MiB (583 * 1024^2 = 611319808), driver/ECC-reserved FB
	//   free (~30.3 GiB) + used (~9.7 GiB) == total, so ~9.7 GiB presents in use.
	A100: {
		Name:                "NVIDIA A100-SXM4-40GB",
		DriverVersion:       "550.163.01",
		NVMLVersion:         "12.550.163.01",
		CUDAVersion:         "12.4",
		MemoryTotalBytes:    42949672960,
		MemoryReservedBytes: 611319808,
		MemoryFreeBytes:     32519249920,
		MemoryUsedBytes:     10430423040,
	},
}

// knownProfiles returns the sorted Profiles keys for a readable error message.
func knownProfiles() string {
	keys := make([]string, 0, len(Profiles))
	for k := range Profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
