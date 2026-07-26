package daemonmgr

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	ManagedByValue = "gpu-sharing"
)

// ImageSpec holds a container image reference injected by the Helm chart.
// It is an internal operator type — it is NOT part of the GpuSharingConfig CRD.
type ImageSpec struct {
	Repository      string
	Tag             string
	ImagePullPolicy string
}

// FullImage returns the complete image reference: <Repository>:<Tag>.
// If Tag is empty, returns just <Repository>. If Repository is empty, returns "".
func (s ImageSpec) FullImage() string {
	if s.Repository == "" {
		return ""
	}
	if s.Tag != "" {
		return fmt.Sprintf("%s:%s", s.Repository, s.Tag)
	}
	return s.Repository
}

// PullPolicy returns the corev1.PullPolicy for this image, defaulting to IfNotPresent.
func (s ImageSpec) PullPolicy() corev1.PullPolicy {
	if s.ImagePullPolicy != "" {
		return corev1.PullPolicy(s.ImagePullPolicy)
	}
	return corev1.PullIfNotPresent
}

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
	Namespace     string               // namespace for the DaemonSet
	NodeSelector  map[string]string    // selects which nodes the daemon targets
	DefaultImages map[string]ImageSpec // Helm-injected default images keyed by daemon name
}

// DaemonHealth holds the observed health of a ManagedDaemon after reconciliation.
// Only aggregate counters are kept (mirroring DaemonSet status), to avoid scaling
// issues on large clusters.
type DaemonHealth struct {
	DesiredNodes int32
	ReadyNodes   int32
}
