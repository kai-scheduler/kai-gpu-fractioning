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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gpusharingv1alpha1 "github.com/kai-scheduler/gpu-sharing/api/v1alpha1"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/common/daemonmgr"
)

type fakeDependencyChecker struct {
	calls int
	err   error
}

func (f *fakeDependencyChecker) Check(_ context.Context, _ *gpusharingv1alpha1.GpuSharingConfig, ready metav1.Condition) (metav1.Condition, error) {
	f.calls++
	return ready, f.err
}

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

			// Reconciling registers the node-condition cleanup finalizer, so
			// deletion only completes after another reconcile releases it.
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil, true,
			)
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, typeNamespacedName, &gpusharingv1alpha1.GpuSharingConfig{}))
			}).Should(BeTrue())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil, true,
			)

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should check dependencies and requeue periodically after a healthy reconcile", func() {
			checker := &fakeDependencyChecker{}
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil, true,
			)
			controllerReconciler.DependencyChecker = checker

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(checker.calls).To(Equal(1))
			Expect(result.RequeueAfter).To(Equal(dependencyCheckInterval))
		})

		It("should retry sooner when dependency checking fails", func() {
			checker := &fakeDependencyChecker{err: errors.New("dependency check failed")}
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil, true,
			)
			controllerReconciler.DependencyChecker = checker

			result, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(checker.calls).To(Equal(1))
			Expect(result.RequeueAfter).To(Equal(requeueInterval))
		})

		It("should update observedGeneration on reconcile", func() {
			controllerReconciler := NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(10),
				"default", nil, true,
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
				"default", nil, true,
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

	Context("When deleting a resource", func() {
		// The CRD's CEL rule enforces a singleton CR named "default".
		const (
			resourceName = "default"
			namespace    = "gpu-sharing-delete-test"
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{Name: resourceName}
		selectorLabels := map[string]string{"gpu-sharing-test/target": "true"}

		var (
			reconciler   *GpuSharingConfigReconciler
			createdNodes []string
		)

		// gpuSharingConditionOf returns the node's gpu-sharing.nvidia.com/Ready
		// condition, or nil if the node does not carry it.
		gpuSharingConditionOf := func(nodeName string) *corev1.NodeCondition {
			node := &corev1.Node{}
			ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: nodeName}, node)).To(Succeed())
			for i := range node.Status.Conditions {
				if string(node.Status.Conditions[i].Type) == daemonmgr.NodeConditionType {
					return &node.Status.Conditions[i]
				}
			}
			return nil
		}

		createNode := func(name string, labels map[string]string, withCondition bool) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
			ExpectWithOffset(1, k8sClient.Create(ctx, node)).To(Succeed())
			createdNodes = append(createdNodes, name)
			if withCondition {
				node.Status.Conditions = append(node.Status.Conditions,
					corev1.NodeCondition{
						Type:   corev1.NodeConditionType(daemonmgr.NodeConditionType),
						Status: corev1.ConditionTrue,
						Reason: "AllDaemonsReady",
					},
					corev1.NodeCondition{
						Type:   corev1.NodeReady,
						Status: corev1.ConditionTrue,
						Reason: "KubeletReady",
					},
				)
				ExpectWithOffset(1, k8sClient.Status().Update(ctx, node)).To(Succeed())
			}
		}

		createConfig := func(name string, selector map[string]string) {
			resource := &gpusharingv1alpha1.GpuSharingConfig{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       gpusharingv1alpha1.GpuSharingConfigSpec{NodeSelector: selector},
			}
			ExpectWithOffset(1, k8sClient.Create(ctx, resource)).To(Succeed())
		}

		reconcileName := func(name string) error {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name},
			})
			return err
		}

		// deleteConfigAndFinalize deletes the CR and reconciles once so the
		// finalizer cleanup runs, then waits for the CR to be fully gone.
		deleteConfigAndFinalize := func(name string) {
			key := types.NamespacedName{Name: name}
			resource := &gpusharingv1alpha1.GpuSharingConfig{}
			ExpectWithOffset(1, k8sClient.Get(ctx, key, resource)).To(Succeed())
			ExpectWithOffset(1, k8sClient.Delete(ctx, resource)).To(Succeed())
			ExpectWithOffset(1, reconcileName(name)).To(Succeed())
			EventuallyWithOffset(1, func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, key, &gpusharingv1alpha1.GpuSharingConfig{}))
			}).Should(BeTrue())
		}

		BeforeEach(func() {
			// Namespaces cannot be deleted in envtest (they hang in
			// Terminating), so create it once and tolerate AlreadyExists.
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			if err := k8sClient.Create(ctx, ns); err != nil {
				Expect(apierrors.IsAlreadyExists(err)).To(BeTrue())
			}

			// Default images are required for the DaemonSets to pass API
			// validation (an empty image is rejected).
			defaultImages := map[string]gpusharingv1alpha1.ImageSpec{
				"sharingd": {Repository: "example.com/sharingd", Tag: "test"},
				"mpsd":     {Repository: "example.com/mpsd", Tag: "test"},
			}
			reconciler = NewGpuSharingConfigReconciler(
				k8sClient, k8sClient, k8sClient.Scheme(), record.NewFakeRecorder(20),
				namespace, defaultImages, true,
			)
			createdNodes = nil
		})

		AfterEach(func() {
			var configs gpusharingv1alpha1.GpuSharingConfigList
			Expect(k8sClient.List(ctx, &configs)).To(Succeed())
			for i := range configs.Items {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &configs.Items[i]))).To(Succeed())
				Expect(reconcileName(configs.Items[i].Name)).To(Succeed())
			}
			Eventually(func() int {
				Expect(k8sClient.List(ctx, &configs)).To(Succeed())
				return len(configs.Items)
			}).Should(BeZero())

			for _, name := range createdNodes {
				node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).To(Succeed())
			}

			// envtest has no garbage collector, so owned DaemonSets must be
			// removed by hand to keep tests independent.
			Expect(k8sClient.DeleteAllOf(ctx, &appsv1.DaemonSet{}, client.InNamespace(namespace))).To(Succeed())
		})

		It("removes node conditions and completes deletion", func() {
			createNode("g4-node-a", selectorLabels, true)
			createNode("g4-node-b", selectorLabels, true)
			// Outside the selector: cleanup is selector-scoped, so this node
			// must not be touched even though it carries the condition.
			createNode("g4-node-c", map[string]string{"gpu-sharing-test/other": "true"}, true)

			createConfig(resourceName, selectorLabels)
			Expect(reconcileName(resourceName)).To(Succeed())

			By("registering the cleanup finalizer on first reconcile")
			resource := &gpusharingv1alpha1.GpuSharingConfig{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, resource)).To(Succeed())
			Expect(resource.Finalizers).To(ContainElement(NodeConditionCleanupFinalizer))

			By("owning the DaemonSets so garbage collection still applies")
			var dsList appsv1.DaemonSetList
			Expect(k8sClient.List(ctx, &dsList, client.InNamespace(namespace))).To(Succeed())
			Expect(dsList.Items).NotTo(BeEmpty())
			for _, ds := range dsList.Items {
				Expect(ds.OwnerReferences).To(HaveLen(1))
				Expect(ds.OwnerReferences[0].UID).To(Equal(resource.UID))
			}

			By("cleaning targeted nodes when the CR is deleted")
			deleteConfigAndFinalize(resourceName)
			Expect(gpuSharingConditionOf("g4-node-a")).To(BeNil())
			Expect(gpuSharingConditionOf("g4-node-b")).To(BeNil())
			Expect(gpuSharingConditionOf("g4-node-c")).NotTo(BeNil())

			By("preserving unrelated node conditions")
			node := &corev1.Node{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "g4-node-a"}, node)).To(Succeed())
			Expect(node.Status.Conditions).To(HaveLen(1))
			Expect(node.Status.Conditions[0].Type).To(Equal(corev1.NodeReady))
		})

		It("leaves no stale condition on delete + recreate with a different nodeSelector", func() {
			oldSelector := map[string]string{"gpu-sharing-test/pool": "a"}
			newSelector := map[string]string{"gpu-sharing-test/pool": "b"}
			createNode("g9-node-a", oldSelector, true)
			createNode("g9-node-b", newSelector, false)

			createConfig(resourceName, oldSelector)
			Expect(reconcileName(resourceName)).To(Succeed())
			deleteConfigAndFinalize(resourceName)

			// Simulate the garbage collection envtest lacks before recreating.
			Expect(k8sClient.DeleteAllOf(ctx, &appsv1.DaemonSet{}, client.InNamespace(namespace))).To(Succeed())

			createConfig(resourceName, newSelector)
			Expect(reconcileName(resourceName)).To(Succeed())

			Expect(gpuSharingConditionOf("g9-node-a")).To(BeNil())
			Expect(gpuSharingConditionOf("g9-node-b")).To(BeNil())
		})

		It("completes deletion when no node carries the condition", func() {
			createNode("idem-node", selectorLabels, false)

			createConfig(resourceName, selectorLabels)
			Expect(reconcileName(resourceName)).To(Succeed())
			deleteConfigAndFinalize(resourceName)

			// A further reconcile after full deletion is a no-op.
			Expect(reconcileName(resourceName)).To(Succeed())
		})
	})
})
