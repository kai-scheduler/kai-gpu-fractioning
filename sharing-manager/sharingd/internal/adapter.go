package internal

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/mapping/store"

	"github.com/containerd/nri/pkg/api"
)

// adapter converts NRI runtime objects (api.PodSandbox, api.Container) into the
// runtime-independent store.ContainerInfo consumed by the events processor. It is
// the only place that knows the NRI api.* types on the ingest path, which keeps
// the events processor and the store free of any NRI dependency.
//
// adapter is stateless apart from the configured GPU-fraction annotation key; the
// methods read only their arguments plus that key, so the value can be shared and
// its methods called concurrently.
type adapter struct {
	// gpuFractionAnnotation is the pod annotation key whose value is the requested
	// GPU fraction (e.g. "0.5"), used to normalize SM utilization. Empty disables
	// fraction lookup (RequestedGPUFraction stays 0).
	gpuFractionAnnotation string

	// log records per-container mapping decisions at debug level. It runs on the
	// events worker goroutine (off the NRI hot path). May be nil (falls back to
	// slog.Default()).
	log *slog.Logger
}

// logger returns the adapter's logger, or the default when unset (e.g. in tests
// that construct the adapter directly).
func (a adapter) logger() *slog.Logger {
	if a.log != nil {
		return a.log
	}
	return slog.Default()
}

// container builds the container→pod mapping for a single container. Only
// fractional GPU containers are tracked: the pod must carry at least one of the
// nvidia.com/gpu-memory.container.<name>.{request,limit} annotations, returning
// ok=false otherwise. Full-GPU (non-fractional) containers are intentionally excluded.
// GPU device nodes are still read when present (they supply the device index
// for process-to-pod attribution), but their presence is not required.
func (a adapter) container(pod *api.PodSandbox, container *api.Container) (store.ContainerInfo, bool) {
	if container == nil || container.GetId() == "" {
		return store.ContainerInfo{}, false
	}
	if !isFractionalGPUContainer(container.GetName(), pod.GetAnnotations()) {
		a.logger().Debug("skipping container: no fractional GPU-memory annotation for it",
			"container", container.GetName(),
			"pod", pod.GetName(),
			"namespace", pod.GetNamespace(),
			"expectedAnnotation", containerMemoryAnnotationKey(annotationGPUMemoryPrefix, container.GetName(), annotationSuffixLimit),
		)
		return store.ContainerInfo{}, false
	}

	info := store.ContainerInfo{
		ContainerID:          container.GetId(),
		Container:            container.GetName(),
		Pod:                  pod.GetName(),
		Namespace:            pod.GetNamespace(),
		PodUID:               pod.GetUid(),
		GPUDevices:           gpuDevices(container),
		RequestedGPUFraction: a.requestedGPUFraction(pod.GetAnnotations()),
	}
	if linux := container.GetLinux(); linux != nil {
		info.CgroupPath = linux.GetCgroupsPath()
	}

	a.logger().Debug("recorded fractional GPU container mapping",
		"container", info.Container,
		"pod", info.Pod,
		"namespace", info.Namespace,
		"podUID", info.PodUID,
		"containerID", info.ContainerID,
		"gpuDevices", info.GPUDevices,
		"requestedGpuFraction", info.RequestedGPUFraction,
	)
	return info, true
}

// isFractionalGPUContainer reports whether the pod annotations contain a
// fractional GPU memory annotation for the named container.
func isFractionalGPUContainer(containerName string, annotations map[string]string) bool {
	_, hasRequest := annotations[containerMemoryAnnotationKey(annotationGPUMemoryPrefix, containerName, annotationSuffixRequest)]
	_, hasLimit := annotations[containerMemoryAnnotationKey(annotationGPUMemoryPrefix, containerName, annotationSuffixLimit)]
	return hasRequest || hasLimit
}

// requestedGPUFraction returns the requested GPU fraction read from the
// configured annotation key (e.g. "gpu-fraction" → "0.5"). It returns 0 when the
// key is unconfigured, absent, or its value is not a parseable fraction —
// normalization then treats the container as having no known request rather than
// failing ingest.
func (a adapter) requestedGPUFraction(annotations map[string]string) float64 {
	if a.gpuFractionAnnotation == "" {
		return 0
	}
	value, ok := annotations[a.gpuFractionAnnotation]
	if !ok {
		return 0
	}
	fraction, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || fraction <= 0 {
		return 0
	}
	return fraction
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
