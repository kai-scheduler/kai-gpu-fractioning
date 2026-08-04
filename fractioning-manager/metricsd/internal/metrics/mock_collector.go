package metrics

type fakeCollector struct {
	snapshots int
	snapshot  GPUProcessSnapshot
}

func (c *fakeCollector) Close() error { return nil }
func (c *fakeCollector) Snapshot() GPUProcessSnapshot {
	c.snapshots++
	return c.snapshot
}
