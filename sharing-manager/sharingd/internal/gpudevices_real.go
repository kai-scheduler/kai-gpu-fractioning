//go:build !e2e

package internal

import (
	"github.com/kai-scheduler/gpu-sharing/sharing-manager/common/mapping/store"
	"github.com/kai-scheduler/gpu-sharing/sharing-manager/sharingd/internal/realgpu"

	"github.com/containerd/nri/pkg/api"
)

// gpuDevices is the production detector: GPUs come from real NVIDIA device nodes.
// The fake-GPU (env-based) detector is selected instead under the `e2e` build tag
// (see gpudevices_fake.go) and never ships in the production binary.
func gpuDevices(container *api.Container) []store.GPUDevice {
	return realgpu.GPUDevices(container)
}
