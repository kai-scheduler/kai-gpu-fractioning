package audit

import (
	"slices"
	"testing"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/annotations"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/internal/injection"
)

func TestDetectorViolations(t *testing.T) {
	tests := []struct {
		name        string
		pod         *api.PodSandbox
		ctr         *api.Container
		wantMissing []string // nil ⇒ expect no violator
	}{
		{
			name: "fully injected container is not a violator",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", "2Gi"),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State:  api.ContainerState_CONTAINER_RUNNING,
				Env:    injectedEnv(true, true),
				Mounts: []*api.Mount{mpsMount()},
			},
		},
		{
			name: "missing MPS env is a violator",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State:  api.ContainerState_CONTAINER_RUNNING,
				Env:    []string{injection.EnvGPUMemoryLimits + "=4294", injection.EnvGPUMemoryRequests + "=4294"},
				Mounts: []*api.Mount{mpsMount()},
			},
			wantMissing: []string{"env:" + injection.EnvMPSPipeDirectory},
		},
		{
			name: "missing MPS mount is a violator",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
				Env:   injectedEqualEnv(),
			},
			wantMissing: []string{"mount:" + configuration.DefaultMPSPipeDirectory},
		},
		{
			name: "missing limit env (limit annotated) is a violator",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
				// request env present (defaulted from the limit), limit env missing.
				Env:    []string{injection.EnvMPSPipeDirectory + "=" + configuration.DefaultMPSPipeDirectory, injection.EnvGPUMemoryRequests + "=4294"},
				Mounts: []*api.Mount{mpsMount()},
			},
			wantMissing: []string{"env:" + injection.EnvGPUMemoryLimits},
		},
		{
			name: "nothing injected reports every missing piece",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", "2Gi"),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
			},
			wantMissing: []string{
				"env:" + injection.EnvMPSPipeDirectory,
				"env:" + injection.EnvGPUMemoryLimits,
				"env:" + injection.EnvGPUMemoryRequests,
				"mount:" + configuration.DefaultMPSPipeDirectory,
			},
		},
		{
			name: "request-only sharing container expects both request and limit env",
			pod:  sharingPod("p", "pod", "trainer", "", "2Gi"),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
				// limit defaults from the request, so both memory envs are expected.
				Env:    []string{injection.EnvMPSPipeDirectory + "=" + configuration.DefaultMPSPipeDirectory},
				Mounts: []*api.Mount{mpsMount()},
			},
			wantMissing: []string{"env:" + injection.EnvGPUMemoryLimits, "env:" + injection.EnvGPUMemoryRequests},
		},
		{
			name: "missing NVIDIA_VISIBLE_DEVICES env (device assigned) is a violator",
			pod:  withVisibleDevices(sharingPod("p", "pod", "trainer", "4Gi", ""), "GPU-abc123"),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State:  api.ContainerState_CONTAINER_RUNNING,
				Env:    injectedEqualEnv(),
				Mounts: []*api.Mount{mpsMount()},
			},
			wantMissing: []string{"env:" + injection.EnvVisibleDevices},
		},
		{
			name: "assigned device fully injected is not a violator",
			pod:  withVisibleDevices(sharingPod("p", "pod", "trainer", "4Gi", ""), "GPU-abc123"),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State:  api.ContainerState_CONTAINER_RUNNING,
				Env:    append(injectedEqualEnv(), injection.EnvVisibleDevices+"=GPU-abc123"),
				Mounts: []*api.Mount{mpsMount()},
			},
		},
		{
			name: "no device assignment does not expect NVIDIA_VISIBLE_DEVICES",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State:  api.ContainerState_CONTAINER_RUNNING,
				Env:    injectedEqualEnv(),
				Mounts: []*api.Mount{mpsMount()},
			},
		},
		{
			name: "created state is enforceable",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_CREATED,
			},
			wantMissing: []string{
				"env:" + injection.EnvMPSPipeDirectory,
				"env:" + injection.EnvGPUMemoryLimits,
				"env:" + injection.EnvGPUMemoryRequests,
				"mount:" + configuration.DefaultMPSPipeDirectory,
			},
		},
		{
			name: "non-sharing container is skipped",
			pod:  &api.PodSandbox{Id: "p", Name: "pod", Annotations: map[string]string{"foo": "bar"}},
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
			},
		},
		{
			name: "sibling without its own annotation is skipped",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "sidecar", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
			},
		},
		{
			name: "stopped container is skipped even if uninjected",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_STOPPED,
			},
		},
		{
			name: "container with empty id is skipped",
			pod:  sharingPod("p", "pod", "trainer", "4Gi", ""),
			ctr: &api.Container{
				Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
			},
		},
		{
			name: "unparseable annotation is skipped, not stopped",
			pod:  sharingPod("p", "pod", "trainer", "not-a-quantity", ""),
			ctr: &api.Container{
				Id: "c", Name: "trainer", PodSandboxId: "p",
				State: api.ContainerState_CONTAINER_RUNNING,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newDetector().violators(
				[]*api.PodSandbox{tt.pod},
				[]*api.Container{tt.ctr},
			)

			if tt.wantMissing == nil {
				if len(got) != 0 {
					t.Fatalf("expected no violator, got %+v", got)
				}
				return
			}

			if len(got) != 1 {
				t.Fatalf("expected exactly one violator, got %d: %+v", len(got), got)
			}
			v := got[0]
			if v.containerID != tt.ctr.GetId() || v.container != tt.ctr.GetName() {
				t.Errorf("violator identity = %q/%q, want %q/%q", v.containerID, v.container, tt.ctr.GetId(), tt.ctr.GetName())
			}
			assertSameSet(t, v.missing, tt.wantMissing)
		})
	}
}

