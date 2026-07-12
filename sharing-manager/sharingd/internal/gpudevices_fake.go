//go:build e2e

package internal

import (
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/store"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/fakegpu"

	"github.com/containerd/nri/pkg/api"
)

// TODO(P0): Eliminate this real-vs-fake build fork. e2e should run the same
// realgpu device-node path as production; fakeness belongs in the e2e/hack cluster
// setup, not in a compiled-in detector.
//
// gpuDevices is the TEST/DEV detector, compiled in only under the `e2e` build
// tag: GPUs come from the container's NVIDIA_VISIBLE_DEVICES /
// MOCK_NVIDIA_VISIBLE_DEVICES env var, so the mapping path can be exercised on
// fake-GPU clusters. The production build uses the device-node detector in
// gpudevices_real.go instead.
func gpuDevices(container *api.Container) []store.GPUDevice {
	return fakegpu.GPUDevices(container)
}
