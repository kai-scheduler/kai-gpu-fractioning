//go:build e2e

package harness

import "fmt"

// Shared identifiers the e2e suites assert against. These mirror values in the
// operator/daemon source (operator/internal/common/daemonmgr,
// sharing-manager/common/configuration, sharing-manager/sharingd/internal/
// injection). Those packages are internal to their own modules and can't be
// imported from test/e2e, so the constants are redeclared here and kept in sync
// with the operator/sharing-manager source.
//
// Only genuinely cross-suite identifiers live here; suite-specific ones (e.g.
// the operator's per-daemon status reasons, mpsd runtime class / container
// names) stay in that suite's package.
const (
	// Node condition the operator sets on each targeted node (daemonmgr.NodeConditionType).
	NodeConditionType = "gpu-sharing.nvidia.com/Ready"

	// CR status condition types (daemonmgr: "<daemon>Ready" + ConditionReady).
	CondSharingdReady = "SharingdReady"
	CondMpsdReady     = "MpsdReady"
	CondReady         = "Ready"

	// sharingd annotation prefix (configuration.DefaultAnnotationPrefix).
	AnnotationPrefix = "nvidia.com/container."

	// Env vars sharingd injects into a GPU-sharing container (injection pkg).
	EnvGPUMemRequests = "NVIDIA_GPU_MEMORY_REQUESTS"
	EnvGPUMemLimits   = "NVIDIA_GPU_MEMORY_LIMITS"
	EnvMPSPipeDir     = "CUDA_MPS_PIPE_DIRECTORY"

	// MPS pipe dir (configuration.DefaultMPSPipeDirectory).
	MPSPipeDir = "/run/nvidia-mps"

	// Component label values (app.kubernetes.io/component) on each managed DS.
	ComponentSharingd = "sharingd"
	ComponentMpsd     = "mpsd"

	// Management labels the operator stamps on the DaemonSets it owns
	// (daemonmgr LabelManagedBy/ManagedByValue/LabelComponent).
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	ManagedByValue = "gpu-sharing"

	// Namespace for the suites' own workload pods (kept off any scheduler
	// project namespace this cluster may not have).
	WorkloadNamespace = "operator-e2e"

	// The GPU-sharing container name used by the annotated test workload.
	WorkloadContainer = "cuda"

	// GPU-memory request/limit the sharingd-injection cases stamp onto the
	// annotated workload (MiB, no unit suffix). These are arbitrary test
	// inputs, not a cluster property: sharingd parses them as k8s Quantities
	// and deterministically re-emits decimal MB regardless of the underlying
	// GPU (2048Mi→2147MB, 4096Mi→4294MB; see ExpectedDecimalMB).
	MemRequestMiB = "2048"
	MemLimitMiB   = "4096"

	// dsNamePrefix + "-" + component is the DaemonSet name the operator builds.
	dsNamePrefix = "gpu-sharing"
)

// DSName returns the DaemonSet name for a component, e.g. "gpu-sharing-mpsd".
func DSName(component string) string { return fmt.Sprintf("%s-%s", dsNamePrefix, component) }

// ComponentSelector returns the label selector matching a component's pods.
func ComponentSelector(component string) string {
	return fmt.Sprintf("%s=%s,%s=%s", LabelManagedBy, ManagedByValue, LabelComponent, component)
}
