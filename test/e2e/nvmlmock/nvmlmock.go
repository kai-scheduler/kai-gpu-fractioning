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

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/pods"
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

	// SharingdDaemonSet is the operator-managed DaemonSet that hosts the metricsd
	// sidecar under test. It's restarted after a config change so the sidecar's
	// loaded NVML .so re-reads it. Its namespace is c.Config.OperatorNamespace.
	SharingdDaemonSet = "gpu-sharing-sharingd"
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

	// nvml-mock first (rewrites the on-disk config the sidecar's .so reads),
	// then sharingd (reloads the .so). Order matters.
	if err := restartNVMLMock(ctx, c); err != nil {
		return fmt.Errorf("restart %s/%s: %w", Namespace, DaemonSet, err)
	}
	if err := rolloutRestart(ctx, c, c.Config.OperatorNamespace, SharingdDaemonSet); err != nil {
		return fmt.Errorf("restart %s/%s: %w", c.Config.OperatorNamespace, SharingdDaemonSet, err)
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
	if err := bumpRestartAnnotation(ctx, c, ns, name); err != nil {
		return err
	}
	return waiter.PollUntil(ctx, c.Config.DaemonSetReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("daemonset %s/%s rollout", ns, name),
		func(ctx context.Context) (bool, error) { return daemonSetReady(ctx, c, ns, name) })
}

// restartNVMLMock restarts the nvml-mock DaemonSet like rolloutRestart, but
// re-applies the GPU node label on every poll tick while it waits.
//
// nvml-mock's preStop hook (cleanup.sh) runs `kubectl label node ...
// nvidia.com/gpu.present-` on shutdown, stripping the very label its own
// nodeSelector requires. That label is bootstrapped only once, at cluster
// creation (k3d --k3s-node-label), and k3s never re-adds it. So a plain rolling
// restart deletes the old pod, the label vanishes, no node matches the
// nodeSelector, the replacement never schedules (DesiredNumberScheduled drops to
// 0), and the rollout deadlocks until the timeout. Re-labelling the GPU nodes on
// each tick lets the replacement schedule; setup.sh re-applies the label too
// once it runs, but we can't wait for that — scheduling has to happen first.
func restartNVMLMock(ctx context.Context, c *cluster.Client) error {
	key, value, err := parseEqualsSelector(c.Config.GPUNodeSelector)
	if err != nil {
		return err
	}
	// Capture the GPU nodes now, while the label is still present — once it's
	// stripped we can no longer select them by it.
	nodes, err := gpuNodeNames(ctx, c, key, value)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes match GPU selector %q before restart", c.Config.GPUNodeSelector)
	}

	if err := bumpRestartAnnotation(ctx, c, Namespace, DaemonSet); err != nil {
		return err
	}
	return waiter.PollUntil(ctx, c.Config.DaemonSetReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("daemonset %s/%s rollout", Namespace, DaemonSet),
		func(ctx context.Context) (bool, error) {
			if err := relabelNodes(ctx, c, nodes, key, value); err != nil {
				return false, err
			}
			return daemonSetReady(ctx, c, Namespace, DaemonSet)
		})
}

// bumpRestartAnnotation writes a fresh timestamp into the DaemonSet's pod
// template, which the controller treats as a rollout trigger.
func bumpRestartAnnotation(ctx context.Context, c *cluster.Client, ns, name string) error {
	var ds appsv1.DaemonSet
	if err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Namespace: ns, Name: name}, &ds); err != nil {
		return err
	}
	if ds.Spec.Template.Annotations == nil {
		ds.Spec.Template.Annotations = map[string]string{}
	}
	ds.Spec.Template.Annotations["nvmlmock.e2e/restartedAt"] = time.Now().Format(time.RFC3339Nano)
	return c.Ctrl.Update(ctx, &ds)
}

// daemonSetReady reports whether every desired pod of a DaemonSet is updated,
// ready, and available at the DaemonSet's current generation.
func daemonSetReady(ctx context.Context, c *cluster.Client, ns, name string) (bool, error) {
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
}

// parseEqualsSelector splits a single "key=value" label selector (the form of
// config.GPUNodeSelector) into its key and value.
func parseEqualsSelector(sel string) (key, value string, err error) {
	k, v, ok := strings.Cut(sel, "=")
	if !ok || k == "" {
		return "", "", fmt.Errorf("GPU node selector %q is not a simple key=value selector", sel)
	}
	return k, v, nil
}

// gpuNodeNames returns the names of nodes currently carrying key=value.
func gpuNodeNames(ctx context.Context, c *cluster.Client, key, value string) ([]string, error) {
	var list corev1.NodeList
	if err := c.Ctrl.List(ctx, &list, ctrlclient.MatchingLabels{key: value}); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	return names, nil
}

// relabelNodes ensures each named node carries key=value, patching only those
// that don't already (so a steady state is a cheap no-op).
func relabelNodes(ctx context.Context, c *cluster.Client, names []string, key, value string) error {
	for _, name := range names {
		var node corev1.Node
		if err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Name: name}, &node); err != nil {
			return err
		}
		if node.Labels[key] == value {
			continue
		}
		patch := ctrlclient.MergeFrom(node.DeepCopy())
		if node.Labels == nil {
			node.Labels = map[string]string{}
		}
		node.Labels[key] = value
		if err := c.Ctrl.Patch(ctx, &node, patch); err != nil {
			return err
		}
	}
	return nil
}
