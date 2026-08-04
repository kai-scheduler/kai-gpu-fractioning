// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package v1alpha1 contains API Schema definitions for the gpu-fractioning v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=gpu-fractioning.kai.scheduler
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: "gpu-fractioning.kai.scheduler", Version: "v1alpha1"}
	GroupVersion       = SchemeGroupVersion

	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&GpuFractioningConfig{},
		&GpuFractioningConfigList{},
	)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
}
