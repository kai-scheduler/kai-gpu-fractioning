// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"testing"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

func TestCollectIntervalFloorAndDefault(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{"unset uses default", 0, 5 * time.Second},
		{"negative uses default", -1, 5 * time.Second},
		{"below floor clamps to 2s", 1 * time.Second, 2 * time.Second},
		{"at floor kept", 2 * time.Second, 2 * time.Second},
		{"above floor kept", 7 * time.Second, 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Config{Interval: tt.interval}).collectInterval(); got != tt.want {
				t.Fatalf("collectInterval(%v) = %v, want %v", tt.interval, got, tt.want)
			}
		})
	}
}

func TestShouldSkipProcessUtilizationSample(t *testing.T) {
	tests := []struct {
		name   string
		sample nvml.ProcessUtilizationSample
		want   bool
	}{
		{
			name:   "valid sample",
			sample: nvml.ProcessUtilizationSample{Pid: 1234, SmUtil: 100, MemUtil: 100, EncUtil: 100, DecUtil: 100, TimeStamp: 101},
		},
		{
			name:   "zero pid",
			sample: nvml.ProcessUtilizationSample{Pid: 0, TimeStamp: 101},
			want:   true,
		},
		{
			name:   "sm utilization above range",
			sample: nvml.ProcessUtilizationSample{Pid: 1234, SmUtil: 101, TimeStamp: 101},
			want:   true,
		},
		{
			name:   "memory utilization above range",
			sample: nvml.ProcessUtilizationSample{Pid: 1234, MemUtil: 101, TimeStamp: 101},
			want:   true,
		},
		{
			name:   "encoder utilization above range",
			sample: nvml.ProcessUtilizationSample{Pid: 1234, EncUtil: 101, TimeStamp: 101},
			want:   true,
		},
		{
			name:   "decoder utilization above range",
			sample: nvml.ProcessUtilizationSample{Pid: 1234, DecUtil: 101, TimeStamp: 101},
			want:   true,
		},
		{
			name:   "old timestamp",
			sample: nvml.ProcessUtilizationSample{Pid: 1234, TimeStamp: 100},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipProcessUtilizationSample(tt.sample, 100)
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
