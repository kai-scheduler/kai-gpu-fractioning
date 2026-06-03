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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gpusharingv1alpha1 "github.com/run-ai/gpu-sharing-operator/operator/api/v1alpha1"
)

var _ = Describe("GpuSharingConfig Webhook", func() {
	var (
		validator GpuSharingConfigCustomValidator
		ctx       context.Context
	)

	BeforeEach(func() {
		validator = GpuSharingConfigCustomValidator{}
		ctx = context.Background()
	})

	Context("ValidateCreate", func() {
		It("should accept a resource named 'default'", func() {
			obj := &gpusharingv1alpha1.GpuSharingConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject a resource with any other name", func() {
			obj := &gpusharingv1alpha1.GpuSharingConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "my-config"},
			}
			_, err := validator.ValidateCreate(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`must be named "default"`))
		})
	})

	Context("ValidateUpdate", func() {
		It("should accept update on resource named 'default'", func() {
			obj := &gpusharingv1alpha1.GpuSharingConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}
			_, err := validator.ValidateUpdate(ctx, obj, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("ValidateDelete", func() {
		It("should accept deletion of any resource", func() {
			obj := &gpusharingv1alpha1.GpuSharingConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
			}
			_, err := validator.ValidateDelete(ctx, obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
