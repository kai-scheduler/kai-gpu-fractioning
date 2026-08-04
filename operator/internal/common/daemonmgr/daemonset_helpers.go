// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package daemonmgr

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const daemonSetPrefix = "gpu-fractioning"

// BaseDaemonSet returns a DaemonSet skeleton with standard naming, labels,
// selector, and RollingUpdate strategy. The DaemonSet is named
// "gpu-fractioning-<component>" and labelled with the managed-by and component
// labels that the controller uses for pod listing and condition patching.
// Callers layer on their own container spec, volumes, etc.
func BaseDaemonSet(component, namespace string) *appsv1.DaemonSet {
	labels := map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelComponent: component,
	}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", daemonSetPrefix, component),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			UpdateStrategy: appsv1.DaemonSetUpdateStrategy{
				Type: appsv1.RollingUpdateDaemonSetStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					// Drain the daemon from nodes undergoing a GPU driver upgrade
					// so MPS graceful-quits before the driver is unloaded. See
					// driverUpgradeNodeAffinity.
					Affinity: driverUpgradeNodeAffinity(),
				},
			},
		},
	}
}

// DaemonResources returns the resource requests and limits for a managed system
// daemon container. CPU and memory requests are always set so the pods get a
// predictable QoS and the scheduler accounts for them; only a memory limit is
// applied (no CPU limit) so these latency-sensitive privileged daemons are never
// CPU-throttled while the node is still protected from a runaway memory leak.
func DaemonResources(cpuRequest, memRequest, memLimit string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuRequest),
			corev1.ResourceMemory: resource.MustParse(memRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(memLimit),
		},
	}
}

// PrivilegedSecurityContext returns a SecurityContext with privileged=true,
// required by both fractiond (NRI socket access) and mpsd (MPS daemon).
func PrivilegedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		Privileged: ptr.To(true),
	}
}

// SetOwnerReference sets the GpuFractioningConfig CR as the owner of the DaemonSet
// so that garbage collection cleans up DaemonSets when the CR is deleted.
func SetOwnerReference(ds *appsv1.DaemonSet, owner metav1.Object, scheme *runtime.Scheme) error {
	return controllerutil.SetControllerReference(owner, ds, scheme)
}
