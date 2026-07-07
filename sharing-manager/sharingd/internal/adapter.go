package internal

import (
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/store"

	"github.com/containerd/nri/pkg/api"
)

// adapter converts NRI runtime objects (api.PodSandbox, api.Container) into the
// runtime-independent store.ContainerInfo consumed by the events processor. It is
// the only place that knows the NRI api.* types on the ingest path, which keeps
// the events processor and the store free of any NRI dependency.
//
// A container is recorded for metrics using the same signal the mutation path
// enforces on: a well-formed GPU-memory annotation (<annotationPrefix><name>.
// {request,limit}). This is deliberately the identical decision — there is no
// separate "is this a fractional GPU container" annotation — so a container is
// recorded iff it is also mutated. adapter is stateless apart from the
// configured prefix, so its methods are safe to call concurrently.
type adapter struct {
	// annotationPrefix is the GPU-memory annotation prefix (e.g.
	// "nvidia.com/gpu-memory.container."). It is the same prefix the mutation path
	// uses, so mapping and mutation select the same containers.
	annotationPrefix string
}

// container builds the container→pod mapping for a single container. Only
// GPU-sharing containers are tracked: the pod must carry a well-formed GPU-memory
// annotation for this container, returning ok=false otherwise. The recorded
// RequestedMemoryMB (limit, else request) is what the metrics sidecar divides by
// the device's total memory to normalize SM utilization. GPU device nodes are
// read when present (they supply the device index for process-to-pod
// attribution), but their presence is not required.
func (a adapter) container(pod *api.PodSandbox, container *api.Container) (store.ContainerInfo, bool) {
	if container == nil || container.GetId() == "" {
		return store.ContainerInfo{}, false
	}
	cfg, ok := a.isFractionalGPUContainer(pod, container.GetName())
	if !ok {
		return store.ContainerInfo{}, false
	}

	info := store.ContainerInfo{
		ContainerID:       container.GetId(),
		Container:         container.GetName(),
		Pod:               pod.GetName(),
		Namespace:         pod.GetNamespace(),
		PodUID:            pod.GetUid(),
		GPUDevices:        gpuDevices(container),
		RequestedMemoryMB: cfg.EffectiveMemoryMB(),
	}
	if linux := container.GetLinux(); linux != nil {
		info.CgroupPath = linux.GetCgroupsPath()
	}
	return info, true
}

// isFractionalGPUContainer reports whether the named container is a fractional
// (GPU-sharing) container: it carries a well-formed GPU-memory annotation under
// the configured prefix. It returns the parsed config so the caller can read the
// requested memory without parsing twice.
//
// A malformed annotation counts as "not fractional" here: the mutation path
// fail-closes on it (blocking container creation), so it must not be recorded in
// the mapping either. This is the same signal the mutation path enforces on, so
// a container is recorded for metrics iff it is also mutated.
func (a adapter) isFractionalGPUContainer(pod *api.PodSandbox, containerName string) (GPUMemoryConfig, bool) {
	cfg, err := ParseGPUMemoryAnnotations(pod.GetAnnotations(), containerName, a.annotationPrefix)
	if err != nil || cfg.IsEmpty() {
		return GPUMemoryConfig{}, false
	}
	return cfg, true
}

// containers builds the full mapping set delivered by an NRI Synchronize. Each
// container is matched to its pod sandbox by ID; non-GPU-sharing containers are
// dropped.
func (a adapter) containers(pods []*api.PodSandbox, containers []*api.Container) []store.ContainerInfo {
	podsByID := podSandboxLookup(pods)

	infos := make([]store.ContainerInfo, 0, len(containers))
	for _, container := range containers {
		pod := podsByID[container.GetPodSandboxId()]
		if info, ok := a.container(pod, container); ok {
			infos = append(infos, info)
		}
	}
	return infos
}

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
