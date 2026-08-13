// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package daemonmgr

import (
	"testing"

	"k8s.io/utils/ptr"
)

func TestResolveRuntimeClassName(t *testing.T) {
	tests := []struct {
		name string
		in   *string
		want *string
	}{
		{
			name: "nil defaults to nvidia",
			in:   nil,
			want: ptr.To(DefaultRuntimeClassName),
		},
		{
			name: "custom is propagated",
			in:   ptr.To("custom-nvidia"),
			want: ptr.To("custom-nvidia"),
		},
		{
			name: "empty uses node default",
			in:   ptr.To(""),
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveRuntimeClassName(tt.in)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("ResolveRuntimeClassName() = %q, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("ResolveRuntimeClassName() = %v, want %q", got, *tt.want)
			}
		})
	}
}
