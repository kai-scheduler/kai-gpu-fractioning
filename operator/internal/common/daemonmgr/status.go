package daemonmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
)

const (
	// NodeConditionType is the condition type set on GPU nodes to indicate
	// whether all gpu-sharing daemons are healthy on that node.
	NodeConditionType = "gpu-sharing.nvidia.com/Ready"

	ConditionReady = "Ready"

	// Condition reasons.
	ReasonReconcileError     = "ReconcileError"
	ReasonNoTargetNodes      = "NoTargetNodes"
	ReasonAllPodsReady       = "AllPodsReady"
	ReasonPartiallyAvailable = "PartiallyAvailable"
	ReasonRolloutInProgress  = "RolloutInProgress"
	ReasonComponentNotReady  = "ComponentNotReady"
	ReasonComponentUnknown   = "ComponentUnknown"
	ReasonAllComponentsReady = "AllComponentsReady"
	ReasonNoComponents       = "NoComponents"

	// Condition messages.
	MessageNoTargetNodes      = "no target nodes found"
	MessageComponentUnknown   = "one or more components have unknown status"
	MessageAllComponentsReady = "all components are healthy"
	MessageNoComponents       = "no components have been evaluated"
)

// DaemonHealthToCondition maps a DaemonHealth to a metav1.Condition for the CR.
// If reconcileErr is non-nil, the condition is set to False with reason ReconcileError.
func DaemonHealthToCondition(daemonName string, health *DaemonHealth, generation int64, reconcileErr error) metav1.Condition {
	condType := fmt.Sprintf("%sReady", Capitalize(daemonName))
	now := metav1.NewTime(time.Now())

	if reconcileErr != nil {
		return metav1.Condition{
			Type:               condType,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             ReasonReconcileError,
			Message:            reconcileErr.Error(),
		}
	}

	if health.DesiredNodes == 0 {
		return metav1.Condition{
			Type:               condType,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             ReasonNoTargetNodes,
			Message:            MessageNoTargetNodes,
		}
	}

	if health.ReadyNodes == health.DesiredNodes {
		return metav1.Condition{
			Type:               condType,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             ReasonAllPodsReady,
			Message:            fmt.Sprintf("%d of %d pods ready", health.ReadyNodes, health.DesiredNodes),
		}
	}

	reason := ReasonPartiallyAvailable
	if health.ReadyNodes == 0 {
		reason = ReasonRolloutInProgress
	}

	return metav1.Condition{
		Type:               condType,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            fmt.Sprintf("%d of %d pods ready", health.ReadyNodes, health.DesiredNodes),
	}
}

// AggregateReadyCondition computes the aggregate Ready condition from all
// per-daemon conditions. Ready is True only when all are True.
func AggregateReadyCondition(conditions []metav1.Condition, generation int64) metav1.Condition {
	now := metav1.NewTime(time.Now())

	if len(conditions) == 0 {
		return metav1.Condition{
			Type:               ConditionReady,
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             ReasonNoComponents,
			Message:            MessageNoComponents,
		}
	}

	var falseComponents, unknownComponents []string
	for _, c := range conditions {
		if c.Type == ConditionReady {
			continue
		}
		switch c.Status {
		case metav1.ConditionFalse:
			falseComponents = append(falseComponents, string(c.Type))
		case metav1.ConditionUnknown:
			unknownComponents = append(unknownComponents, string(c.Type))
		}
	}

	// False takes precedence over Unknown: if any component is definitively
	// not ready we report that. Unknown components are only surfaced when
	// nothing is False.
	if len(falseComponents) > 0 {
		return metav1.Condition{
			Type:               ConditionReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             ReasonComponentNotReady,
			Message:            fmt.Sprintf("not ready: %s", strings.Join(falseComponents, ", ")),
		}
	}

	if len(unknownComponents) > 0 {
		return metav1.Condition{
			Type:               ConditionReady,
			Status:             metav1.ConditionUnknown,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             ReasonComponentUnknown,
			Message:            fmt.Sprintf("unknown: %s", strings.Join(unknownComponents, ", ")),
		}
	}

	return metav1.Condition{
		Type:               ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		LastTransitionTime: now,
		Reason:             ReasonAllComponentsReady,
		Message:            MessageAllComponentsReady,
	}
}

