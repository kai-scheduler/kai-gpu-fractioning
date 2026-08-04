// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

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
