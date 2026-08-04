// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package waiter provides a minimal polling helper. It has zero dependencies
// on the rest of the e2e framework so any package (including future
// diagnostics collection) can depend on it without risking an import cycle.
package waiter

import (
	"context"
	"fmt"
	"time"
)

// Condition reports whether the awaited state has been reached. A returned
// error aborts the poll immediately (treated as a permanent failure).
type Condition func(ctx context.Context) (done bool, err error)

// PollUntil polls cond every interval until it reports done, the context is
// cancelled, or timeout elapses. lastErr (if any) is included in the timeout
// error so failures are diagnosable without extra logging.
func PollUntil(ctx context.Context, timeout, interval time.Duration, what string, cond Condition) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for {
		done, err := cond(ctx)
		if err != nil {
			lastErr = err
		} else if done {
			return nil
		}

		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for %s: %w", what, lastErr)
			}
			return fmt.Errorf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}
