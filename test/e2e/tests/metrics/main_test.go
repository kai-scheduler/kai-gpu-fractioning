//go:build e2e

// Package metrics holds the gpu-sharing-plugin metrics e2e suite. Run just this
// suite with:
//
//	make test-e2e-metrics
//
// or, against a cluster already created and with the plugin image already
// loaded:
//
//	cd test/e2e && go test -tags e2e ./tests/metrics/... -v -timeout 20m
//
// Each e2e suite lives in its own tests/<suite> package with its own TestMain,
// so a CI job can target exactly one suite (e.g. the Metrics e2e job runs only
// ./tests/metrics/...). See test/e2e/README.md for the full workflow. This
// suite does not create or destroy clusters — it runs an ordered setup
// (connect → preflight → deploy) against an existing cluster.
package metrics

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/run-ai/gpu-sharing-operator/test/e2e/suite"
)

var s *suite.Suite

func TestMain(m *testing.M) {
	ctx := context.Background()

	created, err := suite.New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e suite setup failed: connect: %v\n", err)
		os.Exit(1)
	}
	s = created

	// Preflight is an explicit, fast-fail precondition step run before the
	// (slower) deploy, so a misconfigured cluster fails immediately with a
	// clear message instead of a rollout timeout.
	if err := s.Preflight(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "e2e preflight failed: %v\n", err)
		os.Exit(1)
	}

	if err := s.Deploy(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "e2e deploy failed: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
