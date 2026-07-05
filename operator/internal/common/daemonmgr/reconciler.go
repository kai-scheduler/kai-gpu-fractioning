package daemonmgr

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ReconcileDaemon ensures the DaemonSet for the given daemon exists and matches the
// desired spec. It returns the observed health derived from the DaemonSet status and
// its pods.
func ReconcileDaemon(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner metav1.Object,
	daemon ManagedDaemon,
	opts BuildOptions,
) (*DaemonHealth, error) {
	log := logf.FromContext(ctx).WithValues("daemon", daemon.Name())

	desired := daemon.BuildDaemonSet(opts)
	dsKey := types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}

	existing := appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}

	// Idempotent create-or-update: on first run (CreationTimestamp zero) the full
	// metadata is copied; on subsequent runs only the spec is patched so that
	// Kubernetes can perform a rolling update. The owner reference is always set
	// to ensure the DaemonSet is garbage-collected when the CR is deleted.
	result, err := controllerutil.CreateOrUpdate(ctx, c, &existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return SetOwnerReference(&existing, owner, scheme)
	})
	if err != nil {
		return nil, fmt.Errorf("create-or-update DaemonSet %s: %w", dsKey, err)
	}

	log.Info("reconciled DaemonSet", "result", result)

	// Re-fetch the DaemonSet to get accurate status.
	if err := c.Get(ctx, dsKey, &existing); err != nil {
		return nil, fmt.Errorf("fetching DaemonSet status for %s: %w", dsKey, err)
	}

	health := daemonSetHealth(&existing)
	return health, nil
}

// daemonSetHealth builds a DaemonHealth from the DaemonSet status counters.
func daemonSetHealth(ds *appsv1.DaemonSet) *DaemonHealth {
	return &DaemonHealth{
		DesiredNodes: ds.Status.DesiredNumberScheduled,
		ReadyNodes:   ds.Status.NumberReady,
	}
}
