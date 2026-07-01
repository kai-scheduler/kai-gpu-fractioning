package daemonmgr

import (
	appsv1 "k8s.io/api/apps/v1"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
)

const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	ManagedByValue = "gpu-sharing-operator"
)

// ManagedDaemon describes a daemon whose lifecycle is managed by the controller.
// Each implementation (sharingd, mpsd) defines the DaemonSet spec for its daemon;
// the shared reconciler handles create-or-update, health tracking, and reporting.
type ManagedDaemon interface {
	// Name returns a unique identifier used in DaemonSet names, conditions, and logs
	// (e.g. "sharingd", "mpsd").
	Name() string

	// BuildDaemonSet returns the desired DaemonSet for this daemon.
	BuildDaemonSet(opts BuildOptions) *appsv1.DaemonSet
}

// BuildOptions carries the context needed by ManagedDaemon.BuildDaemonSet.
type BuildOptions struct {
	Namespace     string                        // namespace for the DaemonSet
	NodeSelector  map[string]string             // selects which nodes the daemon targets
	DefaultImages map[string]v1alpha1.ImageSpec // Helm-injected default images keyed by daemon name
}

// DaemonHealth holds the observed health of a ManagedDaemon after reconciliation.
// Only aggregate counters are kept (mirroring DaemonSet status), to avoid scaling
// issues on large clusters.
type DaemonHealth struct {
	DesiredNodes int32
	ReadyNodes   int32
}
