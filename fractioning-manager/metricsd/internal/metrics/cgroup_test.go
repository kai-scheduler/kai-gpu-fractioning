// Copyright 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func assertPaths(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d; got %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseCgroupPaths(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "mixed v2 and v1 lines",
			input: "0::/kubepods.slice/pod.slice/container.scope\n11:memory:/legacy/path\n",
			want:  []string{"/kubepods.slice/pod.slice/container.scope", "/legacy/path"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "only blank lines",
			input: "\n\n\n",
			want:  nil,
		},
		{
			name:  "line with only one field",
			input: "0\n",
			want:  nil,
		},
		{
			name:  "line with only two fields",
			input: "0:memory\n",
			want:  nil,
		},
		{
			name:  "line with empty path field",
			input: "0::\n",
			want:  nil,
		},
		{
			name:  "line with whitespace-only path field",
			input: "0::   \n",
			want:  nil,
		},
		{
			name:  "valid line mixed with invalid lines",
			input: "bad\n0::/valid/path\n:\n",
			want:  []string{"/valid/path"},
		},
		{
			name:  "trailing newlines do not produce extra entries",
			input: "0::/only/path\n\n\n",
			want:  []string{"/only/path"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertPaths(t, parseCgroupPaths(tc.input), tc.want)
		})
	}
}

func TestNewProcCgroupResolverDefaults(t *testing.T) {
	tests := []struct {
		name     string
		procRoot string
	}{
		{name: "empty string", procRoot: ""},
		{name: "whitespace only", procRoot: "   "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewProcCgroupResolver(tc.procRoot)
			if r.ProcRoot != defaultProcRoot {
				t.Errorf("ProcRoot = %q, want %q", r.ProcRoot, defaultProcRoot)
			}
		})
	}
}

func TestProcCgroupResolverCgroupPaths(t *testing.T) {
	const pid = uint32(123)

	tests := []struct {
		name        string
		fileContent string // empty means: don't create the pid dir (error case)
		wantPaths   []string
		wantErr     bool
	}{
		{
			name:        "single cgroup v2 path",
			fileContent: "0::/container/path\n",
			wantPaths:   []string{"/container/path"},
		},
		{
			name:        "multiple paths",
			fileContent: "0::/kubepods/pod/container\n11:memory:/legacy/path\n",
			wantPaths:   []string{"/kubepods/pod/container", "/legacy/path"},
		},
		{
			name:    "missing pid directory returns error",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()

			if !tc.wantErr {
				pidDir := filepath.Join(root, "123")
				if err := os.MkdirAll(pidDir, 0o755); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
				if err := os.WriteFile(filepath.Join(pidDir, "cgroup"), []byte(tc.fileContent), 0o644); err != nil {
					t.Fatalf("WriteFile: %v", err)
				}
			}

			r := NewProcCgroupResolver(root)
			paths, err := r.CgroupPaths(pid)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertPaths(t, paths, tc.wantPaths)
		})
	}
}
