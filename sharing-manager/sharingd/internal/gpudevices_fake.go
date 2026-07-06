//go:build e2e

package internal

import (
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/fakegpu"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/mapping/store"

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
