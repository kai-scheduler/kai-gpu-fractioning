/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/sharingmanager/components/mpsd"
	"github.com/run-ai/gpu-sharing-operator/operator/internal/sharingmanager/components/sharingd"
)

const (
	requeueInterval = 30 * time.Second
	podListPageSize = 500
)

// GpuSharingConfigReconciler reconciles a GpuSharingConfig object
type GpuSharingConfigReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// APIReader is the manager's uncached reader (direct API server reads).
	// Pods and Nodes are read through it — never through the cached Client —
	// so the operator never maintains cluster-scale Pod/Node informers. See
	// patchNodeConditions for the rationale.
	APIReader client.Reader

	// Namespace is the namespace where DaemonSets and their pods are created.
	// Since GpuSharingConfig is cluster-scoped, the DaemonSets use the
	// operator's own namespace (injected via POD_NAMESPACE).
	Namespace string

	// DefaultImages holds the Helm-injected default image for each daemon,
	// keyed by daemon name (e.g. "sharingd").
	DefaultImages map[string]v1alpha1.ImageSpec
}

// NewGpuSharingConfigReconciler creates a reconciler with safe defaults.
func NewGpuSharingConfigReconciler(
	c client.Client,
	apiReader client.Reader,
	scheme *runtime.Scheme,
	recorder record.EventRecorder,
	namespace string,
	defaultImages map[string]v1alpha1.ImageSpec,
) *GpuSharingConfigReconciler {
	return &GpuSharingConfigReconciler{
		Client:        c,
		APIReader:     apiReader,
		Scheme:        scheme,
		Recorder:      recorder,
		Namespace:     namespace,
		DefaultImages: defaultImages,
	}
}

// buildDaemons constructs the managed daemons from the current CRD spec,
// so each reconcile sees the latest configuration.
func buildDaemons(spec *v1alpha1.GpuSharingConfigSpec) []daemonmgr.ManagedDaemon {
	return []daemonmgr.ManagedDaemon{
		sharingd.NewSharingdDaemon(spec.SharingAgent, spec.MetricsAgent),
		mpsd.NewMpsdDaemon(spec.MpsDaemon),
	}
}

func (r *GpuSharingConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var config v1alpha1.GpuSharingConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		if errors.IsNotFound(err) {
			log.Info("GpuSharingConfig resource deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch GpuSharingConfig: %w", err)
	}

	log.Info("reconciling GpuSharingConfig", "generation", config.Generation)

	// Derive previous health from the persisted Ready condition so the
	// controller stays stateless across restarts and leader elections.
	wasUnhealthy := !isConditionTrue(config.Status.Conditions, daemonmgr.ConditionReady)

	// Reconcile each managed daemon and collect health + conditions.
	var needsRequeue bool
	daemons := buildDaemons(&config.Spec)

	opts := r.buildOptions(&config)
	for _, daemon := range daemons {

		health, reconcileErr := daemonmgr.ReconcileDaemon(ctx, r.Client, r.Scheme, &config, daemon, opts)
		if reconcileErr != nil {
			r.Recorder.Eventf(&config, corev1.EventTypeWarning, "ReconcileError", "%s: %v", daemon.Name(), reconcileErr)
			health = &daemonmgr.DaemonHealth{}
			needsRequeue = true
		}

		cond := daemonmgr.DaemonHealthToCondition(daemon.Name(), health, config.Generation, reconcileErr)
		daemonmgr.SetCondition(&config.Status, cond)

		if health.ReadyNodes < health.DesiredNodes {
			needsRequeue = true
		}
	}

	// Aggregate Ready condition.
	readyCond := daemonmgr.AggregateReadyCondition(config.Status.Conditions, config.Generation)
	daemonmgr.SetCondition(&config.Status, readyCond)

	// Patch gpu-sharing.nvidia.com/Ready on each node. We only list pods
	// when something is unhealthy or when transitioning back to healthy
	// (to flip nodes from False→True). In steady state this is skipped entirely.
	if needsRequeue || wasUnhealthy {
		nodesUnhealthy, err := r.patchNodeConditions(ctx, r.Namespace)
		if err != nil {
			// In case it that prev state was unhealthy, but we failed to patch nodes, we want to requeue
			log.Error(err, "failed to patch node conditions, will requeue")
			needsRequeue = true
		} else {
			needsRequeue = needsRequeue || nodesUnhealthy
		}
	}

	// Update CR status.
	config.Status.ObservedGeneration = config.Generation
	if err := r.Status().Update(ctx, &config); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to update status: %w", err)
	}

	if needsRequeue {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	return ctrl.Result{}, nil
}

// buildOptions resolves the BuildOptions shared by all daemons. DefaultImages
// contains the Helm-injected defaults; each daemon's BuildDaemonSet merges its
// own CRD overrides internally.
func (r *GpuSharingConfigReconciler) buildOptions(config *v1alpha1.GpuSharingConfig) daemonmgr.BuildOptions {
	return daemonmgr.BuildOptions{
		Namespace:     r.Namespace,
		NodeSelector:  config.Spec.NodeSelector,
		DefaultImages: r.DefaultImages,
	}
}

