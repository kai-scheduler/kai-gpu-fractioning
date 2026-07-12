package daemonmgr

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func nodeWithConditions(name string, conditions ...corev1.NodeCondition) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: conditions},
	}
}

func gpuSharingCondition(status corev1.ConditionStatus) corev1.NodeCondition {
	return corev1.NodeCondition{
		Type:   corev1.NodeConditionType(NodeConditionType),
		Status: status,
		Reason: "AllDaemonsReady",
	}
}

func TestRemoveNodeCondition(t *testing.T) {
	readyCondition := corev1.NodeCondition{
		Type:   corev1.NodeReady,
		Status: corev1.ConditionTrue,
	}

	tests := []struct {
		name               string
		node               *corev1.Node
		expectedConditions []corev1.NodeConditionType
	}{
		{
			name:               "removes the gpu-sharing condition and preserves others",
			node:               nodeWithConditions("node-a", readyCondition, gpuSharingCondition(corev1.ConditionTrue)),
			expectedConditions: []corev1.NodeConditionType{corev1.NodeReady},
		},
		{
			name:               "no-op when the condition is absent",
			node:               nodeWithConditions("node-b", readyCondition),
			expectedConditions: []corev1.NodeConditionType{corev1.NodeReady},
		},
		{
			name:               "no-op when the node has no conditions",
			node:               nodeWithConditions("node-c"),
			expectedConditions: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithObjects(tt.node).Build()

			if err := RemoveNodeCondition(context.Background(), c, tt.node.Name); err != nil {
				t.Fatalf("RemoveNodeCondition() error = %v", err)
			}

			var got corev1.Node
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(tt.node), &got); err != nil {
				t.Fatalf("getting node: %v", err)
			}

			var gotTypes []corev1.NodeConditionType
			for _, cond := range got.Status.Conditions {
				gotTypes = append(gotTypes, cond.Type)
			}
			if len(gotTypes) != len(tt.expectedConditions) {
				t.Fatalf("conditions = %v, expected %v", gotTypes, tt.expectedConditions)
			}
			for i := range gotTypes {
				if gotTypes[i] != tt.expectedConditions[i] {
					t.Errorf("conditions = %v, expected %v", gotTypes, tt.expectedConditions)
				}
			}
		})
	}
}

func TestRemoveNodeCondition_MissingNode(t *testing.T) {
	c := fake.NewClientBuilder().Build()

	if err := RemoveNodeCondition(context.Background(), c, "no-such-node"); err != nil {
		t.Fatalf("RemoveNodeCondition() on missing node error = %v, expected nil", err)
	}
}
