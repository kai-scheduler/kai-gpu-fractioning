// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import "context"

// NoopCollector is a no-op collector used when NVML is unavailable (no driver
// installed, fake-GPU cluster). It always returns an empty snapshot so the rest
// of the pipeline keeps running and publishes idle (zero) metrics.
type NoopCollector struct{}

func (n *NoopCollector) Run(_ context.Context) {}
func (n *NoopCollector) Snapshot() GPUProcessSnapshot {
	return GPUProcessSnapshot{
		DeviceUUIDs:            map[int]string{},
		DeviceMinorToNVMLIndex: map[int]int{},
		DeviceTotalMemoryBytes: map[int]uint64{},
	}
}
func (n *NoopCollector) Close() error { return nil }
