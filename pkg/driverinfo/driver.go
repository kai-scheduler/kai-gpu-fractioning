// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package driverinfo defines gpu-fractioning-owned NVIDIA driver metadata.
package driverinfo

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// NVIDIADriverMajorLabel is written by the mpsd DaemonSet after reading the
	// node-local driver version via NVML during startup.
	NVIDIADriverMajorLabel = "gpu-fractioning.kai.scheduler/nvidia-driver-version.major"
)

// ParseDriverVersionMajor extracts the major branch from an NVML driver version
// string such as "615.43.02".
func ParseDriverVersionMajor(version string) (int, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return 0, fmt.Errorf("driver version is empty")
	}

	majorText, _, _ := strings.Cut(version, ".")
	return parsePositiveInt(majorText)
}

// ParseDriverMajorLabel parses the node label value, which must contain only the
// decimal driver major version.
func ParseDriverMajorLabel(value string) (int, error) {
	return parsePositiveInt(strings.TrimSpace(value))
}

func parsePositiveInt(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("value is empty")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("value must be positive")
	}
	return parsed, nil
}
