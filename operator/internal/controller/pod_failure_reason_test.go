package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func TestGpuDriverConditionMutator(t *testing.T) {
	tests := []struct {
		name           string
		ready          bool
		labels         map[string]string
		initialReason  string
		expectedReady  bool
		expectedReason string
	}{
		{
			name:           "ready condition is not changed even with missing driver label",
			ready:          true,
			labels:         nil,
			initialReason:  daemonmgr.ReasonAllDaemonsReady,
			expectedReady:  true,
			expectedReason: daemonmgr.ReasonAllDaemonsReady,
		},
		{
			name:           "not ready condition is enriched when driver label is missing",
			ready:          false,
			labels:         nil,
			initialReason:  "CrashLoopBackOff",
			expectedReady:  false,
			expectedReason: daemonmgr.ReasonGPUDriverVersionMissing,
		},
		{
			name:           "not ready condition is enriched when driver version is too old",
			ready:          false,
			labels:         map[string]string{gpuDriverMajorLabel: "614"},
			initialReason:  "CrashLoopBackOff",
			expectedReady:  false,
			expectedReason: daemonmgr.ReasonGPUDriverVersionUnsupported,
		},
		{
			name:           "not ready condition keeps pod failure when driver version is supported",
			ready:          false,
			labels:         map[string]string{gpuDriverMajorLabel: "615"},
			initialReason:  "CrashLoopBackOff",
			expectedReady:  false,
			expectedReason: "CrashLoopBackOff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: tt.labels}}
			gotReady, gotReason, _ := gpuDriverConditionMutator(node, tt.ready, tt.initialReason, "message")
			if gotReady != tt.expectedReady {
				t.Fatalf("ready = %t, expected %t", gotReady, tt.expectedReady)
			}
			if gotReason != tt.expectedReason {
				t.Fatalf("reason = %q, expected %q", gotReason, tt.expectedReason)
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
