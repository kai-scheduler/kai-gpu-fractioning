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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GpuSharingConfigSpec defines the desired state of GpuSharingConfig.
type GpuSharingConfigSpec struct {
}

// GpuSharingConfigStatus defines the observed state of GpuSharingConfig.
type GpuSharingConfigStatus struct {
	// observedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'default'",message="GpuSharingConfig must be named 'default'"

// GpuSharingConfig is the Schema for the gpusharingconfigs API
type GpuSharingConfig struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of GpuSharingConfig
	// +required
	Spec GpuSharingConfigSpec `json:"spec"`

	// status defines the observed state of GpuSharingConfig
	// +optional
	Status GpuSharingConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GpuSharingConfigList contains a list of GpuSharingConfig
type GpuSharingConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GpuSharingConfig `json:"items"`
}
