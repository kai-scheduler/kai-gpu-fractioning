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

	// SharedMPSSocketPath is the path segment fractiond appends to
	// DefaultMPSPipeDirectory to reach the shared MPS server's default
	// socket: <DefaultMPSPipeDirectory>/<SharedMPSSocketPath>. mpsd renders
	// the "shared" server (the first segment, [servers.shared] in the
	// control-daemon TOML config) with a "default" MAWS namespace (the
	// second segment) for clients that don't request a specific one.
	SharedMPSSocketPath = "shared/default"

	// ContainerMPSPipeDirectory is the fixed in-container path sm-sharing
	// containers mount their MPS pipe directory at (decoupled from the
	// host-side shared-server subpath so the container never needs to know
	// about server/namespace naming on the node).
	ContainerMPSPipeDirectory = "/tmp/nvidia-mps"
)
