// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package configuration holds shared configuration defaults used by both
// mpsd and fractiond.
package configuration

const (
	// DefaultAnnotationPrefix is the leading prefix for the scheduler's
	// per-container GPU-fractioning annotations. Full keys:
	//	<prefix><containerName>.gpu-memory.request
	//	<prefix><containerName>.gpu-memory.limit
	//	<prefix><containerName>.gpus.devices
	// Configurable via the CRD.
	DefaultAnnotationPrefix = "nvidia.com/container."

	// DefaultMPSPipeDirectory is the host path where the MPS daemon creates its
	// named pipe. Both mpsd (creates it) and fractiond (mounts it into containers)
	// reference this path.
	DefaultMPSPipeDirectory = "/run/nvidia-mps"
)
