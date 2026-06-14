//go:build !e2e

package plugin

import (
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/plugin/realgpu"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/store"

	"github.com/containerd/nri/pkg/api"
)

// gpuDevices is the production detector: GPUs come from real NVIDIA device nodes.
// The fake-GPU (env-based) detector is selected instead under the `e2e` build tag
// (see gpudevices_fake.go) and never ships in the production binary.
func gpuDevices(container *api.Container) []store.GPUDevice {
	return realgpu.GPUDevices(container)
}
