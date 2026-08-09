// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package annotations

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// Per-container GPU-fractioning annotation keys have the shape
	// <prefix><containerName>.<...>, where <prefix> is the configurable leading
	// prefix (configuration.DefaultAnnotationPrefix = "nvidia.com/container."). The
	// scheduler emits, for a container named "trainer":
	//
	//	nvidia.com/container.trainer.gpu-memory.request
	//	nvidia.com/container.trainer.gpu-memory.limit
	//	nvidia.com/container.trainer.gpus.devices
	//
	// memoryInfix sits between the container name and the request/limit suffix;
	// devicesSuffix is the trailing segment of the device-assignment key.
	memoryInfix   = "gpu-memory"
	devicesSuffix = "gpus.devices"

	// Annotation key suffixes appended after the memory infix.
	annotationSuffixRequest = "request"
	annotationSuffixLimit   = "limit"

	// Minimum memory MB value that MPS can meaningfully enforce.
	minDecimalMB = 1

	// bytesPerDecimalMB is the divisor for DecimalSI and bare-byte quantities.
	bytesPerDecimalMB = 1_000_000

	// bytesPerBinaryMiB is the divisor for converting BinarySI quantities to
	// the MiB-count MB integer NVIDIA expects.
	bytesPerBinaryMiB = 1024 * 1024
)

// GPUMemoryConfig holds the parsed GPU memory request and limit for a container.
// Values are memory MB strings suitable for NVIDIA MPS environment variables.
// Empty string means the annotation was not present.
//
// This intentionally does NOT reuse resource.Quantity or a k8s resource struct:
// the source data comes from pod annotations, not from a k8s resource spec, and
// the values are already converted to the memory MB format that MPS expects.
type GPUMemoryConfig struct {
	Request string
	Limit   string
}

// IsEmpty returns true if neither request nor limit was specified.
func (c GPUMemoryConfig) IsEmpty() bool {
	return c.Request == "" && c.Limit == ""
}

// ApplyDefaults fills a missing request or limit from the other so a container
// that specified only one of the two ends up with both, and with request ==
// limit. This makes enforcement symmetric with the whole-GPU path:
//
//   - request only: the limit defaults to the request, so the memory cap
//     (NVIDIA_GPU_MEMORY_LIMITS) is enforced at the requested size instead of
//     being unbounded.
//   - limit only: the request defaults to the limit, so the requested size
//     (NVIDIA_GPU_MEMORY_REQUESTS, used for metrics/fraction accounting) is
//     populated.
//
// A config with neither set (IsEmpty) is returned unchanged.
func (c GPUMemoryConfig) ApplyDefaults() GPUMemoryConfig {
	if c.Request == "" {
		c.Request = c.Limit
	}
	if c.Limit == "" {
		c.Limit = c.Request
	}
	return c
}

// EffectiveMemoryMB returns the container's allocated GPU memory in memory MB:
// the limit if set, otherwise the request, otherwise 0. Request and Limit are
// already memory MB strings (see parseToMemoryMB), so this just parses one back
// to an integer. It is the numerator the metrics sidecar uses to derive the GPU
// fraction (requested memory ÷ device total memory) for SM-util normalization.
func (c GPUMemoryConfig) EffectiveMemoryMB() int64 {
	value := c.Limit
	if value == "" {
		value = c.Request
	}
	if value == "" {
		return 0
	}
	mb, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return mb
}

// ParseGPUMemoryAnnotations extracts GPU memory configuration for a specific
// container from the pod's annotation map.
//
// Annotation format:
//
//	<prefix><containerName>.gpu-memory.request = "<quantity>"
//	<prefix><containerName>.gpu-memory.limit   = "<quantity>"
//
// With the default prefix "nvidia.com/container.", a full key is:
//
//	nvidia.com/container.trainer.gpu-memory.request
//	nvidia.com/container.trainer.gpu-memory.limit
//
// Values must be valid Kubernetes Quantities (e.g. "4Gi", "2048Mi", "4096M").
// BinarySI values preserve their MiB count (7680Mi -> "7680"); DecimalSI and
// bare-byte values are converted to decimal MB.
// Returns an error if an annotation exists but cannot be parsed, or if the
// resolved value is less than 1 MB.
func ParseGPUMemoryAnnotations(annotations map[string]string, containerName, prefix string) (GPUMemoryConfig, error) {
	config := GPUMemoryConfig{}

	requestKey := containerMemoryAnnotationKey(prefix, containerName, annotationSuffixRequest)
	limitKey := containerMemoryAnnotationKey(prefix, containerName, annotationSuffixLimit)

	if val, ok := annotations[requestKey]; ok {
		mb, err := parseToMemoryMB(val)
		if err != nil {
			return config, fmt.Errorf("parsing annotation %q: %w", requestKey, err)
		}
		config.Request = mb
	}

	if val, ok := annotations[limitKey]; ok {
		mb, err := parseToMemoryMB(val)
		if err != nil {
			return config, fmt.Errorf("parsing annotation %q: %w", limitKey, err)
		}
		config.Limit = mb
	}

	return config, nil
}

