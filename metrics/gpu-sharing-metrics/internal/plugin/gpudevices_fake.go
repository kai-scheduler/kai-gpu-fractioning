//go:build e2e

package plugin

import (
	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/plugin/fakegpu"
	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/store"

	"github.com/containerd/nri/pkg/api"
)

// gpuDevices is the TEST/DEV detector, compiled in only under the `e2e` build
// tag: GPUs come from the container's NVIDIA_VISIBLE_DEVICES /
// MOCK_NVIDIA_VISIBLE_DEVICES env var, so the mapping path can be exercised on
// fake-GPU clusters. The production build uses the device-node detector in
// gpudevices_real.go instead.
func gpuDevices(container *api.Container) []store.GPUDevice {
	return fakegpu.GPUDevices(container)
}
