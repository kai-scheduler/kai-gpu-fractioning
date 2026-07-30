//go:build e2e

package sharingd

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/kai-scheduler/gpu-sharing/test/e2e/harness"
	"github.com/kai-scheduler/gpu-sharing/test/e2e/k8s/daemonset"
	"github.com/kai-scheduler/gpu-sharing/test/e2e/waiter"
	"github.com/kai-scheduler/gpu-sharing/test/e2e/workload"
)

// InjectsMemoryEnv — sharingd injects the GPU-memory env vars and MPS
// pipe dir into an annotated container (request/limit converted to decimal MB).
func caseInjectsMemoryEnv(ctx context.Context, t *testing.T) {
	pod := h.ApplyRunningWorkload(ctx, t, "d1-inject", h.DefaultWorkloadAnnotations())
	env := h.GetPodEnv(ctx, t, pod, harness.WorkloadContainer)

	wantReq := harness.ExpectedDecimalMB(t, harness.MemRequestMiB)
	wantLim := harness.ExpectedDecimalMB(t, harness.MemLimitMiB)
	if env[harness.EnvGPUMemRequests] != wantReq {
		t.Errorf("%s = %q, want %q", harness.EnvGPUMemRequests, env[harness.EnvGPUMemRequests], wantReq)
	}
	if env[harness.EnvGPUMemLimits] != wantLim {
		t.Errorf("%s = %q, want %q", harness.EnvGPUMemLimits, env[harness.EnvGPUMemLimits], wantLim)
	}
	if env[harness.EnvMPSPipeDir] != harness.MPSPipeDir {
		t.Errorf("%s = %q, want %q", harness.EnvMPSPipeDir, env[harness.EnvMPSPipeDir], harness.MPSPipeDir)
	}
}

// MountsMPSPipeDir — the MPS pipe directory is bind-mounted into an
// annotated container.
func caseMountsMPSPipeDir(ctx context.Context, t *testing.T) {
	pod := h.ApplyRunningWorkload(ctx, t, "d2-mount", h.DefaultWorkloadAnnotations())
	out, err := h.Exec(ctx, pod, harness.WorkloadContainer, "sh", "-c", fmt.Sprintf("test -d %s && echo yes || echo no", harness.MPSPipeDir))
	if err != nil {
		t.Fatalf("exec dir check: %v", err)
	}
	if out != "yes" {
		t.Errorf("MPS pipe dir %s not mounted into container", harness.MPSPipeDir)
	}
}

// SkipsUnannotatedContainer — a container with no gpu-sharing
// annotation is left untouched (no injection).
func caseSkipsUnannotatedContainer(ctx context.Context, t *testing.T) {
	pod := h.ApplyRunningWorkload(ctx, t, "d3-plain", map[string]string{}) // empty ⇒ no annotations
	env := h.GetPodEnv(ctx, t, pod, harness.WorkloadContainer)
	for _, k := range []string{harness.EnvGPUMemRequests, harness.EnvGPUMemLimits, harness.EnvMPSPipeDir} {
		if _, ok := env[k]; ok {
			t.Errorf("unannotated container has injected env %s=%q", k, env[k])
		}
	}
}

// RequestOrLimitOnly — request-only and limit-only annotations both end
// up with request==limit (ApplyDefaults), so the cap is enforced and the request
// is populated.
func caseRequestOrLimitOnly(ctx context.Context, t *testing.T) {
	t.Run("request-only", func(t *testing.T) {
		want := harness.ExpectedDecimalMB(t, harness.MemRequestMiB)
		pod := h.ApplyRunningWorkload(ctx, t, "d4-req", map[string]string{
			h.AnnKey(harness.WorkloadContainer, "request"): harness.MemRequestMiB + "Mi",
		})
		env := h.GetPodEnv(ctx, t, pod, harness.WorkloadContainer)
		if env[harness.EnvGPUMemRequests] != want || env[harness.EnvGPUMemLimits] != want {
			t.Errorf("request-only: req=%q lim=%q, want both %q", env[harness.EnvGPUMemRequests], env[harness.EnvGPUMemLimits], want)
		}
	})
	t.Run("limit-only", func(t *testing.T) {
		want := harness.ExpectedDecimalMB(t, harness.MemLimitMiB)
		pod := h.ApplyRunningWorkload(ctx, t, "d4-lim", map[string]string{
			h.AnnKey(harness.WorkloadContainer, "limit"): harness.MemLimitMiB + "Mi",
		})
		env := h.GetPodEnv(ctx, t, pod, harness.WorkloadContainer)
		if env[harness.EnvGPUMemRequests] != want || env[harness.EnvGPUMemLimits] != want {
			t.Errorf("limit-only: req=%q lim=%q, want both %q", env[harness.EnvGPUMemRequests], env[harness.EnvGPUMemLimits], want)
		}
	})
}

