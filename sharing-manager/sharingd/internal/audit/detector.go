// Package audit inspects the containers reports on an NRI
// (re)connect (the Synchronize snapshot) and finds containers that are running
// without the sharing setup the CreateContainer hook is supposed to inject.
// Such containers slipped through while the sharing agent was down: they
// hold a GPU share but no memory limit, so they can harm neighbours on the same
// GPU. The remediation path stops them, so kubelet recreates them through a
// healthy CreateContainer hook.
package audit

import (
	"log/slog"
	"strings"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/annotations"
)

// Injected env-var keys. Mirror the sharingd create-hook injection
// (internal/plugin.go buildAdjustment). Keep in sync with that code.
const (
	envGPUMemoryRequests = "NVIDIA_GPU_MEMORY_REQUESTS"
	envGPUMemoryLimits   = "NVIDIA_GPU_MEMORY_LIMITS"
	envMPSPipeDirectory  = "CUDA_MPS_PIPE_DIRECTORY"
)

// violation describes a running container that belongs to a GPU-sharing pod but
// is missing the sharing setup the CreateContainer hook would have injected. It
// is produced by detector and consumed by the remediation path (same package).
type violation struct {
	containerID string
	container   string
	pod         string
	namespace   string
	missing     []string // missing lists the expected-but-absent pieces
}

// detector finds GPU-sharing containers that are running without the expected
// injection.
// A container qualifies as a sharing container iff annotations.ParseGPUMemoryAnnotations
// yields a non-empty config for it — the same check the create hook uses. The
// detector then reports any expected env/mount the live container lacks.
type detector struct {
	// annotationPrefix is the GPU-memory annotation prefix, e.g.
	// "nvidia.com/gpu-memory.container." — must match the sharingd plugin's
	// configured prefix.
	annotationPrefix string
	// mpsPipeDirectory is the expected CUDA_MPS_PIPE_DIRECTORY value and MPS bind
	// mount destination — must match the sharingd plugin's configured value.
	mpsPipeDirectory string
	// log is used for skip diagnostics; defaults to slog.Default().
	log *slog.Logger
}

// violations scans an NRI Synchronize snapshot and returns one entry per
// GPU-sharing container that is running without the expected injection. Each
// container is matched to its pod sandbox by PodSandboxId; containers with no
// matching pod, non-enforceable states, or no sharing annotations are skipped.
func (d detector) violations(pods []*api.PodSandbox, containers []*api.Container) []violation {
	podsByID := podSandboxLookup(pods)

	var out []violation
	for _, container := range containers {
		if v, ok := d.check(podsByID[container.GetPodSandboxId()], container); ok {
			out = append(out, v)
		}
	}
	return out
}

// check evaluates a single container against its pod. It returns ok=false for
// anything that must not be stopped: nil/pod-less containers, non-running
// states, non-sharing containers, containers with unparseable annotations, or
// containers that already carry the full expected injection.
func (d detector) check(pod *api.PodSandbox, container *api.Container) (violation, bool) {
	if container == nil || container.GetId() == "" || pod == nil {
		return violation{}, false
	}

	// Only running/created containers can be meaningfully remediated. A stopped
	// or unknown container has nothing to enforce.
	switch container.GetState() {
	case api.ContainerState_CONTAINER_CREATED, api.ContainerState_CONTAINER_RUNNING:
	default:
		return violation{}, false
	}

	cfg, err := annotations.ParseGPUMemoryAnnotations(pod.GetAnnotations(), container.GetName(), d.annotationPrefix)
	if err != nil {
		// Malformed annotations: with fail-closed creation this container would
		// not exist, and recreating it would only hit the same parse error — skip
		// rather than risk a container we cannot reason about. Log the raw
		// annotation values so the offending input is visible, not just the error.
		requestKey := d.annotationPrefix + container.GetName() + ".request"
		limitKey := d.annotationPrefix + container.GetName() + ".limit"
		ann := pod.GetAnnotations()
		d.logger().Warn("audit: skipping container with unparseable GPU memory annotations",
			"container", container.GetName(),
			"pod", pod.GetName(),
			"requestAnnotation", ann[requestKey],
			"limitAnnotation", ann[limitKey],
			"error", err,
		)
		return violation{}, false
	}
	if cfg.IsEmpty() {
		// Not a GPU-sharing container — never touch it.
		return violation{}, false
	}

	missing := d.missingInjection(cfg, container)
	if len(missing) == 0 {
		return violation{}, false
	}

	return violation{
		containerID: container.GetId(),
		container:   container.GetName(),
		pod:         pod.GetName(),
		namespace:   pod.GetNamespace(),
		missing:     missing,
	}, true
}

// missingInjection compares the expected injection (derived from cfg exactly as
// buildAdjustment would) against the live container's env and mounts, returning
// the expected pieces that are absent. Presence-based, not value-equality: a
// mis-set value is out of scope and would risk false positives from formatting
// differences.
func (d detector) missingInjection(cfg annotations.GPUMemoryConfig, container *api.Container) []string {
	env := presentEnv(container.GetEnv())

	var missing []string
	// CUDA_MPS_PIPE_DIRECTORY is always injected for a sharing container.
	if !env[envMPSPipeDirectory] {
		missing = append(missing, "env:"+envMPSPipeDirectory)
	}
	if cfg.Limit != "" && !env[envGPUMemoryLimits] {
		missing = append(missing, "env:"+envGPUMemoryLimits)
	}
	if cfg.Request != "" && !env[envGPUMemoryRequests] {
		missing = append(missing, "env:"+envGPUMemoryRequests)
	}
	if !hasMountDestination(container.GetMounts(), d.mpsPipeDirectory) {
		missing = append(missing, "mount:"+d.mpsPipeDirectory)
	}
	return missing
}

func (d detector) logger() *slog.Logger {
	if d.log != nil {
		return d.log
	}
	return slog.Default()
}

// presentEnv scans the container's "KEY=VALUE" env list once and reports which
// of the injected keys (and only those) are present. A token without '=' is
// treated as a bare key.
func presentEnv(env []string) map[string]bool {
	present := make(map[string]bool, 3)
	for _, kv := range env {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		switch k {
		case envMPSPipeDirectory, envGPUMemoryLimits, envGPUMemoryRequests:
			present[k] = true
		}
	}
	return present
}

// hasMountDestination reports whether any mount targets the given destination.
func hasMountDestination(mounts []*api.Mount, destination string) bool {
	for _, m := range mounts {
		if m.GetDestination() == destination {
			return true
		}
	}
	return false
}

// podSandboxLookup indexes pod sandboxes by their NRI id for O(1) matching
// against a container's PodSandboxId.
func podSandboxLookup(pods []*api.PodSandbox) map[string]*api.PodSandbox {
	out := make(map[string]*api.PodSandbox, len(pods))
	for _, pod := range pods {
		if pod.GetId() == "" {
			continue
		}
		out[pod.GetId()] = pod
	}
	return out
}
