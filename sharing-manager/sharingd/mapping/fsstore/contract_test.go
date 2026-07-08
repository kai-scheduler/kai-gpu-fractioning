package fsstore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/run-ai/gpu-sharing-operator/sharing-manager/sharingd/mapping/store"
)

// goldenRecordV1 is the frozen on-disk wire contract for the container→pod
// mapping handoff (schemaVersion 1). sharingd (this module) writes it; the
// metricsd sidecar reads it from a separate Go module. The identical literal is
// asserted by metricsd's own contract test — if either side changes the schema
// without bumping schemaVersion in lockstep, one of the two tests breaks.
//
// Keep this byte-for-byte in sync with the metricsd contract test.
const goldenRecordV1 = `{"schemaVersion":1,"pod":"trainer-7d9f","namespace":"team-ml","podUID":"3f2a-uid","gpuDevices":[{"index":0},{"index":3}],"requestedGpuFraction":0.5}`

// TestWriterProducesFrozenSchema asserts the Writer emits exactly the frozen v1
// wire format for a representative container mapping.
func TestWriterProducesFrozenSchema(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, nil)

	w.Upsert(store.ContainerInfo{
		ContainerID:          "abc",
		Container:            "trainer", // intentionally not persisted
		CgroupPath:           "/not/persisted",
		Pod:                  "trainer-7d9f",
		Namespace:            "team-ml",
		PodUID:               "3f2a-uid",
		GPUDevices:           []store.GPUDevice{{Index: 0}, {Index: 3}},
		RequestedGPUFraction: 0.5,
	})

	data, err := os.ReadFile(filepath.Join(dir, "abc.json"))
	if err != nil {
		t.Fatalf("read mapping file: %v", err)
	}
	if got := string(data); got != goldenRecordV1 {
		t.Fatalf("writer schema drifted from the frozen v1 contract\n got: %s\nwant: %s", got, goldenRecordV1)
	}
}
