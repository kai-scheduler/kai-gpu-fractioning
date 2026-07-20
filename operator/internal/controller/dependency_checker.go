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

	v1alpha1 "github.com/kai-scheduler/gpu-sharing/api/v1alpha1"
)

// DependencyChecker evaluates external GPU stack prerequisites needed by the
// gpu-sharing operator.
type DependencyChecker interface {
	Check(ctx context.Context, config *v1alpha1.GpuSharingConfig) error
}

// NoopDependencyChecker is the placeholder implementation used until concrete
// GPU Operator and driver version checks are added.
type NoopDependencyChecker struct{}

func (NoopDependencyChecker) Check(_ context.Context, _ *v1alpha1.GpuSharingConfig) error {
	return nil
}
