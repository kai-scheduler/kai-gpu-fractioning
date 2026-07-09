// Package workload creates and tears down single-purpose test pods used to
// exercise gpu-sharing-plugin attribution. Separate from the plugin package,
// which manages the DaemonSet under test itself, not workloads that exercise
// it.
package workload

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/k8s/cluster"
	"github.com/run-ai/gpu-sharing-operator/test/e2e/waiter"
)

// DefaultImage is a minimal image with no GPU/CUDA dependency — attribution
// tests only need a live process holding a GPU allocation, not real compute.
const DefaultImage = "busybox:1.37"

// FractionalPod describes a single-container pod carrying the
// nvidia.com/gpu-memory.container.<name>.{limit,request} annotation that
// makes the gpu-sharing-plugin NRI plugin track it — see
// sharing-manager/metricsd/internal/plugin/adapter.go. Values/format mirror
// sharing-manager/metricsd/test/workloads/test-fractional-gpu-pods.yaml.
type FractionalPod struct {
	Namespace     string
	Name          string
	ContainerName string

	// GPUMemoryLimitMiB/RequestMiB are written verbatim into the derived
	// nvidia.com/gpu-memory.container.<name>.{limit,request} annotation
	// values (e.g. "2048"). Ignored when Annotations is non-nil.
	GPUMemoryLimitMiB   string
	GPUMemoryRequestMiB string

	// Annotations, when non-nil, replaces the derived fractional-GPU
	// annotations entirely. Use an empty (non-nil) map for a full-GPU pod
	// with no fractional annotation at all (TC-7), or a map containing a
	// deliberately malformed key to test that the plugin ignores it rather
	// than tracking or crashing on it (TC-8). Leave nil for the normal,
	// well-formed fractional case.
	Annotations map[string]string

	// NodeSelector defaults to the cluster's configured GPU node selector
	// (c.Config.GPUNodeSelector) when nil — pass an explicit selector (or a
	// specific node via kubernetes.io/hostname) to control co-location.
	NodeSelector map[string]string

	// Marker is a unique token embedded in the container's command line so the
	// nvmlmock helper can resolve this container's host PID by scanning
	// /proc/<pid>/cmdline inside the nvml-mock pod (which runs hostPID: true).
	// Defaults to DefaultMarker(Namespace, Name) when empty.
	Marker string
}

// DefaultMarker is the process-name token Apply embeds in a pod's command line
// when FractionalPod.Marker is empty. The nvmlmock helper greps /proc for it to
// resolve the container's host-namespace PID, which it then pins as a mock NVML
// GPU process so per-pod memory/utilization becomes deterministic.
func DefaultMarker(namespace, name string) string {
	return fmt.Sprintf("gpumock-%s-%s", namespace, name)
}

// Apply creates the namespace (if missing) and the pod, and waits for the
// pod to reach Running. The caller is responsible for calling Delete.
func Apply(ctx context.Context, c *cluster.Client, spec FractionalPod) (*corev1.Pod, error) {
	if err := ensureNamespace(ctx, c, spec.Namespace); err != nil {
		return nil, fmt.Errorf("ensure namespace %s: %w", spec.Namespace, err)
	}

	nodeSelector := spec.NodeSelector
	if nodeSelector == nil {
		sel, err := labels.ConvertSelectorToLabelsMap(c.Config.GPUNodeSelector)
		if err != nil {
			return nil, fmt.Errorf("parse GPU node selector %q as a node selector: %w", c.Config.GPUNodeSelector, err)
		}
		nodeSelector = sel
	}

	annotations := spec.Annotations
	if annotations == nil {
		// Key format must match sharingd's default annotation prefix
		// (configuration.DefaultAnnotationPrefix = "nvidia.com/gpu-memory.container."):
		//   nvidia.com/gpu-memory.container.<container>.{limit,request}
		// The value is parsed by sharingd as a k8s resource.Quantity, so it needs
		// a unit — the fields are MiB, so append the "Mi" suffix (a bare "2048"
		// would be read as 2048 bytes → 0 MB and rejected).
		annotations = map[string]string{
			fmt.Sprintf("nvidia.com/gpu-memory.container.%s.limit", spec.ContainerName):   spec.GPUMemoryLimitMiB + "Mi",
			fmt.Sprintf("nvidia.com/gpu-memory.container.%s.request", spec.ContainerName): spec.GPUMemoryRequestMiB + "Mi",
		}
	}

	marker := spec.Marker
	if marker == "" {
		marker = DefaultMarker(spec.Namespace, spec.Name)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   spec.Namespace,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			NodeSelector:  nodeSelector,
			Containers: []corev1.Container{
				{
					Name:  spec.ContainerName,
					Image: DefaultImage,
					// The marker rides in the shell's command line (visible in
					// /proc/<pid>/cmdline) so nvmlmock.HostPID can find it. No
					// nvidia.com/gpu resource request: with nvml-mock there is no
					// device plugin, and attribution keys off the fractional
					// annotation + cgroup + NVML UUID, not a scheduled GPU.
					//
					// "sleep & wait" (not a bare "sleep # marker") is deliberate:
					// busybox ash exec-optimizes a single simple command, replacing
					// the shell with `sleep 86400` and dropping the `#` comment — so
					// no process would carry the marker. Backgrounding sleep and
					// waiting keeps the shell alive with the marker in its argv. The
					// matched PID (the shell) is in the pod's cgroup, which is all
					// attribution needs.
					Command: []string{"sh", "-c", fmt.Sprintf("sleep 86400 & wait # %s", marker)},
				},
			},
		},
	}

	if err := c.Ctrl.Create(ctx, pod); err != nil {
		return nil, fmt.Errorf("create pod %s/%s: %w", spec.Namespace, spec.Name, err)
	}

	return WaitRunning(ctx, c, spec.Namespace, spec.Name, c.Config.PodReadyTimeout, c.Config.PollInterval)
}

// WaitRunning polls until the pod's phase is Running, returning the latest
// observed pod object.
func WaitRunning(ctx context.Context, c *cluster.Client, namespace, name string, timeout, interval time.Duration) (*corev1.Pod, error) {
	var pod corev1.Pod
	err := waiter.PollUntil(ctx, timeout, interval, fmt.Sprintf("pod %s/%s to be Running", namespace, name),
		func(ctx context.Context) (bool, error) {
			if err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: name}, &pod); err != nil {
				return false, err
			}
			return pod.Status.Phase == corev1.PodRunning, nil
		})
	if err != nil {
		return nil, err
	}
	return &pod, nil
}

// Delete force-removes the pod (grace period 0 — test workloads' entrypoints
// don't handle SIGTERM, so a graceful delete would sit "Terminating" for the
// full default grace period) and waits for it to actually disappear, so a
// subsequent Apply with the same name doesn't collide with a
// still-terminating pod. Safe to call on an already-deleted pod.
func Delete(ctx context.Context, c *cluster.Client, namespace, name string) error {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name}}
	if err := c.Ctrl.Delete(ctx, pod, ctrlclient.GracePeriodSeconds(0)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete pod %s/%s: %w", namespace, name, err)
	}

	return waiter.PollUntil(ctx, 30*time.Second, time.Second,
		fmt.Sprintf("pod %s/%s to be gone", namespace, name),
		func(ctx context.Context) (bool, error) {
			err := c.Ctrl.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: name}, &corev1.Pod{})
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		})
}

func ensureNamespace(ctx context.Context, c *cluster.Client, name string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := c.Ctrl.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
