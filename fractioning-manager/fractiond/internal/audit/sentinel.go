// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package audit

import (
	"context"
	"log/slog"
	"sync"

	"github.com/containerd/nri/pkg/api"
)

// Sentinel ties detection to remediation for retroactive enforcement on NRI
// reconnect. It is the package's public entry point; the detector and
// remediator it drives stay unexported so all policy lives here.
//
// Detection is synchronous and pure, so it is safe on the NRI callback
// goroutine. Remediation runs on a background goroutine because stopping a
// container blocks on the runtime's grace period, which must never stall the
// callback. Only the small []violator crosses into the goroutine — never the
// runtime snapshot slices.
type Sentinel struct {
	detector   detector
	remediator remediator
	wg         sync.WaitGroup
}

// NewSentinel builds a Sentinel. annotationPrefix, mpsPipeDirectory, and
// smSharingEnabled must match the fractiond plugin's create-hook configuration
// so detection mirrors injection exactly. stopper must be non-nil; log
// defaults to slog.Default() when nil.
func NewSentinel(annotationPrefix, mpsPipeDirectory string, smSharingEnabled bool, stopper ContainerStopper, log *slog.Logger) *Sentinel {
	if log == nil {
		log = slog.Default()
	}
	return &Sentinel{
		detector: detector{
			annotationPrefix: annotationPrefix,
			mpsPipeDirectory: mpsPipeDirectory,
			smSharingEnabled: smSharingEnabled,
			log:              log,
		},
		remediator: remediator{
			stopper: stopper,
			log:     log,
		},
	}
}

// Audit inspects the NRI Synchronize snapshot synchronously — cheap and safe on
// the callback goroutine — and, if it finds GPU-fractioning containers missing
// injection, stops them on a background goroutine. It returns immediately.
//
// The stop context is detached from ctx (WithoutCancel) so remediation outlives
// the NRI callback that triggered it; a Synchronize handler returning must not
// cancel in-flight stops. Call Wait to block until remediation completes.
func (s *Sentinel) Audit(ctx context.Context, pods []*api.PodSandbox, containers []*api.Container) {
	violators := s.detector.violators(pods, containers)
	if len(violators) == 0 {
		return
	}

	stopCtx := context.WithoutCancel(ctx)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.remediator.remediate(stopCtx, violators)
	}()
}

// Wait blocks until all in-flight remediation goroutines have finished. Used by
// graceful shutdown and by tests to observe remediation deterministically.
func (s *Sentinel) Wait() {
	s.wg.Wait()
}
