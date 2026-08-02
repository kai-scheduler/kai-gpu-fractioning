//go:build e2e

package harness

import (
	"context"
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// operatorDeployment returns the operator's Deployment. The operator namespace
// contains exactly one Deployment — the data-plane components (sharingd, mpsd)
// are DaemonSets, not Deployments — so there is no ambiguity to resolve and no
// dependence on the Helm release name.
func (h *Harness) operatorDeployment(ctx context.Context) (*appsv1.Deployment, error) {
	var list appsv1.DeploymentList
	if err := h.Client().Ctrl.List(ctx, &list, ctrlclient.InNamespace(h.NS())); err != nil {
		return nil, err
	}
	if len(list.Items) != 1 {
		return nil, fmt.Errorf("expected exactly one Deployment in namespace %s (the operator), found %d — is the operator installed?", h.NS(), len(list.Items))
	}
	return &list.Items[0], nil
}

// OperatorDeployment returns the operator's Deployment, failing the test if it
// is not present.
func (h *Harness) OperatorDeployment(ctx context.Context, t *testing.T) *appsv1.Deployment {
	t.Helper()
	dep, err := h.operatorDeployment(ctx)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return dep
}
