// Package nvmlmock drives the nvml-mock ConfigMap so per-pod GPU metrics are
// deterministic in the e2e suite.
//
// The metricsd e2e binary (Dockerfile.e2e, compiled with -tags e2e) replaces
// the NVML collector with a ConfigMap-backed collector that polls
// gpu-operator/nvml-mock-config on every collection cycle. To make NVML
// attribute memory to a real pod this package:
//
//  1. resolves the workload container's host-namespace PID (HostPID), and
//  2. rewrites the nvml-mock ConfigMap so that PID appears as a GPU process on
//     a chosen device UUID (SetProcesses).
//
// The e2e collector picks up the new config within one collection interval
// (~5 s) without requiring any DaemonSet restarts.
package nvmlmock

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/pods"
)

const (
	// Namespace / ConfigMapName / DaemonSet identify the nvml-mock objects
	// installed by test/e2e/hack/create-cluster.py from
	// sharing-manager/metricsd/deploy/fake-gpu-cluster/nvml-mock.yaml.
	Namespace     = "gpu-operator"
	ConfigMapName = "nvml-mock-config"
	DaemonSet     = "nvml-mock"

	// Container is the nvml-mock pod's container name; it runs hostPID: true and
	// privileged, so an exec into it sees the whole node's process table.
	Container = "nvml-mock"
)

// Device UUIDs baked into the nvml-mock config. Callers pin a Proc to one of
// these; the exported gpu_uuid label equals the chosen value.
const (
	Device0UUID = "2881be89-519e-4b30-8d25-b9032893e6f9"
	Device1UUID = "1a1fb9fb-a857-4986-8b0f-d0d664ba0364"
)

// Proc is one mocked GPU process pinned to a real host PID.
type Proc struct {
	UUID          string // must equal a device uuid below (Device0UUID/Device1UUID)
	PID           uint32 // real host-namespace PID (from HostPID)
	UsedGPUMemory uint64 // bytes; surfaced via nvmlDeviceGetComputeRunningProcesses
	// SMUtil is the per-process SM utilization percent (0–100) reported via
	// GetProcessUtilization. Non-zero values produce a non-zero
	// gpu_sharing_gpu_sm_utilization_percent metric.
	SMUtil uint32
}

// HostPID resolves the host-namespace PID of the workload process identified by
// marker (the token workload.Apply embeds in the container command line). It
// scans /proc/<pid>/cmdline inside the nvml-mock pod on the workload's node,
// which — because that pod runs hostPID: true — sees the workload's process.
//
// The nvml-mock image (Debian, ghcr.io/nvidia/nvml-mock) ships no procps, so
// pgrep/ps are unavailable; grep over /proc is the portable alternative. The
// marker is fed on stdin (grep -f /dev/stdin), not as a command argument, so it
// never lands in the search command's own /proc/<pid>/cmdline — otherwise grep
// and its parent shell would match themselves and the marker would resolve to
// more than one PID.
func HostPID(ctx context.Context, c *cluster.Client, nodeName, marker string) (uint32, error) {
	mockPod, err := podOnNode(ctx, c, nodeName)
	if err != nil {
		return 0, err
	}

	// -l: list matching files, -a: treat NUL-separated cmdline as text,
	// -F: fixed string (the marker), -f /dev/stdin: read the pattern from stdin.
	// `|| true`: a short-lived process can exit mid-scan, leaving grep unable to
	// read its /proc/<pid>/cmdline; grep then exits 2 (error) even though it
	// printed the real match. Swallow that so the exec succeeds — the match-count
	// check below is the real validation.
	out, err := pods.ExecStdin(ctx, c, Namespace, mockPod, Container,
		[]string{"sh", "-c", "grep -laF -f /dev/stdin /proc/[0-9]*/cmdline 2>/dev/null || true"}, marker)
	if err != nil {
		return 0, fmt.Errorf("scan /proc for marker %q in %s/%s: %w", marker, Namespace, mockPod, err)
	}

	lines := strings.Fields(strings.TrimSpace(out))
	if len(lines) != 1 {
		return 0, fmt.Errorf("want exactly one host PID for marker %q, matched %v", marker, lines)
	}
	// lines[0] is "/proc/<pid>/cmdline"; extract <pid>.
	pidStr := strings.TrimSuffix(strings.TrimPrefix(lines[0], "/proc/"), "/cmdline")
	pid, err := strconv.ParseUint(pidStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse host PID from %q: %w", lines[0], err)
	}
	return uint32(pid), nil
}

