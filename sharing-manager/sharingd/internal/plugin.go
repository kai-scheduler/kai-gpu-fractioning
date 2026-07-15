package internal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/store"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/annotations"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/audit"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/events"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/injection"
)

// ReadinessSetter receives the plugin's NRI registration state: true once the
// runtime delivers Synchronize, false when it disconnects. *readiness.State
// implements it; the plugin only needs the setter half, so it depends on this
// interface rather than the readiness package.
type ReadinessSetter interface {
	SetReady(bool)
}

const (
	// DefaultPluginName NRI plugin registration defaults.
	DefaultPluginName = "gpu-sharing"
	DefaultPluginIdx  = "10"
)

// Config configures a Plugin. The first fields drive container mutation (the
// existing GPU-memory/MPS behaviour); the mapping fields drive the container→pod
// mapping handoff consumed by the metricsd sidecar.
type Config struct {
	// AnnotationPrefix is the annotation prefix for GPU memory config (mutation).
	AnnotationPrefix string
	// MPSPipeDirectory is the MPS pipe directory bind-mounted into GPU containers.
	MPSPipeDirectory string
	// FailOpen skips a container on parse error instead of blocking it.
	FailOpen bool

	// RetroactiveEnforcement enables the audit pass on NRI (re)connect: any
	// GPU-sharing container found running without the expected injection is
	// stopped so kubelet recreates it through a healthy CreateContainer hook.
	// Requires a non-nil stopper passed to NewPlugin; otherwise NewPlugin
	// returns an error.
	RetroactiveEnforcement bool

	// MapDir is the shared dir for the container→pod mapping handoff.
	MapDir string
	// LogPodEvents logs each recorded/removed mapping event.
	LogPodEvents bool

	// Log is the logger used by the plugin; defaults to slog.Default() when nil.
	Log *slog.Logger

	// Readiness, when non-nil, is flipped to ready on Synchronize (the runtime
	// delivered the full container state, so registration succeeded) and back to
	// not-ready on Shutdown (the runtime is disconnecting).
	Readiness ReadinessSetter
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

	events    *events.Processor
	adapter   adapter
	readiness ReadinessSetter

	// sentinel runs retroactive enforcement on Synchronize; nil when disabled.
	sentinel *audit.Sentinel
}

// NewPlugin creates a Plugin from cfg. Empty mapping defaults are filled in so a
// minimal caller still gets a working handoff directory.
//
// stopper backs retroactive enforcement: when cfg.RetroactiveEnforcement is true
// the plugin audits each NRI Synchronize snapshot and stops GPU-sharing
// containers missing injection. Enabling the flag without a stopper is a
// misconfiguration and returns an error rather than silently doing nothing;
// when the flag is off, stopper is ignored (pass nil).
func NewPlugin(cfg Config, stopper audit.ContainerStopper) (*Plugin, error) {
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

	var sentinel *audit.Sentinel
	if cfg.RetroactiveEnforcement {
		if stopper == nil {
			return nil, fmt.Errorf("retroactive enforcement enabled but no container stopper was provided")
		}
		sentinel = audit.NewSentinel(cfg.AnnotationPrefix, cfg.MPSPipeDirectory, stopper, log)
	}

	return &Plugin{
		AnnotationPrefix: cfg.AnnotationPrefix,
		MPSPipeDirectory: cfg.MPSPipeDirectory,
		FailOpen:         cfg.FailOpen,
		Log:              log,
		events:           proc,
		adapter:          adapter{annotationPrefix: cfg.AnnotationPrefix, log: log},
		readiness:        cfg.Readiness,
		sentinel:         sentinel,
	}, nil
}

// setReady updates the shared readiness state, if one was configured.
func (p *Plugin) setReady(ready bool) {
	if p.readiness != nil {
		p.readiness.SetReady(ready)
	}
}

// Configure subscribes to every NRI event this plugin implements (returning a
// zero event mask asks the runtime for all of them). The plugin does not consume
// NRI-provided configuration.
func (p *Plugin) Configure(ctx context.Context, _, runtime, version string) (api.EventMask, error) {
	p.Log.InfoContext(ctx, "configured NRI plugin", "runtime", runtime, "runtimeVersion", version)
	return 0, nil
}

// Synchronize rebuilds the full container→pod mapping from the runtime's current
// container set on (re)connect. Pre-existing pods arrive here, not via
// CreateContainer. The conversion runs on the events worker; the handler returns
// no container updates (this plugin does not mutate on sync).
//
// Synchronize only fires after the plugin successfully registered with the
// runtime, so it doubles as the readiness signal.
//
// When retroactive enforcement is enabled it also audits the snapshot for
// GPU-sharing containers that started without injection (while the agent was
// down) and stops them off the hot path. Detection is synchronous and cheap;
// the stopping happens on a background goroutine so it never stalls this NRI
// callback.
func (p *Plugin) Synchronize(ctx context.Context, pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	p.Log.InfoContext(ctx, "synchronizing container mapping with runtime",
		"pods", len(pods), "containers", len(containers))
	p.events.Synchronize(func() []store.ContainerInfo {
		infos := p.adapter.containers(pods, containers)
		p.Log.Info("rebuilt container mapping from runtime sync",
			"recordedContainers", len(infos), "totalContainers", len(containers))
		return infos
	})
	p.setReady(true)
	if p.sentinel != nil {
		p.sentinel.Audit(ctx, pods, containers)
	}
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
	gpuMemoryCfg, err := annotations.ParseGPUMemoryAnnotations(pod.Annotations, ctr.Name, p.AnnotationPrefix)
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
		adj.AddEnv(injection.EnvGPUMemoryRequests, gpuMemoryCfg.Request)
	}
	if gpuMemoryCfg.Limit != "" {
		adj.AddEnv(injection.EnvGPUMemoryLimits, gpuMemoryCfg.Limit)
	}
	adj.AddEnv(injection.EnvMPSPipeDirectory, p.MPSPipeDirectory)

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

// Shutdown flushes any queued mapping events and waits for in-flight
// remediation when the runtime disconnects, and marks the plugin not-ready until
// the next successful Synchronize.
func (p *Plugin) Shutdown(_ context.Context) {
	p.setReady(false)
	p.events.Flush()
	if p.sentinel != nil {
		p.sentinel.Wait()
	}
}

// Flush blocks until all queued mapping events have been applied and any
// in-flight remediation has finished. Used by tests and graceful shutdown.
func (p *Plugin) Flush() {
	p.events.Flush()
	if p.sentinel != nil {
		p.sentinel.Wait()
	}
}
