// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package injection

import (
	"testing"

	"github.com/kai-scheduler/kai-gpu-fractioning/fractioning-manager/fractiond/internal/annotations"
)

// The want values below are spelled out as literals rather than built from the
// configuration constants: these exact paths are the contract with mpsd (which
// renders the shared server at <pipeDir>/shared/default) and with the container
// image (which reads CUDA_MPS_PIPE_DIRECTORY=/tmp/nvidia-mps). Deriving them
// from the same constants the code uses would make the test agree with any
// change to those constants, including a breaking one.
func TestMPSPipeMount(t *testing.T) {
	tests := []struct {
		name            string
		mpsPipeDir      string
		mode            annotations.ComputeMode
		wantSource      string
		wantDestination string
	}{
		{
			name:            "sm-sharing routes to the shared server socket",
			mpsPipeDir:      "/run/nvidia-mps",
			mode:            annotations.ComputeModeSMSharing,
			wantSource:      "/run/nvidia-mps/shared/default",
			wantDestination: "/tmp/nvidia-mps",
		},
		{
			name:            "time-slicing mounts the pipe directory as-is",
			mpsPipeDir:      "/run/nvidia-mps",
			mode:            annotations.ComputeModeTimeSlicing,
			wantSource:      "/run/nvidia-mps",
			wantDestination: "/run/nvidia-mps",
		},
		{
			// The plugin treats a missing compute-mode annotation as
			// time-slicing, but guard the zero value directly: an unset mode
			// must never select the shared socket.
			name:            "empty mode behaves as time-slicing",
			mpsPipeDir:      "/run/nvidia-mps",
			mode:            "",
			wantSource:      "/run/nvidia-mps",
			wantDestination: "/run/nvidia-mps",
		},
		{
			// The pipe directory is CRD-configurable, so only the shared
			// subpath is fixed — the host prefix has to follow the config.
			name:            "sm-sharing honors a non-default pipe directory",
			mpsPipeDir:      "/var/run/mps",
			mode:            annotations.ComputeModeSMSharing,
			wantSource:      "/var/run/mps/shared/default",
			wantDestination: "/tmp/nvidia-mps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, destination := MPSPipeMount(tt.mpsPipeDir, tt.mode)
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
			if destination != tt.wantDestination {
				t.Errorf("destination = %q, want %q", destination, tt.wantDestination)
			}
		})
	}
}
