// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package daemonmgr

import "testing"

func TestFIPSOnlyEnv(t *testing.T) {
	tests := []struct {
		name     string
		fipsOnly bool
		// want is the expected GODEBUG value; empty means no env var at all.
		want string
	}{
		{
			name:     "disabled returns nothing",
			fipsOnly: false,
			want:     "",
		},
		{
			name:     "enabled returns the GODEBUG override",
			fipsOnly: true,
			want:     "fips140=only,tlsmlkem=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FIPSOnlyEnv(tt.fipsOnly)

			if tt.want == "" {
				// Nil, not an empty slice. Callers append this to a container's
				// existing env, and appending an empty-but-non-nil slice would
				// turn a container that has no env of its own into one carrying
				// an empty list, changing the default DaemonSet spec.
				if got != nil {
					t.Fatalf("FIPSOnlyEnv(false) = %v, want nil", got)
				}
				return
			}

			if len(got) != 1 {
				t.Fatalf("FIPSOnlyEnv(true) returned %d vars, want 1: %v", len(got), got)
			}
			if got[0].Name != "GODEBUG" {
				t.Errorf("env name = %q, want GODEBUG", got[0].Name)
			}
			// Compared as the exact string rather than by substring. tlsmlkem=0
			// looks unrelated to FIPS and is the kind of thing that gets tidied
			// away, but without it fips140=only fails every outbound TLS
			// handshake, including the operator's to the API server.
			if got[0].Value != tt.want {
				t.Errorf("GODEBUG = %q, want %q", got[0].Value, tt.want)
			}
		})
	}
}
