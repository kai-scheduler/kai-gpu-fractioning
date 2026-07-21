package daemonmgr

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
)

const (
	// DriverUpgradeStateLabel is the NVIDIA gpu-operator node label that tracks
	// GPU driver upgrade progress. It is a FROZEN external contract owned by the
	// gpu-operator and stays under nvidia.com.
	DriverUpgradeStateLabel = "nvidia.com/gpu-driver-upgrade-state"

	// DriverUpgradeStateDone is the gpu-operator's terminal (idle) value for
	// DriverUpgradeStateLabel. The label is absent on nodes the gpu-operator has
	// never upgraded and set to "upgrade-done" once an upgrade completes; any
	// other non-empty value (cordon-required, pod-deletion-required, …) means an
	// upgrade is actively in progress on that node.
	DriverUpgradeStateDone = "upgrade-done"

	// ConditionDriverUpgradeInProgress is the CR status condition set to True
	// while any GPU node targeted by the CR is mid driver-upgrade (its managed
	// daemons are drained). It is orthogonal to Ready and must be excluded from
	// the Ready aggregation.
	ConditionDriverUpgradeInProgress = "DriverUpgradeInProgress"
)

// DriverUpgradeActive reports whether a DriverUpgradeStateLabel value indicates
// an in-progress upgrade: any value set other than the terminal "upgrade-done".
func DriverUpgradeActive(labelValue string) bool {
	return labelValue != "" && labelValue != DriverUpgradeStateDone
}

// driverUpgradeNodeAffinity returns a required nodeAffinity that schedules the
// managed daemon only onto nodes that are NOT mid driver-upgrade: those with no
// DriverUpgradeStateLabel (the common case) or with the terminal "upgrade-done".
//
// This is how driver-upgrade coordination works with no new control API. For a
// DaemonSet, the DaemonSet controller actively deletes pods from nodes that no
// longer satisfy the pod's node affinity (unlike a Deployment, where
// IgnoredDuringExecution would leave the pod running). So when the gpu-operator
// sets the upgrade label on a node, the node stops matching, the DaemonSet
// controller drains sharingd/mpsd from it, mpsd exits and MPS graceful-quits
// before the driver is unloaded. When the label clears (or reaches upgrade-done)
// the node matches again and the daemons are rescheduled.
func driverUpgradeNodeAffinity() *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				// The two terms are ORed: schedule where the label is absent OR
				// equal to the terminal upgrade-done value.
				NodeSelectorTerms: []corev1.NodeSelectorTerm{
					{MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      DriverUpgradeStateLabel,
						Operator: corev1.NodeSelectorOpDoesNotExist,
					}}},
					{MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      DriverUpgradeStateLabel,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{DriverUpgradeStateDone},
					}}},
				},
			},
		},
	}
}

// DriverUpgradeCondition builds the CR's DriverUpgradeInProgress condition for
// the given aggregate state.
func DriverUpgradeCondition(active bool, generation int64) metav1.Condition {
	status := metav1.ConditionFalse
	reason := "NoDriverUpgrade"
	// Steady-state (nothing draining): the reason already says it, so leave the
	// message empty rather than restating it.
	message := ""
	if active {
		status = metav1.ConditionTrue
		reason = "DriverUpgrading"
		message = "a targeted GPU node is undergoing a driver upgrade; its managed daemons are drained"
	}
	return metav1.Condition{
		Type:               ConditionDriverUpgradeInProgress,
		Status:             status,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
		Reason:             reason,
		Message:            message,
	}
}
