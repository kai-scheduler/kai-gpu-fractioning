//go:build !e2e

package metrics

import (
	"context"
	"log/slog"
	"time"
)

// newCollector tries to initialise an NVML-backed collector. When NVML is not
// available we fall back to a no-op collector instead of failing hard, because
// this binary also runs on fake-GPU test clusters that have no driver installed.
// On those nodes the exporter still starts and emits pod-label metrics with zero
// GPU counters, which is useful for integration tests. On real production nodes
// NVML is always present, so the fallback path is exercised only in CI.
func newCollector(ctx context.Context, interval time.Duration, logger *slog.Logger) collector {
	if c, err := newNVMLProcessCollector(interval, logger); err == nil {
		return c
	} else {
		logger.WarnContext(ctx, "NVML unavailable; GPU process metrics will be zero (pod labels still reported)", "error", err)
		return &NoopCollector{}
	}
}
