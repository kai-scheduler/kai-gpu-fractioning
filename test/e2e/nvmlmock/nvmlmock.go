// Package nvmlmock drives the nvml-mock DaemonSet so per-pod GPU metrics are
// deterministic in the e2e suite.
//
// nvml-mock ships a real libnvidia-ml.so that answers NVML calls from a static
// config (see sharing-manager/metricsd/deploy/fake-gpu-cluster/nvml-mock.yaml):
// it reports exactly the GPU processes listed under devices[].processes, keyed
// by PID. There is no dynamic host-process discovery, so to make NVML attribute
// memory to a real pod this package:
//
//  1. resolves the workload container's host-namespace PID (HostPID), and
//  2. rewrites the nvml-mock config so that PID appears as a GPU process on a
//     chosen device UUID, then restarts nvml-mock and the plugin (SetProcesses).
//
// The plugin then reads NVML -> {PID, UUID, memory}, maps PID -> cgroup -> pod
// (via /host/proc), and exports gpu_sharing_gpu_memory_used_bytes for that pod.
package nvmlmock

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/pods"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/waiter"
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

	// PluginNamespace / PluginDaemonSet identify the gpu-sharing-plugin under
	// test, restarted after a config change so its loaded NVML .so re-reads it.
	PluginNamespace = "gpu-sharing"
	PluginDaemonSet = "gpu-sharing-plugin"
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
}

// HostPID resolves the host-namespace PID of the workload process identified by
// marker (the token workload.Apply embeds in the container command line). It
// execs `pgrep -f <marker>` inside the nvml-mock pod on the workload's node,
// which — because that pod runs hostPID: true — sees the workload's process.
func HostPID(ctx context.Context, c *cluster.Client, nodeName, marker string) (uint32, error) {
	mockPod, err := podOnNode(ctx, c, nodeName)
	if err != nil {
		return 0, err
	}

	out, err := pods.Exec(ctx, c, Namespace, mockPod, Container,
		[]string{"sh", "-c", "pgrep -f " + shellQuote(marker)})
	if err != nil {
		return 0, fmt.Errorf("pgrep %q in %s/%s: %w", marker, Namespace, mockPod, err)
	}

	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 1 {
		return 0, fmt.Errorf("want exactly one host PID for marker %q, got %v", marker, fields)
	}
	pid, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse host PID %q: %w", fields[0], err)
	}
	return uint32(pid), nil
}

// SetProcesses rewrites the nvml-mock config for GPU model gpu (a Profiles key,
// e.g. "a100"; "" selects DefaultGPU) so its devices carry procs, then restarts
// nvml-mock (to re-run setup.sh and rewrite driver/config/config.yaml) and the
// gpu-sharing-plugin (so its loaded NVML .so re-reads the config), and waits for
// both rollouts. Passing an empty procs slice resets the mock to idle.
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

	// nvml-mock first (rewrites the on-disk config the plugin's .so reads),
	// then the plugin (reloads the .so). Order matters.
	if err := rolloutRestart(ctx, c, Namespace, DaemonSet); err != nil {
		return fmt.Errorf("restart %s/%s: %w", Namespace, DaemonSet, err)
	}
	if err := rolloutRestart(ctx, c, PluginNamespace, PluginDaemonSet); err != nil {
		return fmt.Errorf("restart %s/%s: %w", PluginNamespace, PluginDaemonSet, err)
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
			_, _ = fmt.Fprintf(&b, "      - pid: %d\n        used_gpu_memory: %d\n        type: C\n",
				p.PID, p.UsedGPUMemory)
		}
	}
	return b.String()
}

// rolloutRestart bumps a template annotation to force a DaemonSet rollout, then
// waits for it to complete — the controller-runtime equivalent of
// `kubectl rollout restart` + `kubectl rollout status`.
func rolloutRestart(ctx context.Context, c *cluster.Client, ns, name string) error {
	var ds appsv1.DaemonSet
	if err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, &ds); err != nil {
		return err
	}
	if ds.Spec.Template.Annotations == nil {
		ds.Spec.Template.Annotations = map[string]string{}
	}
	ds.Spec.Template.Annotations["nvmlmock.e2e/restartedAt"] = time.Now().Format(time.RFC3339Nano)
	if err := c.Ctrl.Update(ctx, &ds); err != nil {
		return err
	}
	return waitRollout(ctx, c, ns, name)
}

func waitRollout(ctx context.Context, c *cluster.Client, ns, name string) error {
	return waiter.PollUntil(ctx, c.Config.DaemonSetReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("daemonset %s/%s rollout", ns, name),
		func(ctx context.Context) (bool, error) {
			var ds appsv1.DaemonSet
			if err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, &ds); err != nil {
				return false, err
			}
			s := ds.Status
			ready := s.ObservedGeneration >= ds.Generation &&
				s.DesiredNumberScheduled > 0 &&
				s.UpdatedNumberScheduled == s.DesiredNumberScheduled &&
				s.NumberReady == s.DesiredNumberScheduled &&
				s.NumberUnavailable == 0
			return ready, nil
		})
}

// shellQuote single-quotes s for safe interpolation into `sh -c`. Markers are
// alphanumeric+hyphen today, but quoting keeps HostPID robust if that changes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
