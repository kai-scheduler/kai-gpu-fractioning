package audit

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestRemediateStopsEveryViolation(t *testing.T) {
	fake := &fakeStopper{}
	r := remediator{stopper: fake}

	r.remediate(context.Background(), []violation{
		{containerID: "c1", container: "trainer", pod: "pod1"},
		{containerID: "c2", container: "trainer", pod: "pod2"},
	})

	assertSameSet(t, fake.stoppedIDs(), []string{"c1", "c2"})
}

func TestRemediateEmptyIsNoop(t *testing.T) {
	fake := &fakeStopper{}
	r := remediator{stopper: fake}

	r.remediate(context.Background(), nil)

	if got := len(fake.stoppedIDs()); got != 0 {
		t.Fatalf("expected no stops, got %d", got)
	}
}

func TestRemediateContinuesAfterStopError(t *testing.T) {
	// The stopper fails for c2 only; c1 and c3 must still be attempted.
	fake := &fakeStopper{failIDs: map[string]error{"c2": errors.New("boom")}}
	r := remediator{stopper: fake}

	r.remediate(context.Background(), []violation{
		{containerID: "c1"},
		{containerID: "c2"},
		{containerID: "c3"},
	})

	// All three are attempted; the failing one is not recorded as stopped.
	assertSameSet(t, fake.attemptedIDs(), []string{"c1", "c2", "c3"})
	assertSameSet(t, fake.stoppedIDs(), []string{"c1", "c3"})
}

// fakeStopper is a test double recording attempted and successful stops, with an
// optional per-ID error. Safe for concurrent use so it can back an async caller.
type fakeStopper struct {
	mu        sync.Mutex
	attempted []string
	stopped   []string
	failIDs   map[string]error
}

func (f *fakeStopper) Stop(_ context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempted = append(f.attempted, containerID)
	if err, ok := f.failIDs[containerID]; ok {
		return err
	}
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeStopper) attemptedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.attempted...)
}

func (f *fakeStopper) stoppedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}
