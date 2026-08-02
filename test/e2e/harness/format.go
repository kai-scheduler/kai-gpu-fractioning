//go:build e2e

package harness

import (
	"context"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// absent is the placeholder the Cond*/NodeCond* formatters return for a
// condition that isn't present, so error messages read "want True, got <absent>"
// rather than an empty string.
const absent = "<absent>"

// ExpectedDecimalMB converts a MiB value (e.g. "2048") to the decimal-MB string
// sharingd injects: bytes(=mib*1Mi) ÷ 1e6. 2048Mi → "2147", 4096Mi → "4294".
func ExpectedDecimalMB(t *testing.T, mib string) string {
	t.Helper()
	q, err := resource.ParseQuantity(mib + "Mi")
	if err != nil {
		t.Fatalf("parse memory quantity %qMi: %v", mib, err)
	}
	return strconv.FormatInt(q.Value()/1_000_000, 10)
}

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
