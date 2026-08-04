// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package internal

import "fmt"

// MPSConfig holds the feature toggles rendered into the nvidia-cuda-mps-control
// TOML config file (passed via -a). memacct is enabled and context-share is
// disabled by design; only the memacct audit log is configurable (surfaced as a
// Helm value and injected via the MPS_MEMACCT_AUDIT_LOG env var).
type MPSConfig struct {
	MemacctEnabled      bool
	MemacctAuditLog     bool
	ContextShareEnabled bool
}

// TOML renders the config in the format nvidia-cuda-mps-control expects.
func (c MPSConfig) TOML() string {
	return fmt.Sprintf(`[features.memacct]
enabled=%t
audit_log=%t

[features.context-share]
enabled=%t
`, c.MemacctEnabled, c.MemacctAuditLog, c.ContextShareEnabled)
}
