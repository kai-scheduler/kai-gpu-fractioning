package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCgroupPaths(t *testing.T) {
	paths := parseCgroupPaths("0::/kubepods.slice/pod.slice/container.scope\n11:memory:/legacy/path\n")
	if len(paths) != 2 {
		t.Fatalf("expected two paths, got %d", len(paths))
	}
	if paths[0] != "/kubepods.slice/pod.slice/container.scope" {
		t.Fatalf("expected cgroup v2 path, got %q", paths[0])
	}
	if paths[1] != "/legacy/path" {
		t.Fatalf("expected legacy path, got %q", paths[1])
	}
}

func TestProcCgroupResolverReadsProcRoot(t *testing.T) {
	root := t.TempDir()
	pidDir := filepath.Join(root, "123")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "cgroup"), []byte("0::/container/path\n"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	paths, err := ProcCgroupResolver{ProcRoot: root}.CgroupPaths(123)
	if err != nil {
		t.Fatalf("CgroupPaths returned error: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/container/path" {
		t.Fatalf("unexpected paths: %#v", paths)
	}
}
