/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package configuration holds shared configuration defaults used by both
// mpsd and sharingd.
package configuration

const (
	// DefaultAnnotationPrefix is the leading prefix for the scheduler's
	// per-container GPU-sharing annotations. Full keys:
	//	<prefix><containerName>.gpu-memory.request
	//	<prefix><containerName>.gpu-memory.limit
	//	<prefix><containerName>.gpus.devices
	// Configurable via the CRD.
	DefaultAnnotationPrefix = "nvidia.com/container."

	// DefaultMPSPipeDirectory is the host path where the MPS daemon creates its
	// named pipe. Both mpsd (creates it) and sharingd (mounts it into containers)
	// reference this path.
	DefaultMPSPipeDirectory = "/run/nvidia-mps"
)
