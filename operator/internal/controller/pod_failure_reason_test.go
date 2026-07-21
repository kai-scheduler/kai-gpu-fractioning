package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/run-ai/gpu-sharing-operator/operator/internal/common/daemonmgr"
)

func TestPodFailureReason(t *testing.T) {
	tests := []struct {
		name     string
		pod      corev1.Pod
		expected string
	}{
		{
			name: "regular container waiting reason",
			pod: corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{waitingStatus("CrashLoopBackOff")},
			}},
			expected: "CrashLoopBackOff",
		},
		{
			name: "stuck init container takes precedence",
			pod: corev1.Pod{Status: corev1.PodStatus{
				InitContainerStatuses: []corev1.ContainerStatus{waitingStatus("ImagePullBackOff")},
				ContainerStatuses:     []corev1.ContainerStatus{waitingStatus("PodInitializing")},
			}},
			expected: "ImagePullBackOff",
		},
		{
			name: "empty waiting reason falls through to phase",
			pod: corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{waitingStatus("")},
				Phase:             corev1.PodPending,
			}},
			expected: "Pending",
		},
		{
			name:     "failed phase",
			pod:      corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodFailed}},
			expected: "Failed",
		},
		{
			name:     "running but not ready",
			pod:      corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}},
			expected: "NotReady",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := podFailureReason(&tt.pod); got != tt.expected {
				t.Errorf("podFailureReason() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func waitingStatus(reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: reason},
		},
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
