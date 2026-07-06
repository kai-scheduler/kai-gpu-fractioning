package fsstore

import (
	"os"
	"path/filepath"
	"testing"
)

// goldenRecordV1 is the frozen on-disk wire contract for the container→pod
// mapping handoff (schemaVersion 1), produced by the sharingd NRI plugin in a
// separate Go module. This test asserts metricsd's Reader parses it into the
// expected ContainerInfo. The identical literal is asserted by sharingd's writer
// contract test — if either side changes the schema without bumping
// schemaVersion in lockstep, one of the two tests breaks.
//
// Keep this byte-for-byte in sync with the sharingd contract test.
const goldenRecordV1 = `{"schemaVersion":1,"pod":"trainer-7d9f","namespace":"team-ml","podUID":"3f2a-uid","gpuDevices":[{"index":0},{"index":3}],"requestedGpuFraction":0.5}`

// TestReaderParsesFrozenSchema asserts the Reader loads a record written by
// sharingd in the frozen v1 wire format.
func TestReaderParsesFrozenSchema(t *testing.T) {
	dir := t.TempDir()
	// The container ID is the file stem, not a field in the record.
	if err := os.WriteFile(filepath.Join(dir, "abc.json"), []byte(goldenRecordV1), 0o644); err != nil {
		t.Fatalf("write golden record: %v", err)
	}

	got := NewReader(dir, nil).ActiveContainers()
	if len(got) != 1 {
		t.Fatalf("expected one container from the golden record, got %d", len(got))
	}
	info := got[0]
	if info.ContainerID != "abc" {
		t.Fatalf("expected container ID recovered from file name, got %q", info.ContainerID)
	}
	if info.Pod != "trainer-7d9f" || info.Namespace != "team-ml" || info.PodUID != "3f2a-uid" {
		t.Fatalf("unexpected pod identity: %#v", info)
	}
	if len(info.GPUDevices) != 2 || info.GPUDevices[0].Index != 0 || info.GPUDevices[1].Index != 3 {
		t.Fatalf("unexpected GPU devices: %#v", info.GPUDevices)
	}
	if info.RequestedGPUFraction != 0.5 {
		t.Fatalf("expected RequestedGPUFraction 0.5, got %g", info.RequestedGPUFraction)
	}
}
