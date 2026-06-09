package metrics

import (
	"context"
	"log/slog"
	"time"
)

// collector is the internal interface satisfied by every GPU process collector.
// It extends the public GPUProcessCollector with a Run loop consumed by the exporter.
type collector interface {
	GPUProcessCollector
	Run(ctx context.Context)
}

// newCollector returns an nvmlProcessCollector when NVML is available, or a
// NoopCollector when it is not (no driver installed, fake-GPU cluster). The
// caller always gets a working collector; NVML absence is logged as a warning.
func newCollector(ctx context.Context, interval time.Duration, logger *slog.Logger) collector {
	if c, err := newNVMLProcessCollector(interval, logger); err == nil {
		return c
	} else {
		logger.WarnContext(ctx, "NVML unavailable; GPU process metrics will be zero (pod labels still reported)", "error", err)
		return &NoopCollector{}
	}
}
