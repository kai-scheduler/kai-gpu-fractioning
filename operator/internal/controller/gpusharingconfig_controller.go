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
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/kai-scheduler/gpu-sharing/api/v1alpha1"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/common/daemonmgr"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/metrics"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/sharingmanager/components/mpsd"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/sharingmanager/components/sharingd"
	"github.com/kai-scheduler/gpu-sharing/pkg/env"
)

const (
	requeueInterval = 30 * time.Second
	podListPageSize = 500

	// NodeConditionCleanupFinalizer blocks GpuSharingConfig deletion until the
	// gpu-sharing.nvidia.com/Ready conditions the controller patched onto nodes
	// are removed. Without it, DaemonSets are garbage-collected via owner
	// references but nodes keep advertising a stale Ready condition.
	NodeConditionCleanupFinalizer = "gpu-sharing.nvidia.com/cleanup-node-conditions"
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

	// DefaultMpsdAuditLog is the Helm-injected default for the mpsd MPS memacct
	// audit log, forwarded to the mpsd container via env.
	DefaultMpsdAuditLog bool
}

// NewGpuSharingConfigReconciler creates a reconciler with safe defaults.
func NewGpuSharingConfigReconciler(
	c client.Client,
	apiReader client.Reader,
	scheme *runtime.Scheme,
	recorder record.EventRecorder,
	namespace string,
	defaultImages map[string]v1alpha1.ImageSpec,
	defaultMpsdAuditLog bool,
) *GpuSharingConfigReconciler {
	return &GpuSharingConfigReconciler{
		Client:              c,
		APIReader:           apiReader,
		Scheme:              scheme,
		Recorder:            recorder,
		Namespace:           namespace,
		DefaultImages:       defaultImages,
		DefaultMpsdAuditLog: defaultMpsdAuditLog,
	}
}

// buildDaemons constructs the managed daemons from the current CRD spec,
// so each reconcile sees the latest configuration.
func buildDaemons(spec *v1alpha1.GpuSharingConfigSpec, mpsdAuditLog bool) []daemonmgr.ManagedDaemon {
	return []daemonmgr.ManagedDaemon{
		sharingd.NewSharingdDaemon(spec.SharingAgent, spec.MetricsAgent),
		mpsd.NewMpsdDaemon(spec.MpsDaemon, mpsdAuditLog),
	}
}

func (r *GpuSharingConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var config v1alpha1.GpuSharingConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("GpuSharingConfig resource deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch GpuSharingConfig: %w", err)
	}

	if !config.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &config)
	}

	// Register the cleanup finalizer before any DaemonSets are created or node
	// conditions patched, so deletion can never race past the cleanup logic.
	if controllerutil.AddFinalizer(&config, NodeConditionCleanupFinalizer) {
		if err := r.Update(ctx, &config); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	log.Info("reconciling GpuSharingConfig", "generation", config.Generation)

	// Derive previous health from the persisted Ready condition so the
	// controller stays stateless across restarts and leader elections.
	wasUnhealthy := !isConditionTrue(config.Status.Conditions, daemonmgr.ConditionReady)

	// Reconcile each managed daemon and collect health + conditions.
	var needsRequeue bool
	daemons := buildDaemons(&config.Spec, r.DefaultMpsdAuditLog)

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
		metrics.SetDaemonHealth(daemon.Name(), health.ReadyNodes, health.DesiredNodes)

		if health.ReadyNodes < health.DesiredNodes {
			needsRequeue = true
		}
	}

	// Aggregate Ready condition.
	readyCond := daemonmgr.AggregateReadyCondition(config.Status.Conditions, config.Generation)
	daemonmgr.SetCondition(&config.Status, readyCond)

	// Reflect GPU driver-upgrade state on the CR. The nodeAffinity on the managed
	// DaemonSets already drains daemons from upgrading nodes (see
	// daemonmgr.driverUpgradeNodeAffinity); this surfaces that as a condition.
	// The reconcile is retriggered by the owned DaemonSets' status changing as
	// pods drain/return, so no Node watch is required.
	upgrading, upErr := r.evaluateDriverUpgrade(ctx, &config)
	if upErr != nil {
		log.Error(upErr, "failed to evaluate driver-upgrade state, will requeue")
		needsRequeue = true
	} else {
		daemonmgr.SetCondition(&config.Status, daemonmgr.DriverUpgradeCondition(upgrading, config.Generation))
	}

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

