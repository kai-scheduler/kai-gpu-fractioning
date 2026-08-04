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
