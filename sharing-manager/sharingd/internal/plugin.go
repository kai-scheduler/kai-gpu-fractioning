package internal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/events"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/store"
)

const (
	// Environment variables injected into containers by the NRI plugin.
	envGPUMemoryRequests = "NVIDIA_GPU_MEMORY_REQUESTS"
	envGPUMemoryLimits   = "NVIDIA_GPU_MEMORY_LIMITS"
	envMPSPipeDirectory  = "CUDA_MPS_PIPE_DIRECTORY"

	// DefaultPluginName NRI plugin registration defaults.
	DefaultPluginName = "gpu-sharing"
	DefaultPluginIdx  = "10"
)

// Config configures a Plugin. The first fields drive container mutation (the
// existing GPU-memory/MPS behaviour); the mapping fields drive the container→pod
// mapping handoff consumed by the metricsd sidecar.
type Config struct {
	AnnotationPrefix string // annotation prefix for GPU memory config (mutation)
	MPSPipeDirectory string // MPS pipe directory bind-mounted into GPU containers
	FailOpen         bool   // skip container on parse error instead of blocking

	MapDir       string // shared dir for the container→pod mapping handoff
	LogPodEvents bool   // log each recorded/removed mapping event

	Log *slog.Logger
}

// Plugin implements the GPU sharing NRI handler logic. It has two independent
// jobs on the container lifecycle:
//
//  1. Mutation: on CreateContainer it evaluates the pod's GPU-memory annotations
//     and injects the NVIDIA_GPU_MEMORY_* env vars, CUDA_MPS_PIPE_DIRECTORY, and
//     the MPS pipe bind mount.
//  2. Mapping: it records a container→pod mapping (plus assigned GPU devices and
//     requested fraction) to a shared directory via an async events processor and
//     fsstore writer. The metricsd sidecar reads that mapping to attribute GPU
//     processes to pods. The mapping path never mutates the container.
//
// The mapping work stays off the NRI hot path: handlers capture the runtime
// objects in a closure and hand it to the events processor, which does the
// api.* → store.ContainerInfo conversion on its own worker goroutine and drops
// events rather than blocking if it falls behind. Mapping failures are
// fire-and-forget, so they can never break container mutation.
//
// It satisfies stub.ConfigureInterface, stub.SynchronizeInterface,
// stub.CreateContainerInterface, stub.RemoveContainerInterface and
// stub.ShutdownInterface.
type Plugin struct {
	AnnotationPrefix string
	MPSPipeDirectory string
	FailOpen         bool
	Log              *slog.Logger

	events  *events.Processor
	adapter adapter
}

// NewPlugin creates a Plugin from cfg. Empty mapping defaults are filled in so a
// minimal caller still gets a working handoff directory.
func NewPlugin(cfg Config) *Plugin {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	mapDir := cfg.MapDir
	if mapDir == "" {
		mapDir = fsstore.DefaultMapDir
	}

	writer := fsstore.NewWriter(mapDir, log)
	logPodEvents := cfg.LogPodEvents
	proc := events.NewProcessor(writer, log, events.Options{
		LogEvents: func() bool { return logPodEvents },
	})

	return &Plugin{
		AnnotationPrefix: cfg.AnnotationPrefix,
		MPSPipeDirectory: cfg.MPSPipeDirectory,
		FailOpen:         cfg.FailOpen,
		Log:              log,
		events:           proc,
		adapter:          adapter{annotationPrefix: cfg.AnnotationPrefix},
	}
}

// Configure subscribes to every NRI event this plugin implements (returning a
// zero event mask asks the runtime for all of them). The plugin does not consume
// NRI-provided configuration.
func (p *Plugin) Configure(_ context.Context, _, _, _ string) (api.EventMask, error) {
	return 0, nil
}

// Synchronize rebuilds the full container→pod mapping from the runtime's current
// container set on (re)connect. The conversion runs on the events worker; the
// handler returns no container updates (this plugin does not mutate on sync).
func (p *Plugin) Synchronize(_ context.Context, pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	p.events.Synchronize(func() []store.ContainerInfo {
		return p.adapter.containers(pods, containers)
	})
	return nil, nil
}

// CreateContainer evaluates a container's pod annotations and returns an
// adjustment if GPU memory sharing is configured, and records the container→pod
// mapping for the metrics sidecar.
//
// If annotation parsing fails and FailOpen is true, the error is logged and nil
// is returned. If FailOpen is false (default), the error is returned and
// container creation is blocked — in which case the mapping is NOT recorded,
// since the container will not exist.
func (p *Plugin) CreateContainer(_ context.Context, pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	adj, err := p.buildAdjustment(pod, ctr)
	if err != nil {
		// Fail-closed: the runtime will refuse to create the container, so do
		// not record a mapping for it.
		return nil, nil, err
	}

	// The container will be created — record its mapping off the hot path. The
	// adapter drops non-fractional-GPU containers (ok=false), so this is a no-op
	// for containers the metrics sidecar does not care about.
	p.events.Upsert(func() (store.ContainerInfo, bool) {
		return p.adapter.container(pod, ctr)
	})

	return adj, nil, nil
}

// buildAdjustment contains the GPU-memory / MPS mutation logic. It returns a nil
// adjustment when the container has no GPU memory annotations, and an error only
// when annotation parsing fails while FailOpen is false.
func (p *Plugin) buildAdjustment(pod *api.PodSandbox, ctr *api.Container) (*api.ContainerAdjustment, error) {
	gpuMemoryCfg, err := ParseGPUMemoryAnnotations(pod.Annotations, ctr.Name, p.AnnotationPrefix)
	if err != nil {
		p.Log.Warn("failed to parse GPU memory annotations",
			"container", ctr.Name,
			"pod", pod.Name,
			"error", err,
		)
		if !p.FailOpen {
			return nil, fmt.Errorf("container %q in pod %q: %w", ctr.Name, pod.Name, err)
		}
		return nil, nil
	}

	if gpuMemoryCfg.IsEmpty() {
		p.Log.Debug("no GPU memory annotations", "container", ctr.Name, "pod", pod.Name)
		return nil, nil
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

	return adj, nil
}

// RemoveContainer drops the container's mapping when the runtime removes it.
func (p *Plugin) RemoveContainer(_ context.Context, _ *api.PodSandbox, ctr *api.Container) error {
	p.events.Delete(ctr.GetId())
	return nil
}

// Shutdown flushes any queued mapping events when the runtime disconnects.
func (p *Plugin) Shutdown(_ context.Context) {
	p.events.Flush()
}

// Flush blocks until all queued mapping events have been applied. Used by tests
// and graceful shutdown.
func (p *Plugin) Flush() {
	p.events.Flush()
}
