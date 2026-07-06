package store

import (
	"strings"
	"sync"
)

type GPUDevice struct {
	Index int `json:"index"`
	// UUID is the GPU/MIG UUID as reported by the kubelet PodResources API. The
	// production NRI path leaves it empty and identifies devices by Index; it is
	// populated by the PodResources metrics backend, and by the fake-GPU (e2e)
	// detector when it reads a UUID from MOCK_NVIDIA_VISIBLE_DEVICES.
	UUID string `json:"uuid,omitempty"`
}

type ContainerInfo struct {
	ContainerID string
	Container   string
	Pod         string
	Namespace   string
	PodUID      string
	CgroupPath  string
	GPUDevices  []GPUDevice
	// RequestedGPUFraction is the GPU fraction the container requested (e.g. 0.5),
	// read from the configured GPU-fraction annotation. It is the divisor used to
	// normalize SM utilization. Zero when the annotation is absent or unparseable.
	RequestedGPUFraction float64
}

// Writer is the write side of the container→pod mapping storage, used by the
// component that ingests runtime events (the NRI mapper). Implementations must
// be safe for concurrent use.
type Writer interface {
	Upsert(container ContainerInfo)
	Delete(containerID string)
	Replace(containers []ContainerInfo)
}

// Reader is the read side of the mapping storage, used by the metrics component
// to attribute GPU processes to pods. Implementations must be safe for
// concurrent use.
type Reader interface {
	MatchCgroupPaths(paths []string) (ContainerInfo, bool)
	ActivePodUIDs() map[string]struct{}
	ActiveContainers() []ContainerInfo
}

// ContainerStore is the in-memory implementation of both Writer and Reader. A
// filesystem-backed implementation (for the split NRI-mapper / metrics
// deployment) can satisfy the same interfaces.
var (
	_ Writer = (*ContainerStore)(nil)
	_ Reader = (*ContainerStore)(nil)
)

type ContainerStore struct {
	mu         sync.RWMutex
	containers map[string]ContainerInfo
}

func NewContainerStore() *ContainerStore {
	return &ContainerStore{containers: map[string]ContainerInfo{}}
}

func (s *ContainerStore) Replace(containers []ContainerInfo) {
	next := make(map[string]ContainerInfo, len(containers))
	for _, container := range containers {
		if container.ContainerID == "" {
			continue
		}
		next[container.ContainerID] = container
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers = next
}

func (s *ContainerStore) Upsert(container ContainerInfo) {
	if container.ContainerID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.containers[container.ContainerID] = container
}

func (s *ContainerStore) Delete(containerID string) {
	if containerID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.containers, containerID)

	normalized := normalizeContainerID(containerID)
	if normalized == "" {
		return
	}
	for storedID := range s.containers {
		if normalizeContainerID(storedID) == normalized {
			delete(s.containers, storedID)
		}
	}
}

func (s *ContainerStore) MatchCgroupPaths(paths []string) (ContainerInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, path := range paths {
		for _, container := range s.containers {
			if cgroupPathMatchesContainer(path, container) {
				return container, true
			}
		}
	}
	return ContainerInfo{}, false
}

func (s *ContainerStore) ActivePodUIDs() map[string]struct{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]struct{}, len(s.containers))
	for _, container := range s.containers {
		if container.PodUID != "" {
			out[container.PodUID] = struct{}{}
		}
	}
	return out
}

func (s *ContainerStore) ActiveContainers() []ContainerInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]ContainerInfo, 0, len(s.containers))
	for _, container := range s.containers {
		out = append(out, container)
	}
	return out
}

func cgroupPathMatchesContainer(processPath string, container ContainerInfo) bool {
	if cgroupPathContains(processPath, container.CgroupPath) {
		return true
	}
	return cgroupPathContainsContainerID(processPath, container.ContainerID)
}

func cgroupPathContains(processPath, containerPath string) bool {
	processPath = normalizeCgroupPath(processPath)
	containerPath = normalizeCgroupPath(containerPath)
	if processPath == "" || containerPath == "" || containerPath == "/" {
		return false
	}
	return processPath == containerPath || strings.HasPrefix(processPath, containerPath+"/")
}

func cgroupPathContainsContainerID(processPath, containerID string) bool {
	processPath = normalizeCgroupPath(processPath)
	containerID = normalizeContainerID(containerID)
	if processPath == "" || containerID == "" {
		return false
	}
	return strings.Contains(processPath, containerID)
}

func normalizeContainerID(containerID string) string {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return ""
	}
	if idx := strings.LastIndex(containerID, "://"); idx >= 0 {
		containerID = containerID[idx+3:]
	}
	return strings.Trim(containerID, "/")
}

func normalizeCgroupPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	if path != "/" {
		path = strings.TrimRight(path, "/")
	}
	return path
}
