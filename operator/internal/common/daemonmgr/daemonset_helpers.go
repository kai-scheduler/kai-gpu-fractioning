package daemonmgr

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const daemonSetPrefix = "gpu-sharing"

// BaseDaemonSet returns a DaemonSet skeleton with standard naming, labels,
// selector, and RollingUpdate strategy. The DaemonSet is named
// "gpu-sharing-<component>" and labelled with the managed-by and component
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
			},
		},
	}
}

// PrivilegedSecurityContext returns a SecurityContext with privileged=true,
// required by both sharingd (NRI socket access) and mpsd (MPS daemon).
func PrivilegedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		Privileged: ptr.To(true),
	}
}

// SetOwnerReference sets the GpuSharingConfig CR as the owner of the DaemonSet
// so that garbage collection cleans up DaemonSets when the CR is deleted.
func SetOwnerReference(ds *appsv1.DaemonSet, owner metav1.Object, scheme *runtime.Scheme) error {
	return controllerutil.SetControllerReference(owner, ds, scheme)
}
