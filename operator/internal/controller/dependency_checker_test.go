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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/kai-scheduler/gpu-sharing/api/v1alpha1"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/common/daemonmgr"
)

func TestGpuOperatorDependencyChecker(t *testing.T) {
	trueReady := metav1.Condition{
		Type:               daemonmgr.ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: 7,
		Reason:             daemonmgr.ReasonAllComponentsReady,
		Message:            daemonmgr.MessageAllComponentsReady,
	}
	falseReady := metav1.Condition{
		Type:               daemonmgr.ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: 7,
		Reason:             daemonmgr.ReasonComponentNotReady,
		Message:            "not ready: SharingdReady",
	}
	config := &v1alpha1.GpuSharingConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "default",
			Generation: 7,
		},
	}

	tests := []struct {
		name            string
		input           metav1.Condition
		objects         []client.Object
		expectedStatus  metav1.ConditionStatus
		expectedReason  string
		expectedMessage string
	}{
		{
			name:  "true Ready condition skips dependency check",
			input: trueReady,
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.3.3"}, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionTrue,
			expectedReason:  daemonmgr.ReasonAllComponentsReady,
			expectedMessage: daemonmgr.MessageAllComponentsReady,
		},
		{
			name:  "ready ClusterPolicy with supported version leaves false Ready unchanged",
			input: falseReady,
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.7.1"}, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonComponentNotReady,
			expectedMessage: "not ready: SharingdReady",
		},
		{
			name:  "ready ClusterPolicy with unsupported version blocks Ready",
			input: falseReady,
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.3.3"}, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorVersionUnsupported,
			expectedMessage: "v26.3.3",
		},
		{
			// v26.7.0 is the immediately preceding patch release; the supported
			// floor is v26.7.1, so it must be rejected.
			name:  "ready ClusterPolicy one patch below the minimum blocks Ready",
			input: falseReady,
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.7.0"}, clusterPolicyStatus("ready", "True", "False", "")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorVersionUnsupported,
			expectedMessage: "v26.7.0",
		},
		{
			name:  "ClusterPolicy Error condition blocks Ready with its message",
			input: falseReady,
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.7.1"}, clusterPolicyStatus("notReady", "False", "True", "operand failed")),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorNotReady,
			expectedMessage: "operand failed",
		},
		{
			name:            "missing ClusterPolicy blocks Ready",
			input:           falseReady,
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorNotReady,
			expectedMessage: "no gpu-operator ClusterServiceVersion found",
		},
		{
			name:  "supported OpenShift ClusterServiceVersion leaves false Ready unchanged",
			input: falseReady,
			objects: []client.Object{
				clusterServiceVersionObject("gpu-operator-certified.v26.7.1", "26.7.1"),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonComponentNotReady,
			expectedMessage: "not ready: SharingdReady",
		},
		{
			name:  "unsupported OpenShift ClusterServiceVersion blocks Ready",
			input: falseReady,
			objects: []client.Object{
				clusterServiceVersionObject("gpu-operator-certified.v26.3.3", "26.3.3"),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorVersionUnsupported,
			expectedMessage: "26.3.3",
		},
		{
			name:  "ClusterPolicy takes precedence over OpenShift ClusterServiceVersion",
			input: falseReady,
			objects: []client.Object{
				clusterPolicyObject(map[string]string{clusterPolicyVersionLabel: "v26.3.3"}, clusterPolicyStatus("ready", "True", "False", "")),
				clusterServiceVersionObject("gpu-operator-certified.v26.7.1", "26.7.1"),
			},
			expectedStatus:  metav1.ConditionFalse,
			expectedReason:  daemonmgr.ReasonGPUOperatorVersionUnsupported,
			expectedMessage: "v26.3.3",
		},
		{
			name:  "missing version label blocks Ready",
			input: falseReady,
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

			got, err := checker.Check(context.Background(), config, tt.input)
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

func TestGpuDriverDependencyChecker(t *testing.T) {
	tests := []struct {
		name           string
		status         corev1.ConditionStatus
		labels         map[string]string
		missingNode    bool
		initialReason  string
		expectedStatus corev1.ConditionStatus
		expectedReason string
	}{
		{
			name:           "ready condition is not changed even with missing driver label",
			status:         corev1.ConditionTrue,
			labels:         nil,
			initialReason:  daemonmgr.ReasonAllDaemonsReady,
			expectedStatus: corev1.ConditionTrue,
			expectedReason: daemonmgr.ReasonAllDaemonsReady,
		},
		{
			name:           "not ready condition is enriched when driver label is missing",
			status:         corev1.ConditionFalse,
			labels:         nil,
			initialReason:  "CrashLoopBackOff",
			expectedStatus: corev1.ConditionFalse,
			expectedReason: daemonmgr.ReasonGPUDriverVersionMissing,
		},
		{
			name:           "not ready condition is enriched when driver version is too old",
			status:         corev1.ConditionFalse,
			labels:         map[string]string{gpuDriverMajorLabel: "614"},
			initialReason:  "CrashLoopBackOff",
			expectedStatus: corev1.ConditionFalse,
			expectedReason: daemonmgr.ReasonGPUDriverVersionUnsupported,
		},
		{
			name:           "not ready condition keeps pod failure when driver version is supported",
			status:         corev1.ConditionFalse,
			labels:         map[string]string{gpuDriverMajorLabel: "615"},
			initialReason:  "CrashLoopBackOff",
			expectedStatus: corev1.ConditionFalse,
			expectedReason: "CrashLoopBackOff",
		},
		{
			name:           "not ready condition keeps pod failure when node cannot be read",
			status:         corev1.ConditionFalse,
			missingNode:    true,
			initialReason:  "CrashLoopBackOff",
			expectedStatus: corev1.ConditionFalse,
			expectedReason: "CrashLoopBackOff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []client.Object{}
			if !tt.missingNode {
				objects = append(objects, &corev1.Node{ObjectMeta: metav1.ObjectMeta{
					Name:   "node-a",
					Labels: tt.labels,
				}})
			}
			checker := NewGpuDriverDependencyChecker(fake.NewClientBuilder().WithObjects(objects...).Build())

			got, err := checker.Check(context.Background(), "node-a", corev1.NodeCondition{
				Type:    corev1.NodeConditionType(daemonmgr.NodeConditionType),
				Status:  tt.status,
				Reason:  tt.initialReason,
				Message: "message",
			})
			if err != nil {
				t.Fatalf("Check returned error: %v", err)
			}
			if got.Status != tt.expectedStatus {
				t.Fatalf("status = %s, expected %s", got.Status, tt.expectedStatus)
			}
			if got.Reason != tt.expectedReason {
				t.Fatalf("reason = %q, expected %q", got.Reason, tt.expectedReason)
			}
		})
	}
}

func TestGpuDriverFailureReason(t *testing.T) {
	tests := []struct {
		name           string
		labels         map[string]string
		expectedFound  bool
		expectedReason string
	}{
		{
			name:           "missing label",
			expectedFound:  true,
			expectedReason: daemonmgr.ReasonGPUDriverVersionMissing,
		},
		{
			name:           "invalid label",
			labels:         map[string]string{gpuDriverMajorLabel: "615.34"},
			expectedFound:  true,
			expectedReason: daemonmgr.ReasonGPUDriverVersionInvalid,
		},
		{
			name:           "too old",
			labels:         map[string]string{gpuDriverMajorLabel: "614"},
			expectedFound:  true,
			expectedReason: daemonmgr.ReasonGPUDriverVersionUnsupported,
		},
		{
			name:          "minimum supported",
			labels:        map[string]string{gpuDriverMajorLabel: "615"},
			expectedFound: false,
		},
		{
			name:          "newer supported",
			labels:        map[string]string{gpuDriverMajorLabel: "620"},
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			gotReason, _, gotFound := gpuDriverFailureReason(node)
			if gotFound != tt.expectedFound {
				t.Fatalf("found = %t, expected %t", gotFound, tt.expectedFound)
			}
			if gotReason != tt.expectedReason {
				t.Fatalf("reason = %q, expected %q", gotReason, tt.expectedReason)
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
	scheme.AddKnownTypeWithName(clusterServiceVersionGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(clusterServiceVersionGVK.GroupVersion().WithKind(clusterServiceVersionGVK.Kind+"List"), &unstructured.UnstructuredList{})
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

func clusterServiceVersionObject(name, version string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"version": version,
		},
	}}
	obj.SetGroupVersionKind(clusterServiceVersionGVK)
	obj.SetName(name)
	obj.SetNamespace("openshift-operators")
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
