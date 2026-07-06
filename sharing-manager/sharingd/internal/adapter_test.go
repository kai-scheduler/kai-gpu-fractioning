package internal

import (
	"testing"

	"github.com/containerd/nri/pkg/api"
)

// fractionalPod builds a pod sandbox whose fractional GPU annotations target
// the named container. Pass empty strings to omit individual annotations.
func fractionalPod(containerName, memLimitMB, memRequestMB string) *api.PodSandbox {
	annotations := map[string]string{}
	if memLimitMB != "" {
		annotations[annotationGPUMemoryPrefix+containerName+annotationGPUMemoryLimitSuffix] = memLimitMB
	}
	if memRequestMB != "" {
		annotations[annotationGPUMemoryPrefix+containerName+annotationGPUMemoryRequestSuffix] = memRequestMB
	}
	return &api.PodSandbox{
		Id:          "pod-id",
		Name:        "pod",
		Namespace:   "default",
		Uid:         "pod-uid",
		Annotations: annotations,
	}
}

// These tests cover the adapter's own responsibilities — assembling identity
// and enforcing the fractional-GPU-only policy — independent of which GPU
// detector the build selects.

func TestContainerAssemblesPodIdentityAndCgroupPath(t *testing.T) {
	c := gpuContainer("container-id", "trainer", "pod-id", 0)
	c.Linux.CgroupsPath = "/container/cgroup"

	info, ok := adapter{}.container(fractionalPod("trainer", "4096", ""), c)
	if !ok {
		t.Fatalf("expected container info")
	}
	if info.CgroupPath != "/container/cgroup" {
		t.Fatalf("expected cgroup path, got %q", info.CgroupPath)
	}
	if info.ContainerID != "container-id" || info.Container != "trainer" {
		t.Fatalf("expected container identity to be copied, got %#v", info)
	}
	if info.Pod != "pod" || info.Namespace != "default" || info.PodUID != "pod-uid" {
		t.Fatalf("expected pod identity to be copied, got %#v", info)
	}
	if len(info.GPUDevices) != 1 {
		t.Fatalf("expected one GPU device, got %#v", info.GPUDevices)
	}
}

func TestContainerSkippedWhenNotFractional(t *testing.T) {
	// A container with device nodes but no fractional GPU annotation must be
	// dropped — full-GPU pods are not tracked.
	_, ok := adapter{}.container(
		&api.PodSandbox{Id: "pod-id"},
		gpuContainer("gpu-container", "trainer", "pod-id", 0),
	)
	if ok {
		t.Fatalf("expected non-fractional GPU container to be skipped")
	}
}

func TestContainerSkippedWhenMissingID(t *testing.T) {
	if _, ok := (adapter{}).container(&api.PodSandbox{}, &api.Container{}); ok {
		t.Fatalf("expected container with empty ID to be skipped")
	}
	if _, ok := (adapter{}).container(&api.PodSandbox{}, nil); ok {
		t.Fatalf("expected nil container to be skipped")
	}
}

func TestContainerFractionalLimitAnnotationIncluded(t *testing.T) {
	// No device nodes (MPS client): included because the limit annotation is present.
	_, ok := adapter{}.container(
		fractionalPod("trainer", "4096", ""),
		&api.Container{Id: "mps-client", Name: "trainer", PodSandboxId: "pod-id"},
	)
	if !ok {
		t.Fatalf("expected fractional GPU container to be included")
	}
}

func TestContainerFractionalRequestAnnotationIncluded(t *testing.T) {
	// Only the request annotation is present — still qualifies as fractional.
	_, ok := adapter{}.container(
		fractionalPod("trainer", "", "2048"),
		&api.Container{Id: "mps-client", Name: "trainer", PodSandboxId: "pod-id"},
	)
	if !ok {
		t.Fatalf("expected fractional GPU container to be included")
	}
}

func TestContainerParsesRequestedGPUFraction(t *testing.T) {
	pod := fractionalPod("trainer", "4096", "")
	pod.Annotations["gpu-fraction"] = "0.5"

	info, ok := adapter{gpuFractionAnnotation: "gpu-fraction"}.container(
		pod,
		&api.Container{Id: "mps-client", Name: "trainer", PodSandboxId: "pod-id"},
	)
	if !ok {
		t.Fatalf("expected fractional GPU container to be included")
	}
	if info.RequestedGPUFraction != 0.5 {
		t.Fatalf("expected RequestedGPUFraction = 0.5, got %g", info.RequestedGPUFraction)
	}
}

