// Package suite ties setup together for a run of an e2e test binary: one
// cluster connection, reused by every test. Setup is exposed as ordered phases
// (New → Preflight → Deploy) so a suite's TestMain can run — and report — each
// step explicitly, and fail fast on preconditions before the expensive deploy.
package suite

import (
	"context"
	"fmt"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/config"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/plugin"
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
// a fast, side-effect-free precondition check meant to run before Deploy so a
// misconfigured cluster fails immediately with a clear message.
func (s *Suite) Preflight(ctx context.Context) error {
	if err := nodes.VerifyGPUNodes(ctx, s.Client); err != nil {
		return fmt.Errorf("cluster is not in the expected configuration: %w", err)
	}
	return nil
}

// Deploy applies the gpu-sharing-plugin DaemonSet under test and waits for its
// rollout.
func (s *Suite) Deploy(ctx context.Context) error {
	if err := plugin.Deploy(ctx, s.Client); err != nil {
		return fmt.Errorf("deploy gpu-sharing-plugin: %w", err)
	}
	return nil
}
