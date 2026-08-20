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
			// This is also the shape cmd/main.go produces when the sm-sharing
			// chicken bit is disabled: ContextShareDefaultSocket/SharedServerName
			// stay at their zero value, so default_socket and [servers.shared] are
			// both omitted below.
			name: "audit log enabled, sm-sharing disabled (context-share off, no shared server)",
			cfg:  MPSConfig{MemacctEnabled: true, MemacctAuditLog: true, ContextShareEnabled: false},
			want: "[features.memacct]\nenabled=true\naudit_log=true\n\n[features.context-share]\nenabled=false\n",
		},
		{
			name: "audit log disabled, sm-sharing disabled (context-share off, no shared server)",
			cfg:  MPSConfig{MemacctEnabled: true, MemacctAuditLog: false, ContextShareEnabled: false},
			want: "[features.memacct]\nenabled=true\naudit_log=false\n\n[features.context-share]\nenabled=false\n",
		},
		{
			name: "context-share with shared server (production shape)",
			cfg: MPSConfig{
				MemacctEnabled:            true,
				MemacctAuditLog:           true,
				ContextShareEnabled:       true,
				ContextShareDefaultSocket: "off",
				SharedServerName:          "shared",
			},
			want: "[features.memacct]\nenabled=true\naudit_log=true\n\n[features.context-share]\nenabled=true\ndefault_socket=\"off\"\n\n[servers.shared]\n",
		},
		{
			name: "context-share enabled without default_socket or shared server",
			cfg:  MPSConfig{MemacctEnabled: true, MemacctAuditLog: true, ContextShareEnabled: true},
			want: "[features.memacct]\nenabled=true\naudit_log=true\n\n[features.context-share]\nenabled=true\n",
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
