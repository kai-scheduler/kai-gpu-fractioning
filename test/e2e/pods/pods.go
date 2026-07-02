// Package pods provides list and exec helpers for pods running in the
// cluster.
package pods

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
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

// Exec runs command inside container of the named pod and returns its
// stdout, equivalent to `kubectl exec <pod> -c <container> -- <command>`.
// Used by attribution tests to read env vars (e.g.
// MOCK_NVIDIA_VISIBLE_DEVICES) the device plugin injected into a running
// container — information not visible on the stored Pod spec.
func Exec(ctx context.Context, c *cluster.Client, namespace, name, container string, command []string) (string, error) {
	req := c.RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.REST, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("build executor for %s/%s: %w", namespace, name, err)
	}

	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return "", fmt.Errorf("exec %v in %s/%s: %w (stderr: %s)", command, namespace, name, err, stderr.String())
	}
	return stdout.String(), nil
}
