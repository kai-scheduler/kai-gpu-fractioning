//go:build e2e

package harness

import (
	"context"
	"sort"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// listOperatorDeployments returns the Deployments in the operator namespace.
// The Helm release name is not assumed — callers pick the operator among them.
func (h *Harness) listOperatorDeployments(ctx context.Context) ([]appsv1.Deployment, error) {
	var list appsv1.DeploymentList
	if err := h.Client().Ctrl.List(ctx, &list, ctrlclient.InNamespace(h.NS())); err != nil {
		return nil, err
	}
	// Prefer a name containing "gpu-sharing" first so it's deterministic.
	sort.Slice(list.Items, func(i, j int) bool {
		gi := strings.Contains(list.Items[i].Name, "gpu-sharing")
		gj := strings.Contains(list.Items[j].Name, "gpu-sharing")
		if gi != gj {
			return gi
		}
		return list.Items[i].Name < list.Items[j].Name
	})
	return list.Items, nil
}

// OperatorDeployment returns the operator's Deployment. The operator namespace
// holds exactly one Deployment (the manager); we take the sole one, preferring a
// name containing "gpu-sharing".
func (h *Harness) OperatorDeployment(ctx context.Context, t *testing.T) *appsv1.Deployment {
	t.Helper()
	deps, err := h.listOperatorDeployments(ctx)
	if err != nil {
		t.Fatalf("list deployments in %s: %v", h.NS(), err)
	}
	if len(deps) == 0 {
		t.Fatalf("no Deployment found in namespace %s — is the operator installed?", h.NS())
	}
	return &deps[0]
}
