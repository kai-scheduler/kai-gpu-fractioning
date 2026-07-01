// Package pods provides list helpers for pods running in the cluster.
package pods

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
)

// ListByLabel lists pods in namespace matching labelSelector.
func ListByLabel(ctx context.Context, c *cluster.Client, namespace, labelSelector string) ([]corev1.Pod, error) {
	list, err := c.Typed.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list pods matching %q in namespace %s: %w", labelSelector, namespace, err)
	}
	return list.Items, nil
}