// SetProcesses rewrites the nvml-mock ConfigMap for GPU model gpu (a Profiles
// key, e.g. "a100"; "" selects DefaultGPU) so its devices carry procs. The
// metricsd e2e collector polls this ConfigMap on each collection cycle (~5 s)
// and picks up the change without requiring any DaemonSet restarts. Passing an
// empty procs slice resets the mock to idle.
func SetProcesses(ctx context.Context, c *cluster.Client, gpu string, procs []Proc) error {
	if gpu == "" {
		gpu = DefaultGPU
	}
	profile, ok := Profiles[gpu]
	if !ok {
		return fmt.Errorf("unknown GPU profile %q (known: %s)", gpu, knownProfiles())
	}

	var cm corev1.ConfigMap
	if err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Namespace: Namespace, Name: ConfigMapName}, &cm); err != nil {
		return fmt.Errorf("get configmap %s/%s: %w", Namespace, ConfigMapName, err)
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data["config.yaml"] = renderConfig(profile, procs)
	if err := c.Ctrl.Update(ctx, &cm); err != nil {
		return fmt.Errorf("update configmap %s/%s: %w", Namespace, ConfigMapName, err)
	}
	return nil
}

func podOnNode(ctx context.Context, c *cluster.Client, nodeName string) (string, error) {
	mockPods, err := pods.ListByLabel(ctx, c, Namespace, "app=nvml-mock")
	if err != nil {
		return "", fmt.Errorf("list nvml-mock pods: %w", err)
	}
	for i := range mockPods {
		if mockPods[i].Spec.NodeName == nodeName {
			return mockPods[i].Name, nil
		}
	}
	return "", fmt.Errorf("no nvml-mock pod scheduled on node %q", nodeName)
}

// renderConfig emits an nvml-mock config for the given GPU profile with two
// devices, attaching each Proc to the device whose uuid it names. Schema mirrors
// sharing-manager/metricsd/deploy/fake-gpu-cluster/nvml-mock.yaml.
func renderConfig(profile GPUProfile, procs []Proc) string {
	byUUID := map[string][]Proc{}
	for _, p := range procs {
		byUUID[p.UUID] = append(byUUID[p.UUID], p)
	}

	devices := []struct {
		index int
		uuid  string
		bus   string
	}{
		{0, Device0UUID, "0000:07:00.0"},
		{1, Device1UUID, "0000:0F:00.0"},
	}

	var b strings.Builder
	// Writes to strings.Builder never fail; blank-assign to satisfy errcheck
	// without the QF1012 churn of WriteString(fmt.Sprintf(...)).
	_, _ = fmt.Fprintf(&b, `version: "1.0"
system:
  driver_version: %q
  nvml_version: %q
  cuda_version: %q
device_defaults:
  name: %q
  memory:
    total_bytes: %d
    reserved_bytes: %d
    free_bytes: %d
    used_bytes: %d
  utilization:
    gpu: 0
    memory: 0
  compute_mode: "default"
  persistence_mode: "enabled"
  processes: []
devices:
`, profile.DriverVersion, profile.NVMLVersion, profile.CUDAVersion, profile.Name,
		profile.MemoryTotalBytes, profile.MemoryReservedBytes, profile.MemoryFreeBytes, profile.MemoryUsedBytes)
	for _, d := range devices {
		_, _ = fmt.Fprintf(&b, "  - index: %d\n    uuid: %q\n    pci:\n      bus_id: %q\n    minor_number: %d\n    processes:\n",
			d.index, d.uuid, d.bus, d.index)
		ps := byUUID[d.uuid]
		if len(ps) == 0 {
			b.WriteString("      []\n")
			continue
		}
		for _, p := range ps {
			_, _ = fmt.Fprintf(&b, "      - pid: %d\n        used_gpu_memory: %d\n        sm_util: %d\n        type: C\n",
				p.PID, p.UsedGPUMemory, p.SMUtil)
		}
	}
	return b.String()
}
