package metrics

import "context"

// collector is the internal interface satisfied by every GPU process collector.
// It extends the public GPUProcessCollector with a Run loop consumed by the exporter.
type collector interface {
	GPUProcessCollector
	Run(ctx context.Context)
}
