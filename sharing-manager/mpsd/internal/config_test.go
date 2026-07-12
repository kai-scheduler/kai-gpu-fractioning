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