// FailClosedBlocks — fail-closed (default): a malformed annotation
// makes sharingd block container creation, so the pod never reaches Running.
func caseFailClosedBlocks(ctx context.Context, t *testing.T) {
	name := "d5-failclosed"
	createBlockedWorkload(ctx, t, name, map[string]string{
		h.AnnKey(harness.WorkloadContainer, "request"): "not-a-quantity",
	})
	if reason := waitContainerBlocked(ctx, t, name); reason == "" {
		t.Errorf("pod %s: expected a container create error (fail-closed), none observed", name)
	} else {
		t.Logf("pod %s blocked as expected: %s", name, reason)
	}
}

// FailOpenSkips — fail-open: with sharingAgent.failOpen=true a malformed
// annotation is skipped, so the container runs but carries no injected env.
func caseFailOpenSkips(ctx context.Context, t *testing.T) {
	dsName := harness.DSName(harness.ComponentSharingd)
	// Capture the sharingd DaemonSet generation before the patch. WaitRolledOut
	// alone can match the already-converged pre-change DaemonSet in the window
	// before the operator reconciles the failOpen change, so we'd apply the
	// workload against the old (fail-closed) sharingd and flake. WaitRolledOutAfter
	// waits for the *new* generation to roll out.
	beforeGen := h.GetDaemonSet(ctx, t, harness.ComponentSharingd).Generation

	patch := []byte(`{"spec":{"sharingAgent":{"failOpen":true}}}`)
	revert := []byte(`{"spec":{"sharingAgent":{"failOpen":null}}}`)
	h.WithCRConfig(ctx, t, patch, revert, func() {
		// The failOpen change re-rolls the sharingd DaemonSet; wait for the new
		// generation to finish rolling out.
		if _, err := daemonset.WaitRolledOutAfter(ctx, h.Client(), h.NS(), dsName, beforeGen, h.RolloutTimeout(), h.PollInterval()); err != nil {
			t.Fatalf("sharingd rollout after failOpen: %v", err)
		}
		pod := h.ApplyRunningWorkload(ctx, t, "d6-failopen", map[string]string{
			h.AnnKey(harness.WorkloadContainer, "request"): "not-a-quantity",
		})
		env := h.GetPodEnv(ctx, t, pod, harness.WorkloadContainer)
		for _, k := range []string{harness.EnvGPUMemRequests, harness.EnvGPUMemLimits, harness.EnvMPSPipeDir} {
			if _, ok := env[k]; ok {
				t.Errorf("fail-open with malformed annotation still injected %s=%q", k, env[k])
			}
		}
	})
}

// ── FailClosedBlocks helpers ─────────────────────────────────────────────────

// createBlockedWorkload creates an annotated pod WITHOUT waiting for Running (it
// is expected to be blocked at container creation). Registers cleanup.
func createBlockedWorkload(ctx context.Context, t *testing.T, name string, annotations map[string]string) {
	t.Helper()
	if err := workload.EnsureNamespace(ctx, h.Client(), harness.WorkloadNamespace); err != nil {
		t.Fatalf("ensure namespace: %v", err)
	}
	if err := workload.Delete(ctx, h.Client(), harness.WorkloadNamespace, name); err != nil {
		t.Fatalf("pre-clean %s: %v", name, err)
	}
	pod := &corev1.Pod{}
	pod.Name = name
	pod.Namespace = harness.WorkloadNamespace
	pod.Annotations = annotations
	pod.Spec.RestartPolicy = corev1.RestartPolicyNever
	pod.Spec.NodeSelector = h.GPUSelectorMap(t)
	pod.Spec.Containers = []corev1.Container{{
		Name:    harness.WorkloadContainer,
		Image:   workload.DefaultImage,
		Command: []string{"sh", "-c", "sleep 86400 & wait"},
	}}
	if err := h.Client().Ctrl.Create(ctx, pod); err != nil {
		t.Fatalf("create blocked workload %s: %v", name, err)
	}
	t.Cleanup(func() { _ = workload.Delete(context.Background(), h.Client(), harness.WorkloadNamespace, name) })
}

// waitContainerBlocked polls until the pod shows a container Waiting state with a
// create/start error reason (fail-closed), returning that reason. It fails the
// test if the pod instead reaches Running.
func waitContainerBlocked(ctx context.Context, t *testing.T, name string) string {
	t.Helper()
	var reason string
	err := waiter.PollUntil(ctx, h.CondTimeout(), h.PollInterval(), fmt.Sprintf("pod %s to be blocked at container create", name),
		func(ctx context.Context) (bool, error) {
			p := h.GetPod(ctx, t, harness.WorkloadNamespace, name)
			if p.Status.Phase == corev1.PodRunning {
				return false, fmt.Errorf("pod %s unexpectedly Running (fail-closed did not block)", name)
			}
			for _, cs := range p.Status.ContainerStatuses {
				if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" &&
					cs.State.Waiting.Reason != "ContainerCreating" && cs.State.Waiting.Reason != "PodInitializing" {
					reason = cs.State.Waiting.Reason
					return true, nil
				}
			}
			return false, nil
		})
	if err != nil {
		t.Fatalf("waiting for fail-closed block: %v", err)
	}
	return reason
}
