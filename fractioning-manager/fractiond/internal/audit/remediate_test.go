// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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

	r.remediate(context.Background(), []violator{
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
	// One stop fails in the MIDDLE of the batch (c2). remediate iterates the
	// slice in order, so c3 is the load-bearing case: it comes AFTER the failure
	// and must still be attempted and stopped. If a stop error aborted the loop,
	// c3 would be neither attempted nor stopped and this test would catch it.
	fake := &fakeStopper{failIDs: map[string]error{"c2": errors.New("boom")}}
	r := remediator{stopper: fake}

	r.remediate(context.Background(), []violator{
		{containerID: "c1"}, // before the failure
		{containerID: "c2"}, // fails
		{containerID: "c3"}, // after the failure — proves the loop keeps going
	})

	// All three are attempted; only c2 fails, so c1 and c3 are recorded stopped.
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
