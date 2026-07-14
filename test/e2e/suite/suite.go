// Package suite ties setup together for a run of an e2e test binary: one
// cluster connection, reused by every test. Setup is exposed as ordered phases
// (New → Preflight) so a suite's TestMain can run — and report — each step
// explicitly, and fail fast on preconditions.
//
// Deploying the component under test is NOT a suite phase: the gpu-sharing
// stack is installed by the operator Helm chart (see test/e2e/e2e.mk's
// `e2e-deploy`, run from the CI workflow or `make e2e`), and the operator
// creates the sharingd DaemonSet + metricsd sidecar. The suite only connects to
// an already-deployed cluster and asserts.
package suite

import (
	"context"
	"fmt"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/config"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
)

// Suite holds the shared state for a run of an e2e test binary. It only
// connects to an existing cluster (whatever E2E_KUBECONFIG points at); it never
// provisions one.
type Suite struct {
	Client *cluster.Client
}

// New connects to the cluster. It does not verify preconditions or deploy
// anything — callers drive those with Preflight and Deploy so the ordering is
// explicit in each suite's TestMain.
func New(ctx context.Context) (*Suite, error) {
	c, err := cluster.NewClient(config.Load())
	if err != nil {
		return nil, fmt.Errorf("connect to cluster: %w", err)
	}
	return &Suite{Client: c}, nil
}

// Preflight verifies the cluster's GPU nodes match what the run expects. It is
// a fast, side-effect-free precondition check so a misconfigured cluster fails
// immediately with a clear message before any test runs.
func (s *Suite) Preflight(ctx context.Context) error {
	if err := nodes.VerifyGPUNodes(ctx, s.Client); err != nil {
		return fmt.Errorf("cluster is not in the expected configuration: %w", err)
	}
	return nil
}
