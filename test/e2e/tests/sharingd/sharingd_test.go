//go:build e2e

package sharingd

import (
	"context"
	"testing"
)

// TestSharingd is the ordered driver for the sharingd data-plane suite. Cases
// run as subtests; order is deterministic because FailOpenSkips patches the CR
// (via withCRConfig) and re-asserts FX-STEADY, so parallelism would be unsafe.
// assertSteady runs before the first case and again at the end so a case that
// leaks a CR override is attributed to that case, not a later run.
func TestSharingd(t *testing.T) {
	ctx := context.Background()
	h.DumpDiagOnFailure(ctx, t)

	h.AssertSteady(ctx, t) // precondition + snapshot of the shipped CR spec

	t.Run("Injection", func(t *testing.T) {
		t.Run("InjectsMemoryEnv", func(t *testing.T) { caseInjectsMemoryEnv(ctx, t) })
		t.Run("MountsMPSPipeDir", func(t *testing.T) { caseMountsMPSPipeDir(ctx, t) })
		t.Run("SkipsUnannotatedContainer", func(t *testing.T) { caseSkipsUnannotatedContainer(ctx, t) })
		t.Run("RequestOrLimitOnly", func(t *testing.T) { caseRequestOrLimitOnly(ctx, t) })
		t.Run("FailClosedBlocks", func(t *testing.T) { caseFailClosedBlocks(ctx, t) })
		t.Run("FailOpenSkips", func(t *testing.T) { caseFailOpenSkips(ctx, t) })
	})

	h.AssertSteady(ctx, t)
}
