package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func waitingStatus(reason string) corev1.ContainerStatus {
	return corev1.ContainerStatus{
		State: corev1.ContainerState{
			Waiting: &corev1.ContainerStateWaiting{Reason: reason},
		},
	}
}

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
