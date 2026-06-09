// Package fakegpu is the TEST/DEV GPU detector. It identifies a container's GPUs
// from its NVIDIA_VISIBLE_DEVICES / MOCK_NVIDIA_VISIBLE_DEVICES environment
// variable rather than from real device nodes, so the NRI mapping path can be
// exercised on fake-GPU clusters (e.g. the fake-gpu-operator / KWOK) that
// advertise GPUs without injecting /dev/nvidia* nodes.
//
// It is selected only by the plugin package's `e2e` build (see gpudevices_fake.go)
// and is never linked into the production binary, which uses the device-node
// detector in the sibling realgpu package. Env vars are not authoritative for
// device identity, which is exactly why this is test-only.
package fakegpu

import (
	"strconv"
	"strings"

	"gpu-sharing-operator/metrics/gpu-sharing-metrics/internal/store"

	"github.com/containerd/nri/pkg/api"
)

// visibleDevicesEnv lists, in priority order, the environment variables whose
// value enumerates the GPUs visible to a container. NVIDIA_VISIBLE_DEVICES is the
// NVIDIA container runtime convention; MOCK_NVIDIA_VISIBLE_DEVICES is injected by
// the fake-gpu-operator on GPU-less test clusters.
var visibleDevicesEnv = []string{"NVIDIA_VISIBLE_DEVICES", "MOCK_NVIDIA_VISIBLE_DEVICES"}

// GPUDevices returns the GPU devices advertised by the container's
// visible-devices environment variable. Each comma-separated token becomes a
// device: a numeric token is recorded as an Index, anything else (e.g. a GPU
// UUID, as injected by the fake-gpu-operator) as a UUID. The sentinel values "",
// "none", "void" and the wildcard "all" yield no GPU.
func GPUDevices(container *api.Container) []store.GPUDevice {
	value := visibleDevicesValue(container)
	switch value {
	case "", "none", "void":
		return nil
	}

	devices := []store.GPUDevice{}
	seen := map[string]struct{}{}
	for _, token := range strings.Split(value, ",") {
		token = strings.TrimSpace(token)
		if token == "" || token == "all" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		if index, err := strconv.Atoi(token); err == nil {
			devices = append(devices, store.GPUDevice{Index: index})
			continue
		}
		devices = append(devices, store.GPUDevice{UUID: token})
	}
	return devices
}

// visibleDevicesValue returns the value of the highest-priority visible-devices
// env var set on the container, or "" if none is set.
func visibleDevicesValue(container *api.Container) string {
	env := container.GetEnv()
	for _, name := range visibleDevicesEnv {
		prefix := name + "="
		for _, kv := range env {
			if !strings.HasPrefix(kv, prefix) {
				continue
			}
			if value := strings.TrimSpace(strings.TrimPrefix(kv, prefix)); value != "" {
				return value
			}
		}
	}
	return ""
}
