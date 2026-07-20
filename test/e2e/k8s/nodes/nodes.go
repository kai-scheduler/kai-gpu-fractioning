// Package nodes verifies GPU node preconditions (count, labels). It never
// labels, cordons, or otherwise mutates nodes.
package nodes

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/kai-scheduler/gpu-sharing/test/e2e/k8s/cluster"
)

// ListGPUNodes returns all nodes matching c.Config.GPUNodeSelector.
func ListGPUNodes(ctx context.Context, c *cluster.Client) ([]corev1.Node, error) {
	sel, err := labels.Parse(c.Config.GPUNodeSelector)
	if err != nil {
		return nil, fmt.Errorf("parse GPU node selector %q: %w", c.Config.GPUNodeSelector, err)
	}
	var list corev1.NodeList
	if err := c.Ctrl.List(ctx, &list, ctrlclient.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, fmt.Errorf("list nodes matching %q: %w", c.Config.GPUNodeSelector, err)
	}
	return list.Items, nil
}

// VerifyGPUNodes asserts that exactly c.Config.GPUNodeCount nodes match
// c.Config.GPUNodeSelector. With GPUNodeCount == 0 there is nothing to
// assert, so it returns immediately without parsing the selector or listing nodes.
func VerifyGPUNodes(ctx context.Context, c *cluster.Client) error {
	if c.Config.GPUNodeCount == 0 {
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
		return fmt.Errorf("no nodes matched GPU node selector %q — is the cluster up and are its GPU nodes labeled", c.Config.GPUNodeSelector)
	}

	if len(nodeList.Items) != c.Config.GPUNodeCount {
		return fmt.Errorf("expected %d GPU node(s) matching %q, found %d", c.Config.GPUNodeCount, c.Config.GPUNodeSelector, len(nodeList.Items))
	}

	return nil
}
