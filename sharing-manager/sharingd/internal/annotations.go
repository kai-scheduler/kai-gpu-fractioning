package internal

import (
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// annotationGPUMemoryPrefix is the key prefix for fractional GPU memory annotations.
	// Full key: <prefix><containerName>.{request,limit}
	annotationGPUMemoryPrefix = "nvidia.com/gpu-memory.container."

	// Annotation key suffixes appended after the container name.
	annotationSuffixRequest = "request"
	annotationSuffixLimit   = "limit"

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

// ParseGPUMemoryAnnotations extracts GPU memory configuration for a specific
// container from the pod's annotation map.
//
// Annotation format:
//
//	<prefix><containerName>.request = "<quantity>"
//	<prefix><containerName>.limit   = "<quantity>"
//
// With the default prefix "nvidia.com/gpu-memory.container.", a full key is:
//
//	nvidia.com/gpu-memory.container.trainer.request
//	nvidia.com/gpu-memory.container.trainer.limit
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

// containerMemoryAnnotationKey builds the full annotation key for a container's
// GPU memory setting.
// Example: containerMemoryAnnotationKey("nvidia.com/gpu-memory.container.", "trainer", "limit")
//
//	→ "nvidia.com/gpu-memory.container.trainer.limit"
func containerMemoryAnnotationKey(prefix, containerName, suffix string) string {
	return prefix + containerName + "." + suffix
}
