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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kai-scheduler/gpu-sharing/api/v1alpha1"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/common/daemonmgr"
)

func TestGpuOperatorDependencyChecker(t *testing.T) {
	baseReady := metav1.Condition{
		Type:               daemonmgr.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: 7,
		Reason:             daemonmgr.ReasonAllComponentsReady,
		Message:            daemonmgr.MessageAllComponentsReady,
	}
	config := &v1alpha1.GpuSharingConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "default",
			Generation: 7,
		},
	}

	tests := []struct {
		name            string
		objects         []client.Object
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name: "ready ClusterPolicy with supported version leaves Ready unchanged",
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.7.0"}, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  daemonmgr.ReasonAllComponentsReady,
			expectedMessage: daemonmgr.MessageAllComponentsReady,
		},
		{
			name: "ready ClusterPolicy with unsupported version blocks Ready",
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.3.3"}, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorVersionUnsupported,
			expectedMessage: "v26.3.3",
		},
		{
			name: "ClusterPolicy Error condition blocks Ready with its message",
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.7.0"}, clusterPolicyStatus("notReady", "False", "True", "operand failed")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorNotReady,
			expectedMessage: "operand failed",
		},
		{
			name:            "missing ClusterPolicy blocks Ready",
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorNotReady,
			expectedMessage: "not found",
		},
		{
			name: "missing version label blocks Ready",
			objects: []client.Object{
				clusterPolicyObject(nil, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorVersionUnsupported,
			expectedMessage: clusterPolicyVersionLabel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := fake.NewClientBuilder().
				WithScheme(clusterPolicyScheme(t)).
				WithObjects(tt.objects...).
				Build()
			checker := NewGpuOperatorDependencyChecker(reader)

			got, err := checker.Check(context.Background(), config, baseReady)
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			if got.Status != tt.expectedStatus {
				t.Fatalf("Status = %s, expected %s", got.Status, tt.expectedStatus)
			}
			if got.Reason != tt.expectedReason {
				t.Fatalf("Reason = %q, expected %q", got.Reason, tt.expectedReason)
			}
			if !strings.Contains(got.Message, tt.expectedMessage) {
				t.Fatalf("Message = %q, expected to contain %q", got.Message, tt.expectedMessage)
			}
		})
	}
}

func TestNormalizeGPUOperatorVersion(t *testing.T) {
	tests := []struct {
		name       string
		version    string
		expected   string
		expectedOK bool
	}{
		{name: "full version", version: "v26.7.1", expected: "v26.7.1", expectedOK: true},
		{name: "without v prefix", version: "26.7.1", expected: "v26.7.1", expectedOK: true},
		{name: "major minor", version: "26.7", expected: "v26.7.0", expectedOK: true},
		{name: "major minor prerelease", version: "v26.7-rc.1", expected: "v26.7.0-rc.1", expectedOK: true},
		{name: "invalid", version: "not-a-version", expectedOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeGPUOperatorVersion(tt.version)
			if ok != tt.expectedOK {
				t.Fatalf("ok = %t, expected %t", ok, tt.expectedOK)
			}
			if got != tt.expected {
				t.Fatalf("version = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func clusterPolicyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(clusterPolicyGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(clusterPolicyGVK.GroupVersion().WithKind(clusterPolicyGVK.Kind+"List"), &unstructured.UnstructuredList{})
	return scheme
}

func clusterPolicyObject(labels map[string]string, status map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetGroupVersionKind(clusterPolicyGVK)
	obj.SetName("cluster-policy")
	obj.SetLabels(labels)
	if status != nil {
		obj.Object["status"] = status
	}
	return obj
}

func clusterPolicyStatus(state, readyStatus, errorStatus, message string) map[string]any {
	return map[string]any{
		"state": state,
		"conditions": []any{
			map[string]any{
				"type":    "Ready",
				"status":  readyStatus,
				"message": message,
			},
			map[string]any{
				"type":    "Error",
				"status":  errorStatus,
				"message": message,
			},
		},
	}
}