func TestDetectorViolationsSkipsContainerWithoutPod(t *testing.T) {
	// A container whose PodSandboxId matches no pod in the snapshot must be
	// skipped rather than panic or flagged.
	got := newDetector().violators(
		nil,
		[]*api.Container{{
			Id: "c", Name: "trainer", PodSandboxId: "missing",
			State: api.ContainerState_CONTAINER_RUNNING,
		}},
	)
	if len(got) != 0 {
		t.Fatalf("expected no violator for pod-less container, got %+v", got)
	}
}

func TestDetectorViolationsAcrossMultipleContainers(t *testing.T) {
	pods := []*api.PodSandbox{
		sharingPod("p1", "pod1", "trainer", "4Gi", ""),
		sharingPod("p2", "pod2", "trainer", "4Gi", ""),
		{Id: "p3", Name: "pod3", Annotations: map[string]string{"foo": "bar"}}, // non-sharing
	}
	containers := []*api.Container{
		{ // p1: injected → ok
			Id: "c1", Name: "trainer", PodSandboxId: "p1",
			State: api.ContainerState_CONTAINER_RUNNING, Env: injectedEqualEnv(), Mounts: []*api.Mount{mpsMount()},
		},
		{ // p2: uninjected → violator
			Id: "c2", Name: "trainer", PodSandboxId: "p2",
			State: api.ContainerState_CONTAINER_RUNNING,
		},
		{ // p3: non-sharing → skipped
			Id: "c3", Name: "app", PodSandboxId: "p3",
			State: api.ContainerState_CONTAINER_RUNNING,
		},
	}

	got := newDetector().violators(pods, containers)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violator, got %d: %+v", len(got), got)
	}
	if got[0].containerID != "c2" {
		t.Fatalf("expected violator for c2, got %q", got[0].containerID)
	}
}

// sharingPod builds a pod sandbox carrying the GPU memory annotations for the
// named container. Pass empty strings to omit an annotation.
func sharingPod(id, name, containerName, limit, request string) *api.PodSandbox {
	ann := map[string]string{}
	if limit != "" {
		ann[configuration.DefaultAnnotationPrefix+containerName+".limit"] = limit
	}
	if request != "" {
		ann[configuration.DefaultAnnotationPrefix+containerName+".request"] = request
	}
	return &api.PodSandbox{Id: id, Name: name, Namespace: "default", Uid: id + "-uid", Annotations: ann}
}

// withVisibleDevices records a GPU device assignment on the pod, as the scheduler
// would, so the detector expects NVIDIA_VISIBLE_DEVICES to be injected.
func withVisibleDevices(pod *api.PodSandbox, value string) *api.PodSandbox {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[annotations.VisibleDevicesAnnotation] = value
	return pod
}

// injectedEnv returns the env keys buildAdjustment would add for the given
// request/limit presence, so tests can construct a fully-injected container.
func injectedEnv(limit, request bool) []string {
	env := []string{injection.EnvMPSPipeDirectory + "=" + configuration.DefaultMPSPipeDirectory}
	if limit {
		env = append(env, injection.EnvGPUMemoryLimits+"=4294")
	}
	if request {
		env = append(env, injection.EnvGPUMemoryRequests+"=2147")
	}
	return env
}

// injectedEqualEnv returns the fully-injected env for a container whose request
// and limit resolve to the same value — the real post-injection state of a
// request-only or limit-only pod after ApplyDefaults (both default to the 4Gi /
// 4294 MB value injectedEnv uses for the limit). Use this instead of
// injectedEnv(true, true) for such pods so fixtures match reality.
func injectedEqualEnv() []string {
	return []string{
		injection.EnvMPSPipeDirectory + "=" + configuration.DefaultMPSPipeDirectory,
		injection.EnvGPUMemoryLimits + "=4294",
		injection.EnvGPUMemoryRequests + "=4294",
	}
}

func mpsMount() *api.Mount {
	return &api.Mount{
		Source:      configuration.DefaultMPSPipeDirectory,
		Destination: configuration.DefaultMPSPipeDirectory,
		Type:        "bind",
		Options:     []string{"bind", "rw"},
	}
}

func newDetector() detector {
	return detector{
		annotationPrefix: configuration.DefaultAnnotationPrefix,
		mpsPipeDirectory: configuration.DefaultMPSPipeDirectory,
	}
}

func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	g := slices.Clone(got)
	w := slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	if !slices.Equal(g, w) {
		t.Errorf("missing set = %v, want %v", g, w)
	}
}