// patchNodeConditions lists all managed daemon pods (across all daemons) and
// patches the gpu-sharing.nvidia.com/Ready condition on each node.
// A node is marked Ready only when every daemon pod on it is ready.
//
// Pods are listed via the uncached API reader (r.APIReader), NOT the cached
// client, and are intentionally never cached. Rationale: this function only
// runs in the rare unhealthy/recovery path (see Reconcile's
// `needsRequeue || wasUnhealthy` guard), and reconciles are driven by DaemonSet
// status via Owns(DaemonSet) — not by pod events. Maintaining a cluster-scale
// Pod informer (O(nodes*daemons) objects resident, plus watch traffic) purely
// to serve a ~0.1%-of-the-time read is wasteful at large node counts, so we
// read on demand instead. Because the API reader supports server-side
// pagination, we page the list to bound peak memory during an incident.
//
// To avoid holding the full result in memory, nodes are patched inline as pods
// are streamed in pages. Only nodes with at least one unhealthy pod are tracked
// (the "unhealthy" set); in steady state this set is empty. If a node was
// already patched True and a later pod reveals it unhealthy, the condition is
// overwritten to False — correctness over patch count.
func (r *GpuSharingConfigReconciler) patchNodeConditions(ctx context.Context, namespace string) (bool, error) {
	log := logf.FromContext(ctx)

	managedByLabels := map[string]string{
		daemonmgr.LabelManagedBy: daemonmgr.ManagedByValue,
	}

	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels(managedByLabels),
		client.Limit(podListPageSize),
	}

	// Tracks nodes already marked unhealthy so we don't overwrite a False
	// with True when a later ready pod on the same node is encountered.
	// In steady state (all healthy) this set stays empty.
	unhealthyNodes := make(map[string]struct{})

	var podList corev1.PodList
	for {
		if err := r.APIReader.List(ctx, &podList, listOpts...); err != nil {
			return false, fmt.Errorf("listing pods for node condition patching: %w", err)
		}

		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Spec.NodeName == "" {
				continue
			}

			// Determine per-node health: if any daemon pod on this node is
			// not ready, the node is marked unhealthy with the specific
			// failure reason (e.g. CrashLoopBackOff, ImagePullBackOff).
			// Once a node is marked unhealthy, later healthy pods on the
			// same node are skipped to avoid overwriting the False condition.
			ready := isPodReady(pod)
			if !ready {
				unhealthyNodes[pod.Spec.NodeName] = struct{}{}
			} else if _, poisoned := unhealthyNodes[pod.Spec.NodeName]; poisoned {
				continue
			}

			reason, msg := nodeConditionArgs(ready, pod)
			if err := daemonmgr.PatchNodeCondition(ctx, r.APIReader, r.Client, pod.Spec.NodeName, ready, reason, msg); err != nil {
				log.Error(err, "failed to patch node condition", "node", pod.Spec.NodeName)
			}
		}

		if podList.Continue == "" {
			break
		}
		listOpts = []client.ListOption{
			client.InNamespace(namespace),
			client.MatchingLabels(managedByLabels),
			client.Limit(podListPageSize),
			client.Continue(podList.Continue),
		}
	}

	return len(unhealthyNodes) > 0, nil
}

// isPodReady returns true if the pod has condition Ready=True.
// There is a utility in k8s.io/component-helpers, but importing that module
// pulls in the entire k8s.io/kubernetes dependency tree, so we keep this
// trivial 4-line helper inline.
func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func nodeConditionArgs(ready bool, pod *corev1.Pod) (reason, message string) {
	if ready {
		return "AllDaemonsReady", "all gpu-sharing daemons are running"
	}
	reason = podFailureReason(pod)
	daemonName := pod.Labels[daemonmgr.LabelComponent]
	message = fmt.Sprintf("%s pod is not ready: %s", daemonName, reason)
	return reason, message
}

func podFailureReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			return cs.State.Waiting.Reason
		}
	}

	switch pod.Status.Phase {
	case corev1.PodPending:
		return "Pending"
	case corev1.PodFailed:
		return "Failed"
	default:
		return "NotReady"
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GpuSharingConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.GpuSharingConfig{}).
		Owns(&appsv1.DaemonSet{}).
		Named("gpusharingconfig").
		Complete(r)
}

// ReadImageFromEnv reads the SHARINGD_IMAGE_* env vars and returns an ImageSpec.
func ReadImageFromEnv(prefix string) v1alpha1.ImageSpec {
	return v1alpha1.ImageSpec{
		Repository:      os.Getenv(prefix + "_REPOSITORY"),
		Tag:             os.Getenv(prefix + "_TAG"),
		ImagePullPolicy: os.Getenv(prefix + "_PULL_POLICY"),
	}
}

func isConditionTrue(conditions []metav1.Condition, condType string) bool {
	for _, c := range conditions {
		if c.Type == condType {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}