// evaluateDriverUpgrade reports whether any GPU node targeted by the CR is
// actively undergoing a driver upgrade, and clears the stale gpu-sharing Ready
// condition from those nodes. The filter is entirely server-side: nodes matching
// the CR's nodeSelector whose gpu-operator upgrade-state label is set to
// something other than the terminal "upgrade-done" — i.e. actively upgrading.
// Excluding "upgrade-done" at the server matters because that label persists on
// nodes after an upgrade, so a plain "has the label" list would return every
// targeted GPU node in a mature cluster; the surviving set is just the nodes
// mid-upgrade right now (bounded).
//
// Draining a node for an upgrade removes its daemon pods, and patchNodeConditions
// is pod-driven, so it never revisits the node and would leave a stale
// Ready=True there. We remove the condition here so the node stops advertising a
// readiness it no longer has; it is re-added (Ready=True) once the daemons
// reschedule after the upgrade. Reads go through the uncached APIReader for the
// same reason as patchNodeConditions: no cluster-scale Node informer.
func (r *GpuSharingConfigReconciler) evaluateDriverUpgrade(ctx context.Context, config *v1alpha1.GpuSharingConfig) (bool, error) {
	// Exists is required alongside NotIn: set-based NotIn also matches nodes that
	// lack the label, so on its own it would count non-upgrading nodes.
	exists, err := labels.NewRequirement(daemonmgr.DriverUpgradeStateLabel, selection.Exists, nil)
	if err != nil {
		return false, err
	}
	notDone, err := labels.NewRequirement(daemonmgr.DriverUpgradeStateLabel, selection.NotIn, []string{daemonmgr.DriverUpgradeStateDone})
	if err != nil {
		return false, err
	}
	selector := labels.SelectorFromSet(config.Spec.NodeSelector).Add(*exists, *notDone)

	var nodeList corev1.NodeList
	if err := r.APIReader.List(ctx, &nodeList, client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return false, fmt.Errorf("listing driver-upgrade nodes: %w", err)
	}

	var errs []error
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		// Only patch when there is actually a condition to remove, so a node stays
		// untouched across reconciles once cleared.
		if _, found := daemonmgr.FindNodeCondition(node); found {
			if err := daemonmgr.RemoveNodeCondition(ctx, r.Client, node.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return len(nodeList.Items) > 0, errors.Join(errs...)
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

// reconcileDelete handles a GpuSharingConfig with a non-zero deletionTimestamp:
// it removes the gpu-sharing.nvidia.com/Ready condition from the nodes the CR
// targets, then releases the finalizer so deletion can complete. Any cleanup
// error is returned with the finalizer still in place, so controller-runtime
// requeues with backoff until cleanup converges — deletion is never unblocked
// prematurely, but also never blocked forever by a transient failure.
func (r *GpuSharingConfigReconciler) reconcileDelete(ctx context.Context, config *v1alpha1.GpuSharingConfig) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(config, NodeConditionCleanupFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := daemonmgr.RemoveNodeConditions(ctx, r.APIReader, r.Client, config.Spec.NodeSelector); err != nil {
		return ctrl.Result{}, fmt.Errorf("cleaning up node conditions: %w", err)
	}

	controllerutil.RemoveFinalizer(config, NodeConditionCleanupFinalizer)
	if err := r.Update(ctx, config); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}

	log.Info("removed node conditions and finalizer, deletion can complete")
	return ctrl.Result{}, nil
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
	// All managed nodes seen this pass, so we can emit ready/degraded gauges.
	seenNodes := make(map[string]struct{})

	// Per-node patch failures are collected and joined so the caller requeues
	// and retries them; one bad node does not abort the rest of the pass.
	var errs []error

	var podList corev1.PodList
	for {
		if err := r.APIReader.List(ctx, &podList, listOpts...); err != nil {
			errs = append(errs, fmt.Errorf("listing pods for node condition patching: %w", err))
			return len(unhealthyNodes) > 0, errors.Join(errs...)
		}

		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Spec.NodeName == "" {
				continue
			}
			seenNodes[pod.Spec.NodeName] = struct{}{}

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
				errs = append(errs, err)
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

	metrics.SetNodeHealth(len(seenNodes)-len(unhealthyNodes), len(unhealthyNodes))
	return len(unhealthyNodes) > 0, errors.Join(errs...)
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
	// Init containers gate the regular containers, so a stuck init container
	// (e.g. Init:CrashLoopBackOff) is the primary failure signal. Waiting.Reason
	// is optional; an empty one must not leak into the node condition's Reason,
	// which the Kubernetes API requires to be a non-empty identifier.
	for _, statuses := range [][]corev1.ContainerStatus{pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses} {
		for _, cs := range statuses {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
				return cs.State.Waiting.Reason
			}
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

// ReadImageFromEnv reads the <PREFIX>_REPOSITORY, <PREFIX>_TAG, and
// <PREFIX>_PULL_POLICY env vars and returns an ImageSpec.
func ReadImageFromEnv(prefix string) v1alpha1.ImageSpec {
	return v1alpha1.ImageSpec{
		Repository:      env.String(prefix+"_REPOSITORY", ""),
		Tag:             env.String(prefix+"_TAG", ""),
		ImagePullPolicy: env.String(prefix+"_PULL_POLICY", ""),
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