// SetCondition upserts a condition into the status. If a condition with the same
// type already exists with the same status, the lastTransitionTime is preserved.
func SetCondition(status *v1alpha1.GpuSharingConfigStatus, cond metav1.Condition) {
	for i, existing := range status.Conditions {
		if existing.Type == cond.Type {
			// Per Kubernetes API conventions, LastTransitionTime is only updated
			// when the status actually changes (e.g. True → False). This lets
			// consumers know how long a condition has been in its current state.
			if existing.Status == cond.Status {
				cond.LastTransitionTime = existing.LastTransitionTime
			}
			status.Conditions[i] = cond
			return
		}
	}
	status.Conditions = append(status.Conditions, cond)
}

// PatchNodeCondition sets or updates the gpu-sharing.nvidia.com/Ready condition
// on a node using a strategic merge patch.
//
// reader serves the current-node read and is deliberately the uncached API
// reader: node conditions are only touched in the rare unhealthy/recovery path,
// so caching every Node cluster-wide (a cluster-scoped informer) would be pure
// overhead. writer performs the status patch, which always goes to the API
// server regardless of caching.
func PatchNodeCondition(ctx context.Context, reader client.Reader, writer client.Client, nodeName string, ready bool, reason, message string) error {
	log := logf.FromContext(ctx).WithValues("node", nodeName)

	condStatus := corev1.ConditionFalse
	if ready {
		condStatus = corev1.ConditionTrue
	}

	now := metav1.NewTime(time.Now())
	transitionTime := now

	// Read the current node to preserve LastTransitionTime when the condition
	// status has not changed
	// Skip the patch entirely when status, reason, and message are all identical (no-op).
	node := &corev1.Node{}
	if err := reader.Get(ctx, types.NamespacedName{Name: nodeName}, node); err == nil {
		for _, existing := range node.Status.Conditions {
			if string(existing.Type) != NodeConditionType {
				continue
			}
			if existing.Status == condStatus {
				if existing.Reason == reason && existing.Message == message {
					return nil // nothing to update
				}
				// same status, therefore LastTransitionTime should be preserved.
				transitionTime = existing.LastTransitionTime
			}
			break // found our condition type
		}
	}

	condition := corev1.NodeCondition{
		Type:               corev1.NodeConditionType(NodeConditionType),
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		LastHeartbeatTime:  now,
		LastTransitionTime: transitionTime,
	}

	patch := map[string]any{
		"status": map[string]any{
			"conditions": []corev1.NodeCondition{condition},
		},
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling node condition patch: %w", err)
	}

	patchNode := &corev1.Node{}
	patchNode.Name = nodeName

	if err := writer.Status().Patch(ctx, patchNode, client.RawPatch(
		types.StrategicMergePatchType, patchBytes,
	)); err != nil {
		return fmt.Errorf("patching node %s condition: %w", nodeName, err)
	}

	log.V(1).Info("patched node condition", "condition", NodeConditionType, "status", condStatus, "reason", reason)
	return nil
}

// removeNodeConditionPatch deletes the gpu-sharing.nvidia.com/Ready entry from
// the merge-keyed conditions list. Node conditions use patchMergeKey "type",
// so a plain strategic merge patch can only upsert entries; deletion needs the
// $patch:delete directive, which typed NodeConditions cannot express. The
// payload depends only on the constant condition type, so it is built once.
var removeNodeConditionPatch = []byte(`{"status":{"conditions":[{"type":"` + NodeConditionType + `","$patch":"delete"}]}}`)

// RemoveNodeCondition deletes the gpu-sharing.nvidia.com/Ready condition from
// a node's status.
//
// The call is idempotent: deleting an absent condition is a server-side no-op,
// and a missing node is treated as success.
func RemoveNodeCondition(ctx context.Context, writer client.Client, nodeName string) error {
	log := logf.FromContext(ctx).WithValues("node", nodeName)

	patchNode := &corev1.Node{}
	patchNode.Name = nodeName

	if err := writer.Status().Patch(ctx, patchNode, client.RawPatch(
		types.StrategicMergePatchType, removeNodeConditionPatch,
	)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing condition from node %s: %w", nodeName, err)
	}

	log.V(1).Info("removed node condition", "condition", NodeConditionType)
	return nil
}

// Capitalize returns s with the first letter uppercased.
func Capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
