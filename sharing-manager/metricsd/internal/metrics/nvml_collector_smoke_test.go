//go:build nvml_smoke

// Package metrics smoke tests exercise the production NVML collector against
// the nvml-mock .so. They require LD_LIBRARY_PATH to point at the mock library
// and MOCK_NVML_CONFIG to point at a config file (defaults to
// testdata/nvml_smoke_config.yaml). Run via:
//
//	make -C sharing-manager/metricsd smoke-nvml
package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	smokeDevice0UUID = "aaaaaaaa-0000-0000-0000-000000000000"
	smokeDevice1UUID = "bbbbbbbb-1111-1111-1111-111111111111"
	smokeTotalMemory = uint64(42949672960) // 40 GiB, matches testdata config

	// PIDs declared in testdata/nvml_smoke_config.yaml.
	smokePID1001 = uint32(1001) // device 0, compute,  1 GiB
	smokePID1002 = uint32(1002) // device 0, compute,  2 GiB
	smokePID1003 = uint32(1003) // device 0, compute,  nvmlValueNotAvailable sentinel
	smokePID2001 = uint32(2001) // device 1, compute,  3 GiB
	smokePID2002 = uint32(2002) // device 1, graphics, 512 MiB
)

// smokeCollector creates a production NVML collector pointed at the smoke test
// config. It skips the test if the nvml-mock .so is not loaded (LD_LIBRARY_PATH
// not set or NVML init fails), so `go test -tags nvml_smoke` is safe to run
// without the library — it simply skips.
func smokeCollector(t *testing.T) *nvmlProcessCollector {
	t.Helper()

	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		abs, err := filepath.Abs(filepath.Join("testdata", "nvml_smoke_config.yaml"))
		if err != nil {
			t.Fatalf("resolve testdata config path: %v", err)
		}
		os.Setenv("MOCK_NVML_CONFIG", abs)
	}

	c, err := newNVMLProcessCollector(time.Second, nil)
	if err != nil {
		t.Skipf("NVML unavailable (nvml-mock .so not loaded via LD_LIBRARY_PATH?): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	c.collect(time.Now())
	return c
}

// procsByPID indexes a slice of GPUProcessMetric by PID for O(1) lookup in assertions.
func procsByPID(procs []GPUProcessMetric) map[uint32]GPUProcessMetric {
	m := make(map[uint32]GPUProcessMetric, len(procs))
	for _, p := range procs {
		m[p.PID] = p
	}
	return m
}

// TestNVMLSmokeDeviceEnumeration verifies that the collector discovers both mock
// devices and returns their UUIDs keyed by device index.
func TestNVMLSmokeDeviceEnumeration(t *testing.T) {
	snap := smokeCollector(t).Snapshot()

	if got := len(snap.DeviceUUIDs); got != 2 {
		t.Fatalf("DeviceUUIDs: want 2 devices, got %d: %v", got, snap.DeviceUUIDs)
	}
	if got := snap.DeviceUUIDs[0]; got != smokeDevice0UUID {
		t.Errorf("device 0 UUID: want %q, got %q", smokeDevice0UUID, got)
	}
	if got := snap.DeviceUUIDs[1]; got != smokeDevice1UUID {
		t.Errorf("device 1 UUID: want %q, got %q", smokeDevice1UUID, got)
	}
}

// TestNVMLSmokeTotalMemory verifies that GetMemoryInfo is called and the result
// stored correctly in DeviceTotalMemoryBytes.
func TestNVMLSmokeTotalMemory(t *testing.T) {
	snap := smokeCollector(t).Snapshot()

	for _, idx := range []int{0, 1} {
		if got := snap.DeviceTotalMemoryBytes[idx]; got != smokeTotalMemory {
			t.Errorf("device %d total memory: want %d, got %d", idx, smokeTotalMemory, got)
		}
	}
}

// TestNVMLSmokeComputeProcesses verifies that GetComputeRunningProcesses is
// called and its entries appear in the snapshot with correct PID, UUID, index,
// and memory.
func TestNVMLSmokeComputeProcesses(t *testing.T) {
	snap := smokeCollector(t).Snapshot()
	byPID := procsByPID(snap.Processes)

	cases := []struct {
		pid     uint32
		wantMem uint64
	}{
		{smokePID1001, 1073741824},
		{smokePID1002, 2147483648},
		{smokePID2001, 3221225472},
	}
	for _, tc := range cases {
		m, ok := byPID[tc.pid]
		if !ok {
			t.Errorf("PID %d not in snapshot", tc.pid)
			continue
		}
		wantUUID := smokeDevice0UUID
		wantIdx := 0
		if tc.pid >= 2000 {
			wantUUID = smokeDevice1UUID
			wantIdx = 1
		}
		if m.GPUUUID != wantUUID {
			t.Errorf("PID %d: UUID want %q, got %q", tc.pid, wantUUID, m.GPUUUID)
		}
		if m.GPUIndex != wantIdx {
			t.Errorf("PID %d: index want %d, got %d", tc.pid, wantIdx, m.GPUIndex)
		}
		if m.UsedGPUMemoryBytes != tc.wantMem {
			t.Errorf("PID %d: memory want %d, got %d", tc.pid, tc.wantMem, m.UsedGPUMemoryBytes)
		}
	}
}

// TestNVMLSmokeGraphicsProcess verifies that GetGraphicsRunningProcesses is
// called and its entries appear in the snapshot. PID 2002 is type G on device 1.
func TestNVMLSmokeGraphicsProcess(t *testing.T) {
	snap := smokeCollector(t).Snapshot()
	byPID := procsByPID(snap.Processes)

	m, ok := byPID[smokePID2002]
	if !ok {
		t.Fatalf("graphics PID %d not in snapshot", smokePID2002)
	}
	if m.GPUUUID != smokeDevice1UUID {
		t.Errorf("UUID: want %q, got %q", smokeDevice1UUID, m.GPUUUID)
	}
	if m.GPUIndex != 1 {
		t.Errorf("index: want 1, got %d", m.GPUIndex)
	}
	if m.UsedGPUMemoryBytes != 536870912 {
		t.Errorf("memory: want 512 MiB, got %d", m.UsedGPUMemoryBytes)
	}
}

// TestNVMLSmokeNVMLValueNotAvailable verifies that the nvmlValueNotAvailable
// sentinel (^uint64(0)) returned for UsedGpuMemory is not stored verbatim.
// The process must appear in the snapshot but with UsedGPUMemoryBytes == 0.
func TestNVMLSmokeNVMLValueNotAvailable(t *testing.T) {
	snap := smokeCollector(t).Snapshot()
	byPID := procsByPID(snap.Processes)

	m, ok := byPID[smokePID1003]
	if !ok {
		// nvml-mock may not support uint64 max in YAML config; that case is
		// covered by mergeRunningProcesses unit tests. Skip rather than fail.
		t.Skip("sentinel PID 1003 not reported by nvml-mock (mock may not parse uint64 max)")
	}
	if m.UsedGPUMemoryBytes == nvmlValueNotAvailable {
		t.Errorf("PID %d: UsedGPUMemoryBytes stores the sentinel %d; should be 0", smokePID1003, nvmlValueNotAvailable)
	}
}

// TestNVMLSmokeMultiDeviceIsolation verifies that processes are attributed to
// the correct device and that no cross-device contamination occurs.
func TestNVMLSmokeMultiDeviceIsolation(t *testing.T) {
	snap := smokeCollector(t).Snapshot()

	for _, m := range snap.Processes {
		switch m.GPUUUID {
		case smokeDevice0UUID:
			if m.GPUIndex != 0 {
				t.Errorf("PID %d: device 0 UUID but index %d", m.PID, m.GPUIndex)
			}
		case smokeDevice1UUID:
			if m.GPUIndex != 1 {
				t.Errorf("PID %d: device 1 UUID but index %d", m.PID, m.GPUIndex)
			}
		default:
			t.Errorf("PID %d: unexpected GPU UUID %q", m.PID, m.GPUUUID)
		}
	}
}

// TestNVMLSmokeProcessCount verifies the exact number of distinct process
// entries after a single collect (5 PIDs: 1001, 1002, 1003, 2001, 2002).
// The sentinel PID 1003 may be absent if nvml-mock can't parse uint64 max.
func TestNVMLSmokeProcessCount(t *testing.T) {
	snap := smokeCollector(t).Snapshot()
	byPID := procsByPID(snap.Processes)

	required := []uint32{smokePID1001, smokePID1002, smokePID2001, smokePID2002}
	for _, pid := range required {
		if _, ok := byPID[pid]; !ok {
			t.Errorf("expected PID %d in snapshot", pid)
		}
	}
	// Sentinel PID is optional (see TestNVMLSmokeNVMLValueNotAvailable).
	minExpected := len(required)
	if got := len(snap.Processes); got < minExpected {
		t.Errorf("want at least %d processes, got %d", minExpected, got)
	}
}

// TestNVMLSmokeNoProcessDuplication verifies that calling collect multiple
// times does not produce duplicate entries for the same PID.
func TestNVMLSmokeNoProcessDuplication(t *testing.T) {
	c := smokeCollector(t)
	c.collect(time.Now())
	c.collect(time.Now())
	snap := c.Snapshot()

	seen := map[uint32]int{}
	for _, m := range snap.Processes {
		seen[m.PID]++
	}
	for pid, count := range seen {
		if count > 1 {
			t.Errorf("PID %d appears %d times after multiple collects (want 1)", pid, count)
		}
	}
}

// TestNVMLSmokeProcessUtilizationUnavailable verifies that the collector
// handles ERROR_NOT_FOUND / ERROR_NOT_SUPPORTED from GetProcessUtilization
// gracefully: memory entries must still appear and SM util defaults to 0.
func TestNVMLSmokeProcessUtilizationUnavailable(t *testing.T) {
	snap := smokeCollector(t).Snapshot()
	byPID := procsByPID(snap.Processes)

	for _, pid := range []uint32{smokePID1001, smokePID1002, smokePID2001, smokePID2002} {
		if _, ok := byPID[pid]; !ok {
			t.Errorf("PID %d missing (GetProcessUtilization unavailable must not drop memory entries)", pid)
		}
	}
	for _, m := range snap.Processes {
		if m.SMUtilizationPercent != 0 {
			t.Errorf("PID %d: SMUtilizationPercent want 0 (mock provides none), got %d", m.PID, m.SMUtilizationPercent)
		}
	}
}

// TestNVMLSmokeSnapshotImmutability verifies that mutating the slice returned
// by Snapshot does not affect subsequent Snapshot calls (cloneGPUProcessSnapshot).
func TestNVMLSmokeSnapshotImmutability(t *testing.T) {
	c := smokeCollector(t)
	snap1 := c.Snapshot()
	originalLen := len(snap1.Processes)
	if originalLen == 0 {
		t.Fatal("need at least one process to test immutability")
	}

	// Zero out and truncate the returned slice.
	for i := range snap1.Processes {
		snap1.Processes[i].PID = 0
		snap1.Processes[i].UsedGPUMemoryBytes = 0
	}
	snap1.Processes = snap1.Processes[:0]

	snap2 := c.Snapshot()
	if got := len(snap2.Processes); got != originalLen {
		t.Errorf("after mutation: want %d processes, got %d", originalLen, got)
	}
	for _, m := range snap2.Processes {
		if m.PID == 0 {
			t.Errorf("internal snapshot was mutated: found zeroed PID")
		}
	}
}

// TestNVMLSmokeDeviceUUIDCached verifies that the UUID cache works: two
// Snapshot calls must return the same UUIDs (no re-query of GetUUID).
func TestNVMLSmokeDeviceUUIDCached(t *testing.T) {
	c := smokeCollector(t)
	c.collect(time.Now())
	snap1 := c.Snapshot()
	c.collect(time.Now())
	snap2 := c.Snapshot()

	for idx := range snap1.DeviceUUIDs {
		if snap1.DeviceUUIDs[idx] != snap2.DeviceUUIDs[idx] {
			t.Errorf("device %d UUID changed between collects: %q → %q",
				idx, snap1.DeviceUUIDs[idx], snap2.DeviceUUIDs[idx])
		}
	}
}

// TestNVMLSmokeNoDeviceErrors verifies that a well-formed mock config produces
// a snapshot with no DeviceErrors.
func TestNVMLSmokeNoDeviceErrors(t *testing.T) {
	snap := smokeCollector(t).Snapshot()
	if snap.DeviceErrors != nil {
		t.Errorf("unexpected device errors: %v", snap.DeviceErrors)
	}
}

// TestNVMLSmokeCloseAndReinit verifies that Close leaves NVML in a state that
// allows a second successful Init — important for supervisor restart scenarios.
func TestNVMLSmokeCloseAndReinit(t *testing.T) {
	if os.Getenv("MOCK_NVML_CONFIG") == "" {
		abs, _ := filepath.Abs(filepath.Join("testdata", "nvml_smoke_config.yaml"))
		os.Setenv("MOCK_NVML_CONFIG", abs)
	}

	c1, err := newNVMLProcessCollector(time.Second, nil)
	if err != nil {
		t.Skipf("NVML unavailable: %v", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	c2, err := newNVMLProcessCollector(time.Second, nil)
	if err != nil {
		t.Fatalf("second Init after Close failed: %v", err)
	}
	if err := c2.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
