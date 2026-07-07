package internal

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/fsstore"
	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/store"
)

// testMappingPlugin builds a plugin whose mapping handoff goes through a
// throwaway directory, plus a reader over the same directory so a test can
// observe what the metrics sidecar would read back.
func testMappingPlugin(t *testing.T) (*Plugin, *fsstore.Reader) {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewPlugin(Config{
		AnnotationPrefix:      "nvidia.com/gpu-memory.container.",
		MPSPipeDirectory:      "/run/nvidia-mps",
		MapDir:                dir,
		GPUFractionAnnotation: "gpu-fraction",
		Log:                   log,
	})
	return p, fsstore.NewReader(dir, log)
}

// activeContainers drains queued events and returns the containers the metrics
// sidecar would attribute processes to.
func activeContainers(t *testing.T, p *Plugin, r *fsstore.Reader) []store.ContainerInfo {
	t.Helper()
	p.Flush()
	return r.ActiveContainers()
}

// gpuContainer builds a container any GPU detector recognizes, so plugin-level
// tests are independent of the build-selected detector. It carries both NVIDIA
// device nodes (major 195) for the given minors — what the production realgpu
// detector reads — and a matching NVIDIA_VISIBLE_DEVICES env listing the same
// minors — what the e2e fakegpu detector reads. Because numeric env tokens map to
// GPUDevice{Index: n}, both detectors yield the same devices.
func gpuContainer(id, name, podSandboxID string, minors ...int64) *api.Container {
	devices := make([]*api.LinuxDevice, 0, len(minors))
	visible := make([]string, 0, len(minors))
	for _, minor := range minors {
		devices = append(devices, &api.LinuxDevice{Major: 195, Minor: minor})
		visible = append(visible, strconv.FormatInt(minor, 10))
	}
	return &api.Container{
		Id:           id,
		Name:         name,
		PodSandboxId: podSandboxID,
		Linux:        &api.LinuxContainer{Devices: devices},
		Env:          []string{"NVIDIA_VISIBLE_DEVICES=" + strings.Join(visible, ",")},
	}
}

func TestCreateContainerRecordsGPUMapping(t *testing.T) {
	plugin, reader := testMappingPlugin(t)

	if _, _, err := plugin.CreateContainer(context.Background(),
		&api.PodSandbox{
			Name: "pod", Namespace: "default", Uid: "pod-uid",
			Annotations: map[string]string{
				annotationGPUMemoryPrefix + "container" + annotationGPUMemoryLimitSuffix: "4Gi",
			},
		},
		gpuContainer("container-id", "container", "", 0, 1),
	); err != nil {
		t.Fatalf("CreateContainer returned error: %v", err)
	}

	got := activeContainers(t, plugin, reader)
	if len(got) != 1 {
		t.Fatalf("expected one recorded mapping, got %d", len(got))
	}
	info := got[0]
	if info.ContainerID != "container-id" || info.Pod != "pod" || info.Namespace != "default" || info.PodUID != "pod-uid" {
		t.Fatalf("unexpected mapping identity: %#v", info)
	}
	if len(info.GPUDevices) != 2 {
		t.Fatalf("expected two GPU devices recorded, got %#v", info.GPUDevices)
	}
}

func TestCreateContainerIgnoresNonFractionalContainer(t *testing.T) {
	plugin, reader := testMappingPlugin(t)

	if _, _, err := plugin.CreateContainer(context.Background(),
		&api.PodSandbox{Name: "pod"},
		&api.Container{Id: "container-id", Name: "container"},
	); err != nil {
		t.Fatalf("CreateContainer returned error: %v", err)
	}

	if n := len(activeContainers(t, plugin, reader)); n != 0 {
		t.Fatalf("expected non-fractional container to be ignored, got %d mappings", n)
	}
}

// A fail-closed annotation parse error must block the container AND skip the
// mapping: the container will not exist, so recording it would be stale.
func TestCreateContainerFailClosedDoesNotRecordMapping(t *testing.T) {
	plugin, reader := testMappingPlugin(t)

	_, _, err := plugin.CreateContainer(context.Background(),
		&api.PodSandbox{
			Name: "pod", Uid: "pod-uid",
			Annotations: map[string]string{
				// Malformed value → mutation fail-closes; the same key drives the
				// mapping, so the container must not be recorded either.
				annotationGPUMemoryPrefix + "container" + annotationGPUMemoryLimitSuffix: "not-a-quantity",
			},
		},
		gpuContainer("container-id", "container", "", 0),
	)
	if err == nil {
		t.Fatal("expected fail-closed error for malformed annotation")
	}
	if n := len(activeContainers(t, plugin, reader)); n != 0 {
		t.Fatalf("expected no mapping recorded on fail-closed, got %d", n)
	}
}

func TestRemoveContainerDeletesMapping(t *testing.T) {
	plugin, reader := testMappingPlugin(t)

	if _, _, err := plugin.CreateContainer(context.Background(),
		&api.PodSandbox{
			Name: "pod", Uid: "pod-uid",
			Annotations: map[string]string{
				annotationGPUMemoryPrefix + "container" + annotationGPUMemoryLimitSuffix: "4Gi",
			},
		},
		gpuContainer("container-id", "container", "", 0),
	); err != nil {
		t.Fatalf("CreateContainer returned error: %v", err)
	}
	if n := len(activeContainers(t, plugin, reader)); n != 1 {
		t.Fatalf("expected mapping to be recorded before delete, got %d", n)
	}

	if err := plugin.RemoveContainer(context.Background(), &api.PodSandbox{}, &api.Container{Id: "container-id"}); err != nil {
		t.Fatalf("RemoveContainer returned error: %v", err)
	}

	if n := len(activeContainers(t, plugin, reader)); n != 0 {
		t.Fatalf("expected mapping to be deleted, got %d", n)
	}
}

func TestSynchronizeReplacesMappings(t *testing.T) {
	plugin, reader := testMappingPlugin(t)

	updates, err := plugin.Synchronize(context.Background(),
		[]*api.PodSandbox{{
			Id: "pod-id", Name: "pod", Namespace: "default", Uid: "pod-uid",
			Annotations: map[string]string{
				annotationGPUMemoryPrefix + "gpu" + annotationGPUMemoryLimitSuffix: "4Gi",
			},
		}},
		[]*api.Container{
			gpuContainer("gpu-container", "gpu", "pod-id", 0),
			{Id: "plain-container", Name: "plain", PodSandboxId: "pod-id"}, // no annotation → ignored
		},
	)
	if err != nil {
		t.Fatalf("Synchronize returned error: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected no container updates, got %d", len(updates))
	}

	got := activeContainers(t, plugin, reader)
	if len(got) != 1 {
		t.Fatalf("expected one GPU mapping after sync, got %d", len(got))
	}
	if got[0].ContainerID != "gpu-container" || got[0].Pod != "pod" {
		t.Fatalf("unexpected synchronized mapping: %#v", got[0])
	}
}
