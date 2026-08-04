//go:build e2e

package harness

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"

	"github.com/kai-scheduler/kai-gpu-fractioning/test/e2e/k8s/daemonset"
)

// GetDaemonSet is a fatal-on-error DaemonSet reader for a component.
func (h *Harness) GetDaemonSet(ctx context.Context, t *testing.T, component string) *appsv1.DaemonSet {
	t.Helper()
	ds, err := daemonset.Get(ctx, h.Client(), h.NS(), DSName(component))
	if err != nil {
		t.Fatalf("get DaemonSet %s: %v", DSName(component), err)
	}
	return ds
}
