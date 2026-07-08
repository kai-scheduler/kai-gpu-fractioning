package store

import "testing"

func TestContainerStoreMatchesLongerProcessCgroupPathByInclusion(t *testing.T) {
	store := NewContainerStore()
	store.Upsert(ContainerInfo{
		ContainerID: "container-id",
		Container:   "container",
		Pod:         "pod",
		Namespace:   "default",
		CgroupPath:  "/kubepods.slice/kubepods-burstable.slice/pod123.slice/cri-containerd-container-id.scope",
	})

	matched, ok := store.MatchCgroupPaths([]string{
		"/kubepods.slice/kubepods-burstable.slice/pod123.slice/cri-containerd-container-id.scope/some/longer/path",
	})
	if !ok {
		t.Fatalf("expected longer process cgroup path to match stored container cgroup path")
	}
	if matched.ContainerID != "container-id" {
		t.Fatalf("expected container-id, got %q", matched.ContainerID)
	}
}

func TestContainerStoreMatchesProcessCgroupPathByContainerID(t *testing.T) {
	store := NewContainerStore()
	store.Upsert(ContainerInfo{
		ContainerID: "containerd://0123456789abcdef",
		Container:   "container",
		Pod:         "pod",
		Namespace:   "default",
		CgroupPath:  "kubepods-burstable-pod123.slice:cri-containerd:0123456789abcdef",
	})

	matched, ok := store.MatchCgroupPaths([]string{
		"/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod123.slice/cri-containerd-0123456789abcdef.scope",
	})
	if !ok {
		t.Fatalf("expected process cgroup path to match stored container id")
	}
	if matched.ContainerID != "containerd://0123456789abcdef" {
		t.Fatalf("expected containerd://0123456789abcdef, got %q", matched.ContainerID)
	}
}

func TestContainerStoreActiveContainersReturnsStoredContainers(t *testing.T) {
	store := NewContainerStore()
	store.Upsert(ContainerInfo{ContainerID: "container-a"})
	store.Upsert(ContainerInfo{ContainerID: "container-b"})

	containers := store.ActiveContainers()
	if len(containers) != 2 {
		t.Fatalf("expected two active containers, got %d", len(containers))
	}
}

func TestContainerStoreDeleteMatchesNormalizedContainerID(t *testing.T) {
	store := NewContainerStore()
	store.Upsert(ContainerInfo{
		ContainerID: "containerd://0123456789abcdef",
		PodUID:      "pod-uid",
	})

	store.Delete("0123456789abcdef")

	if pods := store.ActivePodUIDs(); len(pods) != 0 {
		t.Fatalf("expected pod to be removed after normalized container delete, got %#v", pods)
	}
}

func TestContainerStoreActivePodUIDsDropsPodAfterLastContainerDelete(t *testing.T) {
	store := NewContainerStore()
	store.Upsert(ContainerInfo{ContainerID: "container-a", PodUID: "pod-uid"})
	store.Upsert(ContainerInfo{ContainerID: "container-b", PodUID: "pod-uid"})

	store.Delete("container-a")
	if pods := store.ActivePodUIDs(); len(pods) != 1 {
		t.Fatalf("expected pod to stay active while one container remains, got %#v", pods)
	}

	store.Delete("container-b")
	if pods := store.ActivePodUIDs(); len(pods) != 0 {
		t.Fatalf("expected pod to be removed after last container delete, got %#v", pods)
	}
}
