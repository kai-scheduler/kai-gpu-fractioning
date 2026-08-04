// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kai-scheduler/kai-gpu-fractioning/api/v1alpha1"
	"github.com/kai-scheduler/kai-gpu-fractioning/operator/internal/common/daemonmgr"
)

func upgradeTestNode(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

// TestEvaluateDriverUpgradeClearsStaleReady verifies that an actively-upgrading
// node whose daemons were drained does not keep advertising a stale gpu-fractioning
// Ready condition (patchNodeConditions is pod-driven and would never revisit it).
func TestEvaluateDriverUpgradeClearsStaleReady(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	node := upgradeTestNode("gpu-x", map[string]string{
		"nvidia.com/gpu.present":          "true",
		daemonmgr.DriverUpgradeStateLabel: "pod-deletion-required",
	})
	node.Status.Conditions = []corev1.NodeCondition{{
		Type:   corev1.NodeConditionType(daemonmgr.NodeConditionType),
		Status: corev1.ConditionTrue,
	}}

	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&corev1.Node{}).
		WithObjects(node).
		Build()
	r := &GpuFractioningConfigReconciler{APIReader: fc, Client: fc}
	cfg := &v1alpha1.GpuFractioningConfig{
		Spec: v1alpha1.GpuFractioningConfigSpec{
			NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
		},
	}

	upgrading, err := r.evaluateDriverUpgrade(context.Background(), cfg)
	if err != nil {
		t.Fatalf("evaluateDriverUpgrade: %v", err)
	}
	if !upgrading {
		t.Fatal("expected upgrading = true")
	}

	var got corev1.Node
	if err := fc.Get(context.Background(), client.ObjectKey{Name: "gpu-x"}, &got); err != nil {
		t.Fatalf("get node: %v", err)
	}
	if _, found := daemonmgr.FindNodeCondition(&got); found {
		t.Errorf("stale gpu-fractioning Ready condition was not removed: %+v", got.Status.Conditions)
	}
}

func TestEvaluateDriverUpgrade(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	// gpu returns the labels of a GPU node (targeted by the default selector),
	// optionally carrying a driver-upgrade-state value.
	gpu := func(state string) map[string]string {
		l := map[string]string{"nvidia.com/gpu.present": "true"}
		if state != "" {
			l[daemonmgr.DriverUpgradeStateLabel] = state
		}
		return l
	}

	tests := []struct {
		name  string
		nodes []client.Object
		want  bool
	}{
		{
			name: "gpu node mid-upgrade is active",
			nodes: []client.Object{
				upgradeTestNode("gpu-a", gpu("pod-deletion-required")),
				upgradeTestNode("gpu-b", gpu("upgrade-done")),
				upgradeTestNode("gpu-c", gpu("")),
			},
			want: true,
		},
		{
			name: "only done or unlabelled is inactive",
			nodes: []client.Object{
				upgradeTestNode("gpu-b", gpu("upgrade-done")),
				upgradeTestNode("gpu-c", gpu("")),
			},
			want: false,
		},
		{
			name: "upgrading node not targeted by the selector is ignored",
			nodes: []client.Object{
				upgradeTestNode("cpu-a", map[string]string{daemonmgr.DriverUpgradeStateLabel: "pod-deletion-required"}),
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.nodes...).Build()
			r := &GpuFractioningConfigReconciler{APIReader: fc, Client: fc}
			cfg := &v1alpha1.GpuFractioningConfig{
				Spec: v1alpha1.GpuFractioningConfigSpec{
					NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
				},
			}

			got, err := r.evaluateDriverUpgrade(context.Background(), cfg)
			if err != nil {
				t.Fatalf("evaluateDriverUpgrade: %v", err)
			}
			if got != tc.want {
				t.Errorf("evaluateDriverUpgrade = %v, want %v", got, tc.want)
			}
		})
	}
}
