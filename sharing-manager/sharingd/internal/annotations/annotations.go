package annotations

import (
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// Annotation key suffixes appended after the container name.
	annotationSuffixRequest = "request"
	annotationSuffixLimit   = "limit"
	annotationPathGPUMemory = "gpu-memory"

	// VisibleDevicesAnnotation is the pod-level annotation key the scheduler
	// (KAI/Run:ai) sets to the physical GPU device(s) it assigned to a fractional
	// GPU-sharing pod: a comma-separated list of NVIDIA GPU UUIDs (or indices),
	// e.g. "GPU-abc123,GPU-def456". A fractional pod does not request the
	// nvidia.com/gpu resource, so the NVIDIA device plugin never injects
	// NVIDIA_VISIBLE_DEVICES into its containers; sharingd reads this annotation
	// and injects it (as injection.EnvVisibleDevices) so the container gets access
	// to exactly the GPU the scheduler picked.
	annotationSuffixDevices = "gpus.devices"

	// Minimum value in decimal MB that MPS can meaningfully enforce.
	minDecimalMB = 1

	// bytesPerDecimalMB is the divisor for converting bytes to SI/decimal megabytes.
	// NVIDIA MPS uses decimal MB (÷ 1,000,000), NOT binary MiB.
	bytesPerDecimalMB = 1_000_000
)

// GPUMemoryConfig holds the parsed GPU memory request and limit for a container.
// Values are decimal MB strings suitable for NVIDIA MPS environment variables.
// Empty string means the annotation was not present.
//
// This intentionally does NOT reuse resource.Quantity or a k8s resource struct:
// the source data comes from pod annotations, not from a k8s resource spec, and
// the values are already converted to the decimal-MB format that MPS expects.
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

// EffectiveMemoryMB returns the container's allocated GPU memory in decimal MB:
// the limit if set, otherwise the request, otherwise 0. Request and Limit are
// already decimal-MB strings (see parseToDecimalMB), so this just parses one back
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
// Returns an error if an annotation exists but cannot be parsed, or if the
// resolved value is less than 1 MB.
func ParseGPUMemoryAnnotations(annotations map[string]string, containerName, prefix string) (GPUMemoryConfig, error) {
	config := GPUMemoryConfig{}

	requestKey := containerMemoryAnnotationKey(prefix, containerName, annotationSuffixRequest)
	limitKey := containerMemoryAnnotationKey(prefix, containerName, annotationSuffixLimit)

	if val, ok := annotations[requestKey]; ok {
		mb, err := parseToDecimalMB(val)
		if err != nil {
			return config, fmt.Errorf("parsing annotation %q: %w", requestKey, err)
		}
		config.Request = mb
	}

	if val, ok := annotations[limitKey]; ok {
		mb, err := parseToDecimalMB(val)
		if err != nil {
			return config, fmt.Errorf("parsing annotation %q: %w", limitKey, err)
		}
		config.Limit = mb
	}

	return config, nil
}

// ParseVisibleDevices returns the GPU device assignment the scheduler recorded
// for the named container, trimmed of surrounding whitespace. It returns "" when
// the annotation is absent or blank, which the caller treats as "no assignment
// to inject" (so a pod scheduled by a device plugin that already sets
// NVIDIA_VISIBLE_DEVICES is left untouched). The value is passed through
// verbatim otherwise — its format (UUID list, index list, or "all") is the
// NVIDIA container runtime's contract, not this operator's.
func ParseVisibleDevices(annotations map[string]string, containerName, prefix string) string {
	if annotations == nil {
		return ""
	}
	key := ContainerDevicesAnnotationKey(prefix, containerName)
	if val := strings.TrimSpace(annotations[key]); val != "" {
		return val
	}
	return ""
}

// quantityToDecimalMB converts a Kubernetes Quantity string to decimal megabytes.
// Returns an error if the value is not a valid Quantity.
func quantityToDecimalMB(value string) (int64, error) {
	qty, err := resource.ParseQuantity(value)
	if err != nil {
		return 0, fmt.Errorf("invalid quantity %q: %w", value, err)
	}

	bytes := qty.Value()
	if bytes < 0 {
		return 0, fmt.Errorf("negative quantity: %s", value)
	}

	return bytes / bytesPerDecimalMB, nil
}

// parseToDecimalMB converts a Kubernetes Quantity string to a decimal megabyte string.
// Validates the result is at least minDecimalMB.
func parseToDecimalMB(value string) (string, error) {
	mb, err := quantityToDecimalMB(value)
	if err != nil {
		return "", err
	}

	if mb < minDecimalMB {
		return "", fmt.Errorf("quantity %q resolves to %d MB, minimum is %d MB", value, mb, minDecimalMB)
	}

	return strconv.FormatInt(mb, 10), nil
}

// ParseContainerAnnotationKey extracts the container name and annotation path
// from a key in the shared container-scoped format:
//
//	<prefix><containerName>.<path>
//
// Example: ParseContainerAnnotationKey("nvidia.com/container.trainer.gpu-memory.limit", "nvidia.com/container.")
// returns "trainer", "gpu-memory.limit", true.
func ParseContainerAnnotationKey(key, prefix string) (containerName, path string, ok bool) {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return "", "", false
	}
	containerName, path, ok = strings.Cut(rest, ".")
	if !ok || containerName == "" || path == "" {
		return "", "", false
	}
	return containerName, path, true
}

// containerMemoryAnnotationKey builds the full annotation key for a container's
// GPU memory setting.
// Example: containerMemoryAnnotationKey("nvidia.com/container.", "trainer", "limit")
//
//	→ "nvidia.com/container.trainer.gpu-memory.limit"
func containerMemoryAnnotationKey(prefix, containerName, suffix string) string {
	return containerAnnotationKey(prefix, containerName, annotationPathGPUMemory+"."+suffix)
}

// ContainerDevicesAnnotationKey returns the annotation key for a container's GPU
// device assignment.
func ContainerDevicesAnnotationKey(prefix, containerName string) string {
	return containerAnnotationKey(prefix, containerName, annotationSuffixDevices)
}

func containerAnnotationKey(prefix, containerName, path string) string {
	return prefix + containerName + "." + path
}

// LimitAnnotationKey returns the annotation key sharingd looks up for a
// container's GPU-memory limit (e.g. for diagnostic logging of the annotation a
// non-sharing container is missing).
func LimitAnnotationKey(prefix, containerName string) string {
	return containerMemoryAnnotationKey(prefix, containerName, annotationSuffixLimit)
}

// RequestAnnotationKey returns the annotation key sharingd looks up for a
// container's GPU-memory request.
func RequestAnnotationKey(prefix, containerName string) string {
	return containerMemoryAnnotationKey(prefix, containerName, annotationSuffixRequest)
}
