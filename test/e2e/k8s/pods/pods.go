// Package pods provides list helpers for pods running in the cluster.
package pods

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
)

// ListByLabel lists pods in namespace matching labelSelector.
func ListByLabel(ctx context.Context, c *cluster.Client, namespace, labelSelector string) ([]corev1.Pod, error) {
	sel, err := labels.Parse(labelSelector)
	if err != nil {
		return nil, fmt.Errorf("parse label selector %q: %w", labelSelector, err)
	}

	var list corev1.PodList
	if err := c.Ctrl.List(ctx, &list, ctrlclient.InNamespace(namespace), ctrlclient.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, fmt.Errorf("list pods matching %q in namespace %s: %w", labelSelector, namespace, err)
	}
	return list.Items, nil
}
