// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package internal

import "testing"

func TestMPSConfig_TOML(t *testing.T) {
	tests := []struct {
		name string
		cfg  MPSConfig
		want string
	}{
		{
			name: "audit log enabled",
			cfg:  MPSConfig{MemacctEnabled: true, MemacctAuditLog: true, ContextShareEnabled: false},
			want: "[features.memacct]\nenabled=true\naudit_log=true\n\n[features.context-share]\nenabled=false\n",
		},
		{
			name: "audit log disabled",
			cfg:  MPSConfig{MemacctEnabled: true, MemacctAuditLog: false, ContextShareEnabled: false},
			want: "[features.memacct]\nenabled=true\naudit_log=false\n\n[features.context-share]\nenabled=false\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.TOML(); got != tt.want {
				t.Errorf("TOML() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
