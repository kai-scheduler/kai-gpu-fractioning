// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package operator

import (
	"context"
	"testing"
)

// TestOperator is the single ordered driver for the suite. Cases run as nested
// subtests grouped by stage; order is deterministic (shared fixtures + fault
// injection make parallelism unsafe). assertSteady runs before the first stage
// and again between stages, so a case that fails to restore FX-STEADY is
// attributed to that case rather than the next one.
func TestOperator(t *testing.T) {
	ctx := context.Background()
	h.DumpDiagOnFailure(ctx, t)

	h.AssertSteady(ctx, t) // precondition + snapshot of the shipped CR spec

	// Validation — API/CEL validation & empty-selector (owns/borrows the CR).
	t.Run("Validation", func(t *testing.T) {
		t.Run("RejectsNonDefaultCRName", func(t *testing.T) { verifyRejectsNonDefaultCRName(ctx, t) })
		t.Run("RejectsMissingNodeSelector", func(t *testing.T) { verifyRejectsMissingNodeSelector(ctx, t) })
		t.Run("NodeSelectorImmutable", func(t *testing.T) { verifyNodeSelectorImmutable(ctx, t) })
		t.Run("ZeroMatchSelectorIsReady", func(t *testing.T) { caseZeroMatchSelectorIsReady(ctx, t) })
	})
	h.AssertSteady(ctx, t)

	// Deployment — rollout + read-only deployment/status/node assertions.
	t.Run("Deployment", func(t *testing.T) {
		t.Run("RollsOutToReady", func(t *testing.T) { caseRollsOutToReady(ctx, t) })
		t.Run("BothDaemonSetsReady", func(t *testing.T) { verifyBothDaemonSetsReady(ctx, t) })
		t.Run("NamingAndLabels", func(t *testing.T) { verifyNamingAndLabels(ctx, t) })
		t.Run("OwnerReferences", func(t *testing.T) { verifyOwnerReferences(ctx, t) })
		t.Run("SchedulingScope", func(t *testing.T) { verifySchedulingScope(ctx, t) })
		t.Run("FractiondPodSpec", func(t *testing.T) { verifyFractiondPodSpec(ctx, t) })
		t.Run("MpsdPodSpec", func(t *testing.T) { verifyMpsdPodSpec(ctx, t) })
		t.Run("ReconcileIdempotent", func(t *testing.T) { verifyReconcileIdempotent(ctx, t) })
		t.Run("ReadyTransitionTimeStable", func(t *testing.T) { verifyReadyTransitionTimeStable(ctx, t) })
		t.Run("NodeConditionReady", func(t *testing.T) { verifyNodeConditionReady(ctx, t) })
		t.Run("NodeConditionStable", func(t *testing.T) { verifyNodeConditionStable(ctx, t) })
		t.Run("MpsdReadinessSocket", func(t *testing.T) { verifyMpsdReadinessSocket(ctx, t) })
	})
	h.AssertSteady(ctx, t)

	// Recovery — fault injection & recovery (each restores FX-STEADY).
	t.Run("Recovery", func(t *testing.T) {
		t.Run("MpsdFaultAndRecovery", func(t *testing.T) { caseMpsdFaultAndRecovery(ctx, t) })
		t.Run("FractiondSocketFaultIsolation", func(t *testing.T) { caseFractiondSocketFaultIsolation(ctx, t) })
		t.Run("SupervisorBackoffNoRestart", func(t *testing.T) { caseSupervisorBackoffNoRestart(ctx, t) })
		t.Run("RetryBudgetExhaustedRestart", func(t *testing.T) { caseRetryBudgetExhaustedRestart(ctx, t) })
		t.Run("SpecDriftReverted", func(t *testing.T) { caseSpecDriftReverted(ctx, t) })
		t.Run("DeletedDaemonSetRecreated", func(t *testing.T) { caseDeletedDaemonSetRecreated(ctx, t) })
		t.Run("ControllerRestartTransparent", func(t *testing.T) { caseControllerRestartTransparent(ctx, t) })
	})
	h.AssertSteady(ctx, t)

	// Propagation — config / spec propagation to the DaemonSets (each reverts).
	t.Run("Propagation", func(t *testing.T) {
		t.Run("MpsdConfigToArgs", func(t *testing.T) { caseMpsdConfigToArgs(ctx, t) })
		t.Run("FractiondConfigToArgs", func(t *testing.T) { caseFractiondConfigToArgs(ctx, t) })
		t.Run("CustomAnnotationPrefix", func(t *testing.T) { caseCustomAnnotationPrefix(ctx, t) })
		t.Run("RollingUpdateConverges", func(t *testing.T) { caseRollingUpdateConverges(ctx, t) })
	})
	h.AssertSteady(ctx, t)

	// Teardown — CR deletion → FX-CLEAN, then restore FX-STEADY.
	t.Run("Teardown", func(t *testing.T) {
		t.Run("TeardownAndCleanup", func(t *testing.T) { caseTeardownAndCleanup(ctx, t) })
	})
	h.AssertSteady(ctx, t)
}
