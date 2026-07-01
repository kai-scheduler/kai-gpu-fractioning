//go:build e2e

// Package tests holds the gpu-sharing e2e suite. Run with:
//
//	make e2e
//
// or, against a cluster already created and with the plugin image already
// loaded:
//
//	cd test/e2e && go test -tags e2e ./tests/... -v -timeout 20m
//
// See test/e2e/hack/create-cluster.py for cluster provisioning (k3d +
// fake-gpu-operator, configurable node count) and test/e2e/README.md for the
// full workflow. This suite does not create or destroy clusters — it
// verifies the cluster's GPU nodes and deploys the gpu-sharing-plugin
// DaemonSet under test onto it.
package tests

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
		fmt.Fprintf(os.Stderr, "e2e suite setup failed: %v\n", err)
		os.Exit(1)
	}
	s = created

	os.Exit(m.Run())
}
