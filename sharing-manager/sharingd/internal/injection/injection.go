// Package injection holds the "output" contract of the sharingd NRI plugin: the
// environment variables the CreateContainer hook injects into GPU-sharing
// containers. It is the counterpart to the annotations package (the "input"
// read from the pod) and exists so the injection side (internal/plugin.go
// buildAdjustment) and the verification side (internal/audit) reference one
// source of truth instead of duplicating the string literals.
package injection

// Injected env-var keys the create hook sets on a GPU-sharing container.
const (
	EnvGPUMemoryRequests = "NVIDIA_GPU_MEMORY_REQUESTS"
	EnvGPUMemoryLimits   = "NVIDIA_GPU_MEMORY_LIMITS"
	EnvMPSPipeDirectory  = "CUDA_MPS_PIPE_DIRECTORY"

	// EnvVisibleDevices selects the physical GPU(s) the NVIDIA container runtime
	// exposes to the container. The create hook sets it from the scheduler's
	// device-assignment annotation (annotations.VisibleDevicesAnnotation) so a
	// fractional GPU container — which does not request the nvidia.com/gpu
	// resource and is therefore skipped by the NVIDIA device plugin — still sees
	// exactly the GPU the scheduler picked. Unlike the keys above, it is injected
	// only when that annotation is present, so it is not part of the unconditional
	// injection contract.
	EnvVisibleDevices = "NVIDIA_VISIBLE_DEVICES"
)

// AllEnvKeys lists every env-var key the create hook may inject. Consumers that
// need to recognize injected env (e.g. the audit's presentEnv) should range over
// this single list so a newly added key is never missed in one place while being
// added in another.
var AllEnvKeys = []string{
	EnvGPUMemoryRequests,
	EnvGPUMemoryLimits,
	EnvMPSPipeDirectory,
	EnvVisibleDevices,
}
