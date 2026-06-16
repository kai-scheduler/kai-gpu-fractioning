package plugin

import (
	"regexp"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/store"

	"github.com/containerd/nri/pkg/api"
)

// Fractional GPU annotation format: nvidia.com/container.<containerName>.gpu-memory.{limit,request}.
// The container name is embedded in the key, so each container in a pod is
// addressed independently. The constants below document the structure; the
// regex is what actually validates and parses keys at runtime.
const (
	annotationGPUMemoryPrefix        = "nvidia.com/container."
	annotationGPUMemoryLimitSuffix   = ".gpu-memory.limit"
	annotationGPUMemoryRequestSuffix = ".gpu-memory.request"
)

// annotationGPUMemoryRE matches a well-formed fractional GPU memory annotation
// key. Capture group 1 is the container name (lowercase alphanumeric and
// hyphens, matching the Kubernetes DNS-label character class); capture group 2
// is the annotation type: "limit" or "request".
var annotationGPUMemoryRE = regexp.MustCompile(
	`^nvidia\.com/container\.([a-z0-9](?:[a-z0-9\-]*[a-z0-9])?)\.gpu-memory\.(limit|request)$`,
)

// adapter converts NRI runtime objects (api.PodSandbox, api.Container) into the
// runtime-independent store.ContainerInfo consumed by the events processor. It is
// the only place that knows the NRI api.* types on the ingest path, which keeps
// the events processor and the store free of any NRI dependency.
//
// adapter is stateless; the methods read only their arguments, so the value can
// be shared and its methods called concurrently.
type adapter struct{}

// container builds the container→pod mapping for a single container. Only
// fractional GPU containers are tracked: the pod must carry at least one of the
// nvidia.com/container.trainer.gpu-memory.* annotations, returning ok=false
// otherwise. Full-GPU (non-fractional) containers are intentionally excluded.
// GPU device nodes are still read when present (they supply the device index
// for process-to-pod attribution), but their presence is not required.
func (adapter) container(pod *api.PodSandbox, container *api.Container) (store.ContainerInfo, bool) {
	if container == nil || container.GetId() == "" {
		return store.ContainerInfo{}, false
	}
	if !isFractionalGPUContainer(container.GetName(), pod.GetAnnotations()) {
		return store.ContainerInfo{}, false
	}

	info := store.ContainerInfo{
		ContainerID: container.GetId(),
		Container:   container.GetName(),
		Pod:         pod.GetName(),
		Namespace:   pod.GetNamespace(),
		PodUID:      pod.GetUid(),
		GPUDevices:  gpuDevices(container),
	}
	if linux := container.GetLinux(); linux != nil {
		info.CgroupPath = linux.GetCgroupsPath()
	}
	return info, true
}

// isFractionalGPUContainer reports whether the pod annotations contain a
// fractional GPU memory annotation for the named container. The annotation key
// is validated against annotationGPUMemoryRE so only well-formed keys (with a
// lowercase-alphanumeric container name segment) are recognised.
func isFractionalGPUContainer(containerName string, annotations map[string]string) bool {
	for key := range annotations {
		m := annotationGPUMemoryRE.FindStringSubmatch(key)
		if m != nil && m[1] == containerName {
			return true
		}
	}
	return false
}

// containers builds the full mapping set delivered by an NRI Synchronize. Each
// container is matched to its pod sandbox by ID; non-fractional containers are
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
