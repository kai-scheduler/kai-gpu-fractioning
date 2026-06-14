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
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	gpusharingv1alpha1 "github.com/run-ai/gpu-sharing-operator/api/v1alpha1"
)

// GpuSharingConfigReconciler reconciles a GpuSharingConfig object
type GpuSharingConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *GpuSharingConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var config gpusharingv1alpha1.GpuSharingConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		if errors.IsNotFound(err) {
			log.Info("GpuSharingConfig resource deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to fetch GpuSharingConfig: %w", err)
	}

	log.Info("reconciling GpuSharingConfig", "generation", config.Generation)

	if err := r.updateStatus(ctx, &config); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *GpuSharingConfigReconciler) updateStatus(ctx context.Context, config *gpusharingv1alpha1.GpuSharingConfig) error {
	log := logf.FromContext(ctx)

	if config.Status.ObservedGeneration == config.Generation {
		return nil
	}

	config.Status.ObservedGeneration = config.Generation
	if err := r.Status().Update(ctx, config); err != nil {
		return fmt.Errorf("unable to update status: %w", err)
	}

	log.Info("updated observedGeneration", "generation", config.Generation)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GpuSharingConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gpusharingv1alpha1.GpuSharingConfig{}).
		Named("gpusharingconfig").
		Complete(r)
}
