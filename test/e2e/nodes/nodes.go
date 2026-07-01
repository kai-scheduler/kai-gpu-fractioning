// Package nodes verifies GPU node preconditions (count, labels). It never
// labels, cordons, or otherwise mutates nodes.
package nodes

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
)

// VerifyGPUNodes lists nodes matching c.Config.GPUNodeSelector and, if
// c.Config.ExpectedGPUNodes > 0, asserts the count matches.
func VerifyGPUNodes(ctx context.Context, c *cluster.Client) error {
	nodeList, err := c.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: c.Config.GPUNodeSelector,
	})
	if err != nil {
		return fmt.Errorf("list nodes matching %q: %w", c.Config.GPUNodeSelector, err)
	}

	if len(nodeList.Items) == 0 {
		return fmt.Errorf("no nodes matched GPU node selector %q — is the fake-gpu-operator cluster up? (see test/e2e/hack/create-cluster.py)", c.Config.GPUNodeSelector)
	}

	if c.Config.ExpectedGPUNodes > 0 && len(nodeList.Items) != c.Config.ExpectedGPUNodes {
		return fmt.Errorf("expected %d GPU node(s) matching %q, found %d", c.Config.ExpectedGPUNodes, c.Config.GPUNodeSelector, len(nodeList.Items))
	}

	return nil
}
