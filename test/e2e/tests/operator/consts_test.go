//go:build e2e

package operator

// Operator-only identifiers. Cross-suite identifiers live in the harness package
// and are referenced as harness.* directly. These stay local because only the
// operator suite asserts against them (per-daemon status reasons, mpsd's
// hardcoded runtime class + control socket, and the DaemonSet container names).
const (
	// Per-daemon condition reasons (daemonmgr/status.go).
	reasonAllPodsReady       = "AllPodsReady"
	reasonNoTargetNodes      = "NoTargetNodes"
	reasonPartiallyAvailable = "PartiallyAvailable"
	reasonRolloutInProgress  = "RolloutInProgress"

	// Aggregate + node condition reasons (daemonmgr/status.go, conditions.go).
	reasonAllComponentsReady = "AllComponentsReady"
	reasonAllDaemonsReady    = "AllDaemonsReady"

	// MPS control socket (mpsd readiness probe target).
	mpsControlSocket = "/run/nvidia-mps/control"

	// mpsd hardcodes this RuntimeClass (mpsd.go). On the fake cluster it resolves
	// to the runc handler (create-cluster.py); asserted by MpsdPodSpec.
	nvidiaRuntimeClass = "nvidia"

	// DaemonSet pod-template container names.
	containerSharingd = "sharingd"
	containerMetricsd = "metricsd"
	containerMpsd     = "mpsd"
)
