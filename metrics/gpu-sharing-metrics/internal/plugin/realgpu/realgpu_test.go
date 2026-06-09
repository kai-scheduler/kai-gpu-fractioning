package realgpu

import (
	"testing"

	"github.com/containerd/nri/pkg/api"
)

func TestGPUDevicesExtractsDistinctNVIDIAMinors(t *testing.T) {
	devices := GPUDevices(&api.Container{
		Linux: &api.LinuxContainer{
			Devices: []*api.LinuxDevice{
				{Path: "/dev/renamed0", Major: 195, Minor: 0},
				{Path: "/dev/nvidia1", Major: 195, Minor: 1},
				{Path: "/dev/nvidia1-duplicate", Major: 195, Minor: 1},
				{Path: "/dev/nvidiactl", Major: 195, Minor: 255}, // control node: out of GPU minor range
				{Path: "/dev/not-nvidia", Major: 1, Minor: 0},
			},
		},
	})
	if len(devices) != 2 {
		t.Fatalf("expected two GPU devices, got %d: %#v", len(devices), devices)
	}
	if devices[0].Index != 0 || devices[1].Index != 1 {
		t.Fatalf("unexpected GPU indexes: %#v", devices)
	}
}

func TestGPUDevicesIgnoresEnvironment(t *testing.T) {
	// The production detector is authoritative on device nodes only: a container
	// that advertises a GPU solely via env has no detectable GPU device.
	devices := GPUDevices(&api.Container{
		Env: []string{"NVIDIA_VISIBLE_DEVICES=GPU-from-env,1"},
	})
	if len(devices) != 0 {
		t.Fatalf("expected env-only access to be ignored, got %#v", devices)
	}
}

func TestGPUDevicesNilWithoutLinux(t *testing.T) {
	if devices := GPUDevices(&api.Container{}); len(devices) != 0 {
		t.Fatalf("expected no devices without Linux metadata, got %#v", devices)
	}
}
