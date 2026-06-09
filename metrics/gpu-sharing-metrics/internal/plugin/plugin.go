package plugin

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/events"
	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/fsstore"
	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/store"

	"github.com/containerd/nri/pkg/api"
)

type ContainerInfo = store.ContainerInfo
type GPUDevice = store.GPUDevice

// Plugin is a metrics-only NRI plugin: it never mutates containers. Its sole job
// is to feed the container→pod mapping (plus assigned GPU devices) that the
// metrics component consumes.
//
// The NRI handlers do as little as possible on the runtime's hot path: they
// capture the runtime objects into an adapter closure and hand it to the events
// processor, then return immediately so NRI can proceed. The processor runs the
// adapter on its own worker goroutine — turning the NRI api.* objects into a
// store.ContainerInfo ("calculating the mapping") off the hot path — and persists
// the result to the shared-filesystem mapping store. The metrics component reads
// those files independently.
//
// Capturing the api.* pointers in the closure is safe: the NRI stub decodes a
// fresh request per RPC (it does not pool or reuse the structs), so the objects
// remain valid for the worker to read after the handler returns.
type Plugin struct {
	mu      sync.RWMutex
	cfg     Config
	log     *slog.Logger
	events  *events.Processor
	adapter adapter
}

func New(cfg Config, logger *slog.Logger) *Plugin {
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.withDefaults()
	writer := fsstore.NewWriter(cfg.MapDir, logger)
	p := &Plugin{
		cfg: cfg,
		log: logger,
	}
	p.events = events.NewProcessor(writer, logger, events.Options{
		LogEvents: func() bool { return p.config().LogPodEvents },
	})
	return p
}

func (p *Plugin) Configure(ctx context.Context, config, runtime, version string) (api.EventMask, error) {
	if config != "" {
		cfg, err := ParseConfig([]byte(config))
		if err != nil {
			return 0, fmt.Errorf("parse NRI-provided config: %w", err)
		}
		p.setConfig(cfg)
	}

	p.log.InfoContext(ctx, "configured metrics-only NRI plugin",
		"runtime", runtime,
		"runtimeVersion", version,
		"events", "create-container,remove-container",
		"mutatesContainers", false,
	)

	// Returning 0 asks the NRI stub to subscribe to every event implemented by
	// this plugin. Add a parsed event mask here when the policy becomes narrower.
	return 0, nil
}

// Synchronize rebuilds the mapping from the runtime's full container set on
// (re)connect. The conversion of the container set runs on the events worker; the
// handler returns no container updates (this plugin does not mutate) immediately.
func (p *Plugin) Synchronize(ctx context.Context, pods []*api.PodSandbox, containers []*api.Container) ([]*api.ContainerUpdate, error) {
	p.events.Synchronize(func() []store.ContainerInfo {
		return p.adapter.containers(pods, containers)
	})

	p.log.InfoContext(ctx, "scheduled synchronize with runtime",
		"pods", len(pods),
		"containers", len(containers),
	)
	return nil, nil
}

func (p *Plugin) Shutdown(ctx context.Context) {
	p.events.Flush()
	p.log.InfoContext(ctx, "runtime requested plugin shutdown")
}

// CreateContainer records the container→pod mapping for metrics and returns no
// adjustment: this plugin never changes containers.
func (p *Plugin) CreateContainer(ctx context.Context, pod *api.PodSandbox, container *api.Container) (*api.ContainerAdjustment, []*api.ContainerUpdate, error) {
	p.events.Upsert(func() (store.ContainerInfo, bool) {
		return p.adapter.container(pod, container)
	})
	return nil, nil, nil
}

func (p *Plugin) RemoveContainer(ctx context.Context, pod *api.PodSandbox, container *api.Container) error {
	p.events.Delete(container.GetId())
	return nil
}

// Flush blocks until all queued mapping events have been applied. Used by tests
// and graceful shutdown.
func (p *Plugin) Flush() {
	p.events.Flush()
}

func (p *Plugin) config() Config {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cfg
}

func (p *Plugin) setConfig(cfg Config) {
	cfg = cfg.withDefaults()
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}
