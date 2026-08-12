// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package harness

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// absent is the placeholder the Cond*/NodeCond* formatters return for a
// condition that isn't present, so error messages read "want True, got <absent>"
// rather than an empty string.
const absent = "<absent>"

// CondStatus/CondReasonOf format a possibly-absent CR condition for error messages.
func CondStatus(ok bool, cond metav1.Condition) string {
	if !ok {
		return absent
	}
	return string(cond.Status)
}

func CondReasonOf(ok bool, cond metav1.Condition) string {
	if !ok {
		return absent
	}
	return cond.Reason
}

// NodeCondStatus/NodeCondReason format a possibly-absent node condition.
func NodeCondStatus(ok bool, cond corev1.NodeCondition) string {
	if !ok {
		return absent
	}
	return string(cond.Status)
}

func NodeCondReason(ok bool, cond corev1.NodeCondition) string {
	if !ok {
		return absent
	}
	return cond.Reason
}

// BackgroundCtx returns a context detached from a test deadline, for fault
// goroutines that must keep running across a case's poll waits.
func BackgroundCtx() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
