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
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	gpusharingv1alpha1 "github.com/run-ai/gpu-sharing-operator/operator/api/v1alpha1"
)

// singletonName is the only allowed name for GpuSharingConfig resources,
// enforcing a single cluster-wide instance.
const singletonName = "default"

var log = logf.Log.WithName("gpusharingconfig-webhook")

func SetupGpuSharingConfigWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &gpusharingv1alpha1.GpuSharingConfig{}).
		WithValidator(&GpuSharingConfigCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gpu-sharing-run-ai-v1alpha1-gpusharingconfig,mutating=false,failurePolicy=fail,sideEffects=None,groups=gpu-sharing.run.ai,resources=gpusharingconfigs,verbs=create;update,versions=v1alpha1,name=vgpusharingconfig-v1alpha1.kb.io,admissionReviewVersions=v1
// +kubebuilder:object:generate=false
type GpuSharingConfigCustomValidator struct{}

func (v *GpuSharingConfigCustomValidator) ValidateCreate(_ context.Context, obj *gpusharingv1alpha1.GpuSharingConfig) (admission.Warnings, error) {
	log.Info("validating creation", "name", obj.GetName())
	return nil, validateSingletonName(obj)
}

func (v *GpuSharingConfigCustomValidator) ValidateUpdate(_ context.Context, _, newObj *gpusharingv1alpha1.GpuSharingConfig) (admission.Warnings, error) {
	log.Info("validating update", "name", newObj.GetName())
	return nil, validateSingletonName(newObj)
}

// ValidateDelete is a no-op required by the CustomValidator interface.
func (v *GpuSharingConfigCustomValidator) ValidateDelete(_ context.Context, _ *gpusharingv1alpha1.GpuSharingConfig) (admission.Warnings, error) {
	return nil, nil
}

// validateSingletonName rejects any GpuSharingConfig whose name is not "default".
// This ensures only one instance can exist cluster-wide, since Kubernetes
// prevents two cluster-scoped resources with the same name.
func validateSingletonName(obj *gpusharingv1alpha1.GpuSharingConfig) error {
	if obj.GetName() != singletonName {
		return fmt.Errorf("GpuSharingConfig must be named %q, got %q", singletonName, obj.GetName())
	}
	return nil
}
