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
)
