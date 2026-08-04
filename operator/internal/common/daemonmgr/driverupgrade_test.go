package daemonmgr

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDriverUpgradeActive(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		"upgrade-done":          false,
		"upgrade-required":      true,
		"cordon-required":       true,
		"pod-deletion-required": true,
		"upgrade-failed":        true,
	}
	for value, want := range cases {
		if got := DriverUpgradeActive(value); got != want {
			t.Errorf("DriverUpgradeActive(%q) = %v, want %v", value, got, want)
		}
	}
}

// The managed daemon must schedule only where the upgrade label is absent or
// upgrade-done, so the DaemonSet controller drains it from upgrading nodes.
func TestBaseDaemonSetDriverUpgradeAffinity(t *testing.T) {
	ds := BaseDaemonSet("fractiond", "ns")

	aff := ds.Spec.Template.Spec.Affinity
	if aff == nil || aff.NodeAffinity == nil || aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution == nil {
		t.Fatal("expected a required node affinity on the daemon pod template")
	}
	terms := aff.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms
	if len(terms) != 2 {
		t.Fatalf("expected 2 ORed node selector terms, got %d", len(terms))
	}
	if got := terms[0].MatchExpressions[0]; got.Key != DriverUpgradeStateLabel || got.Operator != corev1.NodeSelectorOpDoesNotExist {
		t.Errorf("term[0] = %+v, want %s DoesNotExist", got, DriverUpgradeStateLabel)
	}
	if got := terms[1].MatchExpressions[0]; got.Operator != corev1.NodeSelectorOpIn ||
		len(got.Values) != 1 || got.Values[0] != DriverUpgradeStateDone {
		t.Errorf("term[1] = %+v, want In[%s]", got, DriverUpgradeStateDone)
	}
}

func TestDriverUpgradeCondition(t *testing.T) {
	active := DriverUpgradeCondition(true, 7)
	if active.Type != ConditionDriverUpgradeInProgress || active.Status != metav1.ConditionTrue || active.ObservedGeneration != 7 {
		t.Errorf("active condition = %+v", active)
	}
	if inactive := DriverUpgradeCondition(false, 7); inactive.Status != metav1.ConditionFalse {
		t.Errorf("inactive status = %v, want False", inactive.Status)
	}
}

// A DriverUpgradeInProgress condition (which is False in steady state) must not
// be mistaken for a not-ready daemon component when aggregating Ready.
func TestAggregateReadyIgnoresDriverUpgrade(t *testing.T) {
	conds := []metav1.Condition{
		{Type: "fractiond", Status: metav1.ConditionTrue},
		{Type: "mpsd", Status: metav1.ConditionTrue},
		{Type: ConditionDriverUpgradeInProgress, Status: metav1.ConditionFalse},
	}
	if got := AggregateReadyCondition(conds, 1); got.Status != metav1.ConditionTrue {
		t.Errorf("Ready = %v, want True (DriverUpgradeInProgress must be excluded)", got.Status)
	}
}