func TestContainerRequestedFractionZeroWhenAnnotationAbsentOrInvalid(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "absent", set: false},
		{name: "not a number", value: "half", set: true},
		{name: "non-positive", value: "0", set: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pod := fractionalPod("trainer", "4096", "")
			if c.set {
				pod.Annotations["gpu-fraction"] = c.value
			}
			info, ok := adapter{gpuFractionAnnotation: "gpu-fraction"}.container(
				pod,
				&api.Container{Id: "mps-client", Name: "trainer", PodSandboxId: "pod-id"},
			)
			if !ok {
				t.Fatalf("expected fractional GPU container to be included")
			}
			if info.RequestedGPUFraction != 0 {
				t.Fatalf("expected RequestedGPUFraction = 0, got %g", info.RequestedGPUFraction)
			}
		})
	}
}

func TestContainerRequestedFractionZeroWhenAnnotationUnconfigured(t *testing.T) {
	// The empty (unconfigured) annotation key disables fraction lookup even when a
	// "gpu-fraction" annotation happens to be present.
	pod := fractionalPod("trainer", "4096", "")
	pod.Annotations["gpu-fraction"] = "0.5"

	info, ok := adapter{}.container(
		pod,
		&api.Container{Id: "mps-client", Name: "trainer", PodSandboxId: "pod-id"},
	)
	if !ok {
		t.Fatalf("expected fractional GPU container to be included")
	}
	if info.RequestedGPUFraction != 0 {
		t.Fatalf("expected RequestedGPUFraction = 0 when annotation key unconfigured, got %g", info.RequestedGPUFraction)
	}
}

func TestContainerFractionalWithDeviceNodePopulatesDevices(t *testing.T) {
	// Some fractional GPU setups inject a device node alongside the annotation.
	// Devices must be recorded.
	c := gpuContainer("c", "trainer", "pod-id", 0)
	info, ok := adapter{}.container(fractionalPod("trainer", "4096", ""), c)
	if !ok {
		t.Fatalf("expected container to be included")
	}
	if len(info.GPUDevices) == 0 {
		t.Fatalf("expected device nodes to be present")
	}
}

func TestContainerAnnotationWithInvalidContainerNameIgnored(t *testing.T) {
	// Annotation keys whose embedded container-name segment contains invalid
	// characters (uppercase, dots, etc.) must be rejected by the regex and not
	// match any container.
	pod := &api.PodSandbox{
		Id:        "pod-id",
		Name:      "pod",
		Namespace: "default",
		Uid:       "pod-uid",
		Annotations: map[string]string{
			"nvidia.com/container.Trainer.gpu-memory.limit":    "4096", // uppercase → invalid
			"nvidia.com/container.trainer.v1.gpu-memory.limit": "4096", // dot in name → invalid
		},
	}
	_, ok := adapter{}.container(pod, &api.Container{Id: "c", Name: "Trainer", PodSandboxId: "pod-id"})
	if ok {
		t.Fatalf("expected container with invalid annotation key to be skipped")
	}
}

func TestContainerSiblingWithoutAnnotationDropped(t *testing.T) {
	// A sidecar on a fractional GPU pod has no annotation for its own name and
	// must be dropped even though the pod carries a fractional annotation for the
	// "trainer" container.
	pod := fractionalPod("trainer", "4096", "")
	_, ok := adapter{}.container(pod, &api.Container{Id: "sidecar", Name: "sidecar", PodSandboxId: "pod-id"})
	if ok {
		t.Fatalf("expected sidecar container without its own annotation to be skipped")
	}
}

func TestContainersDropsNonFractionalAndSiblingContainers(t *testing.T) {
	// Only the container whose name is referenced in the pod annotation is kept.
	// The full-GPU pod's container and the sidecar on the fractional pod are both
	// dropped.
	infos := adapter{}.containers(
		[]*api.PodSandbox{
			{Id: "frac-pod-id", Name: "frac-pod", Namespace: "default", Uid: "frac-uid",
				Annotations: map[string]string{
					annotationGPUMemoryPrefix + "trainer" + annotationGPUMemoryLimitSuffix: "4096",
				}},
			{Id: "full-pod-id", Name: "full-pod", Namespace: "default", Uid: "full-uid"},
		},
		[]*api.Container{
			gpuContainer("frac-container", "trainer", "frac-pod-id", 0),   // annotation matches → kept
			{Id: "sidecar", Name: "sidecar", PodSandboxId: "frac-pod-id"}, // no annotation for "sidecar" → dropped
			gpuContainer("full-container", "gpu", "full-pod-id", 0),       // pod has no annotation → dropped
		},
	)
	if len(infos) != 1 {
		t.Fatalf("expected one fractional GPU mapping, got %d: %#v", len(infos), infos)
	}
	if infos[0].ContainerID != "frac-container" || infos[0].Pod != "frac-pod" {
		t.Fatalf("unexpected mapping: %#v", infos[0])
	}
}
