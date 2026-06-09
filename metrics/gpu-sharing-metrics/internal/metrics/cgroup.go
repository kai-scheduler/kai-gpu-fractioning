package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultProcRoot = "/proc"

type PIDCgroupResolver interface {
	CgroupPaths(pid uint32) ([]string, error)
}

type ProcCgroupResolver struct {
	ProcRoot string
}

func (r ProcCgroupResolver) CgroupPaths(pid uint32) ([]string, error) {
	procRoot := strings.TrimSpace(r.ProcRoot)
	if procRoot == "" {
		procRoot = DefaultProcRoot
	}

	path := filepath.Join(procRoot, strconv.FormatUint(uint64(pid), 10), "cgroup")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pid %d cgroup file %q: %w", pid, path, err)
	}
	return parseCgroupPaths(string(data)), nil
}

func parseCgroupPaths(data string) []string {
	var paths []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		path := strings.TrimSpace(parts[2])
		if path == "" {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}