// ParseVisibleDevices returns the GPU device assignment the scheduler recorded
// for a specific container — the per-container key
// "<prefix><containerName>.gpus.devices" (e.g.
// "nvidia.com/container.trainer.gpus.devices") — trimmed of surrounding
// whitespace. The value is a comma-separated list of NVIDIA GPU UUIDs (or
// indices), e.g. "GPU-abc123,GPU-def456". A fractional container does not request
// the nvidia.com/gpu resource, so the NVIDIA device plugin never injects
// NVIDIA_VISIBLE_DEVICES; fractiond reads this annotation and injects it (as
// injection.EnvVisibleDevices) so the container sees exactly the GPU the
// scheduler picked. It returns "" when the annotation is absent or blank, which
// the caller treats as "no assignment to inject" (so a container that already
// carries NVIDIA_VISIBLE_DEVICES is left untouched). The value is passed through
// verbatim otherwise — its format (UUID list, index list, or "all") is the
// NVIDIA container runtime's contract, not this operator's.
func ParseVisibleDevices(annotations map[string]string, containerName, prefix string) string {
	return strings.TrimSpace(annotations[containerDevicesAnnotationKey(prefix, containerName)])
}

// quantityToMemoryMB converts a Kubernetes Quantity string to the memory MB
// value NVIDIA expects. BinarySI quantities become a MiB count; DecimalSI and
// bare-byte quantities become decimal MB.
// Returns an error if the value is not a valid Quantity.
func quantityToMemoryMB(value string) (int64, error) {
	qty, err := resource.ParseQuantity(value)
	if err != nil {
		return 0, fmt.Errorf("invalid quantity %q: %w", value, err)
	}

	bytes := qty.Value()
	if bytes < 0 {
		return 0, fmt.Errorf("negative quantity: %s", value)
	}

	if qty.Format == resource.BinarySI {
		return bytes / bytesPerBinaryMiB, nil
	}

	return bytes / bytesPerDecimalMB, nil
}

// parseToMemoryMB converts a Kubernetes Quantity string to a memory MB string.
// Validates the result is at least minDecimalMB.
func parseToMemoryMB(value string) (string, error) {
	mb, err := quantityToMemoryMB(value)
	if err != nil {
		return "", err
	}

	if mb < minDecimalMB {
		return "", fmt.Errorf("quantity %q resolves to %d MB, minimum is %d MB", value, mb, minDecimalMB)
	}

	return strconv.FormatInt(mb, 10), nil
}

// containerMemoryAnnotationKey builds the full annotation key for a container's
// GPU memory setting.
// Example: containerMemoryAnnotationKey("nvidia.com/container.", "trainer", "limit")
//
//	→ "nvidia.com/container.trainer.gpu-memory.limit"
func containerMemoryAnnotationKey(prefix, containerName, suffix string) string {
	return prefix + containerName + "." + memoryInfix + "." + suffix
}

// containerDevicesAnnotationKey builds the per-container GPU device-assignment
// annotation key.
// Example: containerDevicesAnnotationKey("nvidia.com/container.", "trainer")
//
//	→ "nvidia.com/container.trainer.gpus.devices"
func containerDevicesAnnotationKey(prefix, containerName string) string {
	return prefix + containerName + "." + devicesSuffix
}

// RequestAnnotationKey returns the annotation key fractiond looks up for a
// container's GPU-memory request (e.g. for diagnostic logging).
func RequestAnnotationKey(prefix, containerName string) string {
	return containerMemoryAnnotationKey(prefix, containerName, annotationSuffixRequest)
}

// DevicesAnnotationKey returns the annotation key fractiond looks up for a
// container's GPU device assignment.
func DevicesAnnotationKey(prefix, containerName string) string {
	return containerDevicesAnnotationKey(prefix, containerName)
}

// LimitAnnotationKey returns the annotation key fractiond looks up for a
// container's GPU-memory limit (e.g. for diagnostic logging of the annotation a
// non-fractioning container is missing).
func LimitAnnotationKey(prefix, containerName string) string {
	return containerMemoryAnnotationKey(prefix, containerName, annotationSuffixLimit)
}
