// Package nodes verifies GPU node preconditions (count, labels). It never
// labels, cordons, or otherwise mutates nodes.
package nodes

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
)

// VerifyGPUNodes asserts that exactly c.Config.ExpectedGPUNodes nodes match
// c.Config.GPUNodeSelector. With ExpectedGPUNodes == 0 there is nothing to
// assert, so it returns immediately without parsing the selector or listing nodes.
func VerifyGPUNodes(ctx context.Context, c *cluster.Client) error {
	if c.Config.ExpectedGPUNodes == 0 {
		return nil
	}

	sel, err := labels.Parse(c.Config.GPUNodeSelector)
	if err != nil {
		return fmt.Errorf("parse GPU node selector %q: %w", c.Config.GPUNodeSelector, err)
	}

	var nodeList corev1.NodeList
	if err := c.Ctrl.List(ctx, &nodeList, ctrlclient.MatchingLabelsSelector{Selector: sel}); err != nil {
		return fmt.Errorf("list nodes matching %q: %w", c.Config.GPUNodeSelector, err)
	}

	if len(nodeList.Items) == 0 {
		return fmt.Errorf("no nodes matched GPU node selector %q — is the fake-gpu-operator cluster up? (see test/e2e/hack/create-cluster.py)", c.Config.GPUNodeSelector)
	}

	if len(nodeList.Items) != c.Config.ExpectedGPUNodes {
		return fmt.Errorf("expected %d GPU node(s) matching %q, found %d", c.Config.ExpectedGPUNodes, c.Config.GPUNodeSelector, len(nodeList.Items))
	}

	return nil
}
