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
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/kai-scheduler/gpu-sharing/api/v1alpha1"
	"github.com/kai-scheduler/gpu-sharing/operator/internal/common/daemonmgr"
)

const (
	clusterPolicyVersionLabel   = "app.kubernetes.io/version"
	minimumGPUOperatorVersion   = "v26.7.0"
	clusterPolicyReadyState     = "ready"
	clusterPolicyReadyCondition = "Ready"
	clusterPolicyErrorCondition = "Error"
)

var clusterPolicyGVK = schema.GroupVersionKind{
	Group:   "nvidia.com",
	Version: "v1",
	Kind:    "ClusterPolicy",
}

// DependencyChecker evaluates external GPU stack prerequisites needed by the
// gpu-sharing operator.
type DependencyChecker interface {
	Check(ctx context.Context, config *v1alpha1.GpuSharingConfig, ready metav1.Condition) (metav1.Condition, error)
}

// GpuOperatorDependencyChecker checks the NVIDIA GPU Operator ClusterPolicy.
type GpuOperatorDependencyChecker struct {
	reader client.Reader
}

func NewGpuOperatorDependencyChecker(reader client.Reader) GpuOperatorDependencyChecker {
	return GpuOperatorDependencyChecker{reader: reader}
}

func (c GpuOperatorDependencyChecker) Check(ctx context.Context, config *v1alpha1.GpuSharingConfig, ready metav1.Condition) (metav1.Condition, error) {
	clusterPolicy, err := c.clusterPolicy(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ready, err
		}
		return gpuOperatorReadyCondition(config.Generation, daemonmgr.ReasonGPUOperatorNotReady,
			fmt.Sprintf("unable to read NVIDIA GPU Operator ClusterPolicy: %v", err)), nil
	}
	if clusterPolicy == nil {
		return gpuOperatorReadyCondition(config.Generation, daemonmgr.ReasonGPUOperatorNotReady,
			"NVIDIA GPU Operator ClusterPolicy was not found"), nil
	}

	if msg := clusterPolicyReadinessFailureMessage(clusterPolicy); msg != "" {
		return gpuOperatorReadyCondition(config.Generation, daemonmgr.ReasonGPUOperatorNotReady, msg), nil
	}

	if msg := clusterPolicyVersionFailureMessage(clusterPolicy); msg != "" {
		return gpuOperatorReadyCondition(config.Generation, daemonmgr.ReasonGPUOperatorVersionUnsupported, msg), nil
	}

	return ready, nil
}

func (c GpuOperatorDependencyChecker) clusterPolicy(ctx context.Context) (*unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(clusterPolicyGVK.GroupVersion().WithKind(clusterPolicyGVK.Kind + "List"))

	if err := c.reader.List(ctx, list); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, err
	}

	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[0], nil
}

func clusterPolicyReadinessFailureMessage(clusterPolicy *unstructured.Unstructured) string {
	if cond, found := clusterPolicyCondition(clusterPolicy, clusterPolicyErrorCondition); found && conditionStatusIsTrue(cond.Status) {
		if cond.Message != "" {
			return cond.Message
		}
		return "NVIDIA GPU Operator ClusterPolicy reports Error=True"
	}

	if cond, found := clusterPolicyCondition(clusterPolicy, clusterPolicyReadyCondition); found && !conditionStatusIsTrue(cond.Status) {
		if cond.Message != "" {
			return cond.Message
		}
		return fmt.Sprintf("NVIDIA GPU Operator ClusterPolicy Ready condition is %s", conditionStatusText(cond.Status))
	}

	state, found, _ := unstructured.NestedString(clusterPolicy.Object, "status", "state")
	if found && !strings.EqualFold(strings.TrimSpace(state), clusterPolicyReadyState) {
		return fmt.Sprintf("NVIDIA GPU Operator ClusterPolicy state is %q, expected %q", state, clusterPolicyReadyState)
	}

	if !found {
		if _, hasReady := clusterPolicyCondition(clusterPolicy, clusterPolicyReadyCondition); !hasReady {
			return "NVIDIA GPU Operator ClusterPolicy status does not report readiness"
		}
	}

	return ""
}

func clusterPolicyVersionFailureMessage(clusterPolicy *unstructured.Unstructured) string {
	rawVersion := strings.TrimSpace(clusterPolicy.GetLabels()[clusterPolicyVersionLabel])
	if rawVersion == "" {
		return fmt.Sprintf("NVIDIA GPU Operator version label %q is missing; minimum supported version is %s",
			clusterPolicyVersionLabel, minimumGPUOperatorVersion)
	}

	version, ok := normalizeGPUOperatorVersion(rawVersion)
	if !ok {
		return fmt.Sprintf("NVIDIA GPU Operator version %q from label %q is invalid; minimum supported version is %s",
			rawVersion, clusterPolicyVersionLabel, minimumGPUOperatorVersion)
	}

	if semver.Compare(version, minimumGPUOperatorVersion) < 0 {
		return fmt.Sprintf("NVIDIA GPU Operator version %s is below minimum supported version %s",
			rawVersion, minimumGPUOperatorVersion)
	}

	return ""
}

func normalizeGPUOperatorVersion(version string) (string, bool) {
	normalized := strings.TrimSpace(version)
	if normalized == "" {
		return "", false
	}
	if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}

	if semver.IsValid(normalized) {
		return semver.Canonical(normalized), true
	}

	main, suffix := splitSemverSuffix(normalized)
	if strings.Count(main, ".") == 1 {
		withPatch := main + ".0" + suffix
		if semver.IsValid(withPatch) {
			return semver.Canonical(withPatch), true
		}
	}

	return "", false
}

func splitSemverSuffix(version string) (main, suffix string) {
	idx := strings.IndexAny(version, "-+")
	if idx == -1 {
		return version, ""
	}
	return version[:idx], version[idx:]
}

type clusterPolicyStatusCondition struct {
	Status  string
	Message string
}

func clusterPolicyCondition(clusterPolicy *unstructured.Unstructured, condType string) (clusterPolicyStatusCondition, bool) {
	conditions, found, err := unstructured.NestedSlice(clusterPolicy.Object, "status", "conditions")
	if err != nil || !found {
		return clusterPolicyStatusCondition{}, false
	}

	for _, condition := range conditions {
		conditionMap, ok := condition.(map[string]any)
		if !ok {
			continue
		}
		currentType, _, _ := unstructured.NestedString(conditionMap, "type")
		if currentType != condType {
			continue
		}
		status, _, _ := unstructured.NestedString(conditionMap, "status")
		message, _, _ := unstructured.NestedString(conditionMap, "message")
		return clusterPolicyStatusCondition{Status: status, Message: message}, true
	}

	return clusterPolicyStatusCondition{}, false
}

func conditionStatusIsTrue(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), string(metav1.ConditionTrue))
}

func conditionStatusText(status string) string {
	if strings.TrimSpace(status) == "" {
		return "Unknown"
	}
	return status
}

func gpuOperatorReadyCondition(generation int64, reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:               daemonmgr.ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.NewTime(time.Now()),
		Reason:             reason,
		Message:            message,
	}
}
