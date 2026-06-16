package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultProcRoot = "/proc"
	cgroupFile      = "cgroup"
)

// PIDCgroupResolver resolves the cgroup paths for a given process ID.
type PIDCgroupResolver interface {
	CgroupPaths(pid uint32) ([]string, error)
}

// ProcCgroupResolver reads cgroup paths from the Linux proc filesystem.
type ProcCgroupResolver struct {
	ProcRoot string
}

// NewProcCgroupResolver returns a ProcCgroupResolver with procRoot trimmed and
// defaulted to /proc if empty.
func NewProcCgroupResolver(procRoot string) ProcCgroupResolver {
	procRoot = strings.TrimSpace(procRoot)
	if procRoot == "" {
		procRoot = defaultProcRoot
	}
	return ProcCgroupResolver{ProcRoot: procRoot}
}

func (r ProcCgroupResolver) CgroupPaths(pid uint32) ([]string, error) {
	path := filepath.Join(r.ProcRoot, strconv.FormatUint(uint64(pid), 10), cgroupFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read pid %d cgroup file %q: %w", pid, path, err)
	}
	return parseCgroupPaths(string(data)), nil
}

// parseCgroupPaths parses the content of a Linux /proc/<pid>/cgroup file and
// returns the list of cgroup paths (one per hierarchy).
//
// Each line has the form:
//
//	<hierarchy-id>:<controller-list>:<cgroup-path>
//
// For example:
//
//	0::/kubepods/burstable/podabc/container123   (cgroup v2, empty controller list)
//	11:memory:/kubepods/burstable/podabc/c123     (cgroup v1, named controller)
//
// The function returns the third field (the path) for each valid line.
// Lines with fewer than three colon-delimited fields, or whose path field is
// empty, are silently skipped: the kernel writes well-formed output under normal
// conditions, so skipping is safe and avoids turning a partial read into a hard
// failure for the caller.
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
