//go:build e2e

// Package sharingd holds the sharingd data-plane e2e suite: the NRI injection
// behaviour of the sharingd daemon (GPU-memory env vars + MPS pipe mount on
// annotated containers, fail-closed/fail-open handling). The operator/controller
// surface is covered by tests/operator; metrics by tests/metrics.
//
// Run just this suite:
//
//	make test-e2e-sharingd
//
// or, against any already-deployed cluster (real or fake GPU — the suite is
// cluster-agnostic and provisions nothing):
//
//	cd test/e2e && go test -tags e2e ./tests/sharingd/... -v -timeout 20m
//
// The stack is deployed out-of-band by the operator Helm chart (make
// e2e-deploy); the suite connects, waits for FX-STEADY (the shipped default CR),
// and exercises sharingd against annotated workloads.
package sharingd

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/kai-scheduler/gpu-sharing/test/e2e/harness"
)

// h is the shared harness (one cluster connection + FX-STEADY snapshot), set in
// TestMain and used by every case via the shims in aliases_test.go.
var h *harness.Harness

func TestMain(m *testing.M) {
	ctx := context.Background()

	created, err := harness.New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sharingd e2e setup failed: connect: %v\n", err)
		os.Exit(1)
	}
	h = created

	// Fast-fail precondition: GPU nodes present and the sharingd DaemonSet
	// deployed. Full FX-STEADY is asserted at the top of TestSharingd.
	if err := h.Preflight(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "sharingd e2e preflight failed: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
