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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gpusharingv1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
)

var _ = Describe("GpuSharingConfig Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "default"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name: resourceName,
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind GpuSharingConfig")
			resource := &gpusharingv1alpha1.GpuSharingConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: resourceName,
				},
				Spec: gpusharingv1alpha1.GpuSharingConfigSpec{
					NodeSelector: map[string]string{"nvidia.com/gpu.present": "true"},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		})

		AfterEach(func() {
			resource := &gpusharingv1alpha1.GpuSharingConfig{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance GpuSharingConfig")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil,
			)

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should update observedGeneration on reconcile", func() {
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil,
			)

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			var updated gpusharingv1alpha1.GpuSharingConfig
			Expect(k8sClient.Get(ctx, typeNamespacedName, &updated)).To(Succeed())
			Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
		})

		It("should handle not-found resources gracefully", func() {
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil,
			)

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "nonexistent",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
