// Package plugin deploys the gpu-sharing-plugin DaemonSet under test onto the
// e2e cluster and waits for its rollout. It is the one thing this e2e suite
// deploys itself — the cluster, fake-gpu-operator, and GPU nodes are
// preconditions handled by test/e2e/hack/create-cluster.py and checked by the
// nodes package, not something plugin creates.
package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/waiter"
)

// manifestPath is the daemonset.yaml under test — resolved relative to this
// source file so it works regardless of the caller's working directory.
func manifestPath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// this file: <repo>/test/e2e/plugin/deploy.go
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(repoRoot, "sharing-manager", "metricsd", "deploy", "daemonset.yaml")
}

// Deploy applies the gpu-sharing-plugin manifest (namespace, configmap,
// daemonset, service) and waits for the DaemonSet rollout to complete. It is
// safe to call repeatedly — existing objects are updated in place.
func Deploy(ctx context.Context, c *cluster.Client) error {
	path := manifestPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plugin manifest %s: %w", path, err)
	}

	var dsName, dsNamespace string
	for _, doc := range splitYAMLDocs(string(raw)) {
		var meta metav1.TypeMeta
		if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
			return fmt.Errorf("parse manifest doc kind: %w", err)
		}

		switch meta.Kind {
		case "Namespace":
			var ns corev1.Namespace
			if err := yaml.Unmarshal([]byte(doc), &ns); err != nil {
				return err
			}
			if err := applyNamespace(ctx, c, &ns); err != nil {
				return err
			}
		case "ConfigMap":
			var cm corev1.ConfigMap
			if err := yaml.Unmarshal([]byte(doc), &cm); err != nil {
				return err
			}
			if err := applyConfigMap(ctx, c, &cm); err != nil {
				return err
			}
		case "Service":
			var svc corev1.Service
			if err := yaml.Unmarshal([]byte(doc), &svc); err != nil {
				return err
			}
			if err := applyService(ctx, c, &svc); err != nil {
				return err
			}
		case "DaemonSet":
			var ds appsv1.DaemonSet
			if err := yaml.Unmarshal([]byte(doc), &ds); err != nil {
				return err
			}
			applyImageOverride(&ds, c.Config.PluginImage, c.Config.PluginImagePullPolicy)
			if err := applyNodeArchOverride(ctx, c, &ds); err != nil {
				return err
			}
			if err := applyDaemonSet(ctx, c, &ds); err != nil {
				return err
			}
			dsName, dsNamespace = ds.Name, ds.Namespace
		case "":
			// blank doc between separators
		default:
			return fmt.Errorf("plugin manifest contains unsupported kind %q", meta.Kind)
		}
	}

	if dsName == "" {
		return fmt.Errorf("plugin manifest %s did not contain a DaemonSet", path)
	}

	return WaitForDaemonSetReady(ctx, c, dsNamespace, dsName)
}

func applyImageOverride(ds *appsv1.DaemonSet, image, pullPolicy string) {
	if len(ds.Spec.Template.Spec.Containers) == 0 {
		return
	}
	container := &ds.Spec.Template.Spec.Containers[0]
	if image != "" {
		container.Image = image
	}
	if pullPolicy != "" {
		container.ImagePullPolicy = corev1.PullPolicy(pullPolicy)
	}
}

// applyNodeArchOverride rewrites the DaemonSet's kubernetes.io/arch node
// selector to match the GPU nodes actually present in the cluster. The
// checked-in manifest targets amd64 (real GPU nodes are amd64 datacenter
// servers), but local e2e clusters — e.g. k3d on Apple Silicon — may run
// arm64 nodes, which would otherwise leave the DaemonSet permanently
// unscheduled (DesiredNumberScheduled stays 0 forever).
func applyNodeArchOverride(ctx context.Context, c *cluster.Client, ds *appsv1.DaemonSet) error {
	if _, ok := ds.Spec.Template.Spec.NodeSelector["kubernetes.io/arch"]; !ok {
		return nil
	}

	nodes, err := c.Typed.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: "nvidia.com/gpu.present=true",
	})
	if err != nil {
		return fmt.Errorf("list GPU nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return nil
	}

	if arch := nodes.Items[0].Labels["kubernetes.io/arch"]; arch != "" {
		ds.Spec.Template.Spec.NodeSelector["kubernetes.io/arch"] = arch
	}
	return nil
}

func splitYAMLDocs(raw string) []string {
	docs := strings.Split(raw, "\n---")
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		if strings.TrimSpace(d) != "" {
			out = append(out, d)
		}
	}
	return out
}

func applyNamespace(ctx context.Context, c *cluster.Client, ns *corev1.Namespace) error {
	_, err := c.Typed.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func applyConfigMap(ctx context.Context, c *cluster.Client, cm *corev1.ConfigMap) error {
	client := c.Typed.CoreV1().ConfigMaps(cm.Namespace)
	if _, err := client.Create(ctx, cm, metav1.CreateOptions{}); apierrors.IsAlreadyExists(err) {
		_, err = client.Update(ctx, cm, metav1.UpdateOptions{})
		return err
	} else {
		return err
	}
}

func applyService(ctx context.Context, c *cluster.Client, svc *corev1.Service) error {
	_, err := c.Typed.CoreV1().Services(svc.Namespace).Create(ctx, svc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func applyDaemonSet(ctx context.Context, c *cluster.Client, ds *appsv1.DaemonSet) error {
	client := c.Typed.AppsV1().DaemonSets(ds.Namespace)
	if _, err := client.Create(ctx, ds, metav1.CreateOptions{}); apierrors.IsAlreadyExists(err) {
		_, err = client.Update(ctx, ds, metav1.UpdateOptions{})
		return err
	} else {
		return err
	}
}

// WaitForDaemonSetReady polls until every desired replica of the DaemonSet is
// ready.
func WaitForDaemonSetReady(ctx context.Context, c *cluster.Client, namespace, name string) error {
	return waiter.PollUntil(ctx, c.Config.DaemonSetReadyTimeout, c.Config.PollInterval,
		fmt.Sprintf("daemonset %s/%s to be ready", namespace, name),
		func(ctx context.Context) (bool, error) {
			ds, err := c.Typed.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return ds.Status.DesiredNumberScheduled > 0 &&
				ds.Status.NumberReady == ds.Status.DesiredNumberScheduled, nil
		})
}
