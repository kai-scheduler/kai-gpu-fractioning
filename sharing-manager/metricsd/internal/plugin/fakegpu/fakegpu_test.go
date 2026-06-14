package fakegpu

import (
	"testing"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/metricsd/internal/store"
)

func TestGPUDevicesParsesUUIDAndIndexTokens(t *testing.T) {
	devices := GPUDevices(&api.Container{
		Env: []string{"MOCK_NVIDIA_VISIBLE_DEVICES=GPU-abc,2,GPU-abc"}, // duplicate dropped
	})
	if len(devices) != 2 {
		t.Fatalf("expected two GPU devices, got %d: %#v", len(devices), devices)
	}
	if devices[0] != (store.GPUDevice{UUID: "GPU-abc"}) {
		t.Fatalf("expected UUID device first, got %#v", devices[0])
	}
	if devices[1] != (store.GPUDevice{Index: 2}) {
		t.Fatalf("expected index device second, got %#v", devices[1])
	}
}

func TestGPUDevicesPrefersRealVarOverMock(t *testing.T) {
	devices := GPUDevices(&api.Container{
		Env: []string{
			"MOCK_NVIDIA_VISIBLE_DEVICES=GPU-mock",
			"NVIDIA_VISIBLE_DEVICES=GPU-real",
		},
	})
	if len(devices) != 1 || devices[0] != (store.GPUDevice{UUID: "GPU-real"}) {
		t.Fatalf("expected the real var to win, got %#v", devices)
	}
}

func TestGPUDevicesIgnoresSentinelsAndWildcard(t *testing.T) {
	for _, value := range []string{"", "none", "void", "all"} {
		devices := GPUDevices(&api.Container{
			Env: []string{"NVIDIA_VISIBLE_DEVICES=" + value},
		})
		if len(devices) != 0 {
			t.Fatalf("expected sentinel %q to yield no GPU, got %#v", value, devices)
		}
	}
}

func TestGPUDevicesNilWithoutEnv(t *testing.T) {
	if devices := GPUDevices(&api.Container{}); len(devices) != 0 {
		t.Fatalf("expected no devices without env, got %#v", devices)
	}
}
