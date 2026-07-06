// Package suite ties setup together for a run of the e2e test binary: one
// cluster connection, reused by every test.
package suite

import (
	"context"
	"fmt"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/config"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/nodes"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/plugin"
)

// Suite holds the shared state for a run of the e2e test binary. The cluster
// itself (fake-gpu-operator + GPU nodes) must already exist — see
// test/e2e/hack/create-cluster.py — Setup only verifies it and deploys the
// gpu-sharing-plugin DaemonSet under test.
type Suite struct {
	Client *cluster.Client
}

// New connects to the cluster, verifies its GPU node count/labels match what's
// expected, and deploys the gpu-sharing-plugin DaemonSet under test.
func New(ctx context.Context) (*Suite, error) {
	cfg := config.Load()

	c, err := cluster.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to cluster: %w", err)
	}

	if err := nodes.VerifyGPUNodes(ctx, c); err != nil {
		return nil, fmt.Errorf("cluster is not in the expected configuration: %w", err)
	}

	if err := plugin.Deploy(ctx, c); err != nil {
		return nil, fmt.Errorf("deploy gpu-sharing-plugin: %w", err)
	}

	return &Suite{Client: c}, nil
}
