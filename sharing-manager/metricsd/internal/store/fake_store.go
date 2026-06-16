package store

import "strings"

// FakeStore is a test double for Reader backed by a static slice of ContainerInfo.
type FakeStore struct {
	Containers []ContainerInfo
}

func (s FakeStore) MatchCgroupPaths(paths []string) (ContainerInfo, bool) {
	for _, path := range paths {
		for _, container := range s.Containers {
			if container.CgroupPath != "" && strings.Contains(path, container.CgroupPath) {
				return container, true
			}
		}
	}
	return ContainerInfo{}, false
}

func (s FakeStore) ActivePodUIDs() map[string]struct{} {
	out := make(map[string]struct{}, len(s.Containers))
	for _, container := range s.Containers {
		if container.PodUID != "" {
			out[container.PodUID] = struct{}{}
		}
	}
	return out
}

func (s FakeStore) ActiveContainers() []ContainerInfo {
	return append([]ContainerInfo(nil), s.Containers...)
}
