package audit

import (
	"context"
	"testing"

	"github.com/containerd/nri/pkg/api"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/common/configuration"
)

func newSentinel(stopper ContainerStopper) *Sentinel {
	return NewSentinel(configuration.DefaultAnnotationPrefix, configuration.DefaultMPSPipeDirectory, stopper, nil)
}

func TestAuditStopsOnlyViolations(t *testing.T) {
	fake := &fakeStopper{}
	e := newSentinel(fake)

	pods := []*api.PodSandbox{
		sharingPod("p1", "pod1", "trainer", "4Gi", ""),
		sharingPod("p2", "pod2", "trainer", "4Gi", ""),
	}
	containers := []*api.Container{
		{ // injected → ok
			Id: "c1", Name: "trainer", PodSandboxId: "p1",
			State: api.ContainerState_CONTAINER_RUNNING, Env: injectedEnv(true, false), Mounts: []*api.Mount{mpsMount()},
		},
		{ // uninjected → violation
			Id: "c2", Name: "trainer", PodSandboxId: "p2",
			State: api.ContainerState_CONTAINER_RUNNING,
		},
	}

	e.Audit(context.Background(), pods, containers)
	e.Wait()

	assertSameSet(t, fake.stoppedIDs(), []string{"c2"})
}

func TestAuditNoViolationsNoStops(t *testing.T) {
	fake := &fakeStopper{}
	e := newSentinel(fake)

	pods := []*api.PodSandbox{sharingPod("p1", "pod1", "trainer", "4Gi", "")}
	containers := []*api.Container{{
		Id: "c1", Name: "trainer", PodSandboxId: "p1",
		State: api.ContainerState_CONTAINER_RUNNING, Env: injectedEnv(true, false), Mounts: []*api.Mount{mpsMount()},
	}}

	e.Audit(context.Background(), pods, containers)
	e.Wait()

	if got := len(fake.stoppedIDs()); got != 0 {
		t.Fatalf("expected no stops, got %d", got)
	}
}
