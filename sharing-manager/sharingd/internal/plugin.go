package internal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/containerd/nri/pkg/api"
)

const (
	// Environment variables injected into containers by the NRI plugin.
	envGPUMemoryRequests = "NVIDIA_GPU_MEMORY_REQUESTS"
	envGPUMemoryLimits   = "NVIDIA_GPU_MEMORY_LIMITS"
	envMPSPipeDirectory  = "CUDA_MPS_PIPE_DIRECTORY"

	// NRI plugin registration defaults.
	DefaultPluginName = "gpu-sharing"
	DefaultPluginIdx  = "10"
)

// Plugin implements the GPU sharing NRI handler logic for container creation.
// It satisfies stub.CreateContainerInterface and stub.SynchronizeInterface.
type Plugin struct {
	AnnotationPrefix string
	MPSPipeDirectory string
	FailOpen         bool
	Log              *slog.Logger
}

// NewPlugin creates a Plugin with the given configuration.
// Callers are responsible for providing non-empty values; defaults are set via
// CLI flags in main.
func NewPlugin(annotationPrefix, mpsPipeDir string, failOpen bool, log *slog.Logger) *Plugin {
	return &Plugin{
		AnnotationPrefix: annotationPrefix,
		MPSPipeDirectory: mpsPipeDir,
		FailOpen:         failOpen,
		Log:              log,
	}
}

// Synchronize acknowledges existing containers. The plugin is stateless so
// there is nothing to rebuild.
func (p *Plugin) Synchronize(_ context.Context, _ []*api.PodSandbox, _ []*api.Container) ([]*api.ContainerUpdate, error) {
	return nil, nil
}

// CreateContainer evaluates a container's pod annotations and returns an
// adjustment if GPU memory sharing is configured. Returns nil adjustment if
// the container has no GPU memory annotations.
//
// If annotation parsing fails and FailOpen is true, the error is logged and
// nil is returned. If FailOpen is false (default), the error is returned and
// container creation is blocked.
func (p *Plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	gpuMemoryCfg, err := ParseGPUMemoryAnnotations(pod.Annotations, ctr.Name, p.AnnotationPrefix)
	if err != nil {
		p.Log.Warn("failed to parse GPU memory annotations",
			"container", ctr.Name,
			"pod", pod.Name,
			"error", err,
		)
		if !p.FailOpen {
			return nil, nil, fmt.Errorf("container %q in pod %q: %w", ctr.Name, pod.Name, err)
		}
		return nil, nil, nil
	}

	if gpuMemoryCfg.IsEmpty() {
		p.Log.Debug("no GPU memory annotations", "container", ctr.Name, "pod", pod.Name)
		return nil, nil, nil
	}

	adj := &api.ContainerAdjustment{}

	if gpuMemoryCfg.Request != "" {
		adj.AddEnv(envGPUMemoryRequests, gpuMemoryCfg.Request)
	}
	if gpuMemoryCfg.Limit != "" {
		adj.AddEnv(envGPUMemoryLimits, gpuMemoryCfg.Limit)
	}
	adj.AddEnv(envMPSPipeDirectory, p.MPSPipeDirectory)

	adj.AddMount(&api.Mount{
		Source:      p.MPSPipeDirectory,
		Destination: p.MPSPipeDirectory,
		Type:        "bind",
		Options:     []string{"bind", "rw"},
	})

	p.Log.Info("adjusting container with GPU memory config",
		"container", ctr.Name,
		"pod", pod.Name,
		"request", gpuMemoryCfg.Request,
		"limit", gpuMemoryCfg.Limit,
	)

	return adj, nil, nil
}
