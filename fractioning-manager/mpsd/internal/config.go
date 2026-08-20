// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package internal

import "fmt"

// MPSConfig holds the feature toggles rendered into the nvidia-cuda-mps-control
// TOML config file (passed via -a). memacct is always on; only its audit log is
// user-configurable (surfaced as a Helm value and injected via the
// MPS_MEMACCT_AUDIT_LOG env var). context-share is also always on so
// fractiond can route sm-sharing containers to the parameterless shared
// server named by SharedServerName instead of the default per-node one.
type MPSConfig struct {
	MemacctEnabled      bool
	MemacctAuditLog     bool
	ContextShareEnabled bool
	// ContextShareDefaultSocket sets [features.context-share].default_socket
	// when non-empty (e.g. "off"); empty omits the line.
	ContextShareDefaultSocket string
	// SharedServerName renders a parameterless [servers.<name>] block when
	// non-empty; empty omits the block entirely.
	SharedServerName string
}

// TOML renders the config in the format nvidia-cuda-mps-control expects. With
// every field set (the production shape), it renders:
//
//	[features.memacct]
//	enabled=true
//	audit_log=true
//
//	[features.context-share]
//	enabled=true
//	default_socket="off"
//
//	[servers.shared]
func (c MPSConfig) TOML() string {
	out := fmt.Sprintf("[features.memacct]\nenabled=%t\naudit_log=%t\n\n[features.context-share]\nenabled=%t\n",
		c.MemacctEnabled, c.MemacctAuditLog, c.ContextShareEnabled)
	if c.ContextShareDefaultSocket != "" {
		out += fmt.Sprintf("default_socket=%q\n", c.ContextShareDefaultSocket)
	}
	if c.SharedServerName != "" {
		out += fmt.Sprintf("\n[servers.%s]\n", c.SharedServerName)
	}
	return out
}
