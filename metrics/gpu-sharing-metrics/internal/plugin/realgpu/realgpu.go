// Package realgpu is the production GPU detector. It identifies a container's
// GPUs from its Linux device nodes (NVIDIA char major 195), which are
// authoritative for device identity. It is selected by the plugin package's
// !e2e build (see gpudevices_real.go); the fake-GPU detector used for testing
// lives in the sibling fakegpu package.
package realgpu

import (
	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/store"

	"github.com/containerd/nri/pkg/api"
)

const (
	nvidiaDeviceMajor = 195
	maxNVIDIAGPUMinor = 32
)

// GPUDevices returns the GPU devices assigned to the container via Linux device
// nodes. Returns nil when no NVIDIA device nodes are present.
func GPUDevices(container *api.Container) []store.GPUDevice {
	return devicesFromNodes(container)
}

func devicesFromNodes(container *api.Container) []store.GPUDevice {
	linux := container.GetLinux()
	if linux == nil {
		return nil
	}

	devices := []store.GPUDevice{}
	seen := map[int]struct{}{}
	for _, device := range linux.GetDevices() {
		index, ok := gpuIndexFromLinuxDevice(device)
		if !ok {
			continue
		}
		if _, exists := seen[index]; exists {
			continue
		}
		seen[index] = struct{}{}
		devices = append(devices, store.GPUDevice{Index: index})
	}
	return devices
}

func gpuIndexFromLinuxDevice(device *api.LinuxDevice) (int, bool) {
	if device.GetMajor() != nvidiaDeviceMajor {
		return 0, false
	}
	minor := device.GetMinor()
	if minor < 0 || minor > maxNVIDIAGPUMinor {
		return 0, false
	}
	return int(minor), true
}
