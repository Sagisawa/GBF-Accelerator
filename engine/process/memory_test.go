package process

import (
	"strings"
	"testing"
)

func TestGetSystemMemory(t *testing.T) {
	mem, err := GetSystemMemory()
	if err != nil {
		t.Fatalf("GetSystemMemory failed: %v", err)
	}

	if mem.TotalBytes == 0 {
		t.Errorf("expected TotalBytes > 0, got 0")
	}

	if mem.AvailableBytes == 0 {
		t.Errorf("expected AvailableBytes > 0, got 0")
	}

	if mem.AvailableBytes > mem.TotalBytes {
		t.Errorf("AvailableBytes (%d) cannot exceed TotalBytes (%d)", mem.AvailableBytes, mem.TotalBytes)
	}

	t.Logf("Detected memory: Total=%d MB, Available=%d MB, Estimated=%v",
		mem.TotalBytes/(1024*1024), mem.AvailableBytes/(1024*1024), mem.Estimated)
}

func TestParseLinuxMeminfo_Standard(t *testing.T) {
	fixture := `MemTotal:       32649068 kB
MemFree:         4123456 kB
MemAvailable:   24567890 kB
Buffers:          345678 kB
Cached:         20123456 kB
SwapCached:            0 kB
`
	info, err := ParseLinuxMeminfo(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ParseLinuxMeminfo failed: %v", err)
	}

	expectedTotal := uint64(32649068) * 1024
	expectedAvail := uint64(24567890) * 1024

	if info.TotalBytes != expectedTotal {
		t.Errorf("expected TotalBytes %d, got %d", expectedTotal, info.TotalBytes)
	}
	if info.AvailableBytes != expectedAvail {
		t.Errorf("expected AvailableBytes %d, got %d", expectedAvail, info.AvailableBytes)
	}
	if info.Estimated {
		t.Errorf("expected Estimated=false when MemAvailable is present")
	}
}

func TestParseLinuxMeminfo_Fallback(t *testing.T) {
	fixture := `MemTotal:       16384000 kB
MemFree:         2000000 kB
Buffers:         1000000 kB
Cached:          5000000 kB
`
	info, err := ParseLinuxMeminfo(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("ParseLinuxMeminfo failed: %v", err)
	}

	expectedTotal := uint64(16384000) * 1024
	expectedAvail := uint64(8000000) * 1024 // 2000000 + 1000000 + 5000000

	if info.TotalBytes != expectedTotal {
		t.Errorf("expected TotalBytes %d, got %d", expectedTotal, info.TotalBytes)
	}
	if info.AvailableBytes != expectedAvail {
		t.Errorf("expected AvailableBytes %d, got %d", expectedAvail, info.AvailableBytes)
	}
	if !info.Estimated {
		t.Errorf("expected Estimated=true when MemAvailable is missing")
	}
}

func TestParseLinuxCgroups(t *testing.T) {
	hostTotal := uint64(64 * 1024 * 1024 * 1024) // 64 GB host
	hostAvail := uint64(32 * 1024 * 1024 * 1024) // 32 GB host avail

	// Container capped at 4 GB with 1 GB used
	maxBytes := []byte("4294967296\n")
	currBytes := []byte("1073741824\n")

	tot, av := ParseLinuxCgroups(maxBytes, currBytes, hostTotal, hostAvail)
	if tot != 4*1024*1024*1024 {
		t.Errorf("expected cgroup total 4GB, got %d", tot)
	}
	if av != 3*1024*1024*1024 {
		t.Errorf("expected cgroup avail 3GB, got %d", av)
	}

	// When cgroup limit is "max", host values should remain unchanged
	tot2, av2 := ParseLinuxCgroups([]byte("max\n"), currBytes, hostTotal, hostAvail)
	if tot2 != hostTotal || av2 != hostAvail {
		t.Errorf("expected unconstrained max to keep host values, got (%d, %d)", tot2, av2)
	}
}

func TestParseDarwinVMStat_AppleSilicon(t *testing.T) {
	fixture := `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               10000.
Pages active:                            500000.
Pages inactive:                          100000.
Pages speculative:                        10000.
Pages throttled:                              0.
Pages wired down:                        200000.
Pages purgeable:                          30000.
`
	totalBytes := uint64(16 * 1024 * 1024 * 1024) // 16 GB
	info, err := ParseDarwinVMStat([]byte(fixture), totalBytes)
	if err != nil {
		t.Fatalf("ParseDarwinVMStat failed: %v", err)
	}

	// 10000 + 100000 + 10000 = 120000 pages * 16384 bytes = 1,966,080,000 bytes
	expectedAvail := uint64(120000) * 16384

	if info.TotalBytes != totalBytes {
		t.Errorf("expected TotalBytes %d, got %d", totalBytes, info.TotalBytes)
	}
	if info.AvailableBytes != expectedAvail {
		t.Errorf("expected AvailableBytes %d, got %d", expectedAvail, info.AvailableBytes)
	}
	if !info.Estimated {
		t.Errorf("expected Estimated=true on Darwin")
	}
}

func TestParseDarwinVMStat_Intel(t *testing.T) {
	fixture := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Pages free:                               20000.
Pages active:                            200000.
Pages inactive:                           50000.
Pages speculative:                        10000.
`
	totalBytes := uint64(8 * 1024 * 1024 * 1024) // 8 GB
	info, err := ParseDarwinVMStat([]byte(fixture), totalBytes)
	if err != nil {
		t.Fatalf("ParseDarwinVMStat failed: %v", err)
	}

	// 20000 + 50000 + 10000 = 80000 pages * 4096 bytes = 327,680,000 bytes
	expectedAvail := uint64(80000) * 4096

	if info.TotalBytes != totalBytes {
		t.Errorf("expected TotalBytes %d, got %d", totalBytes, info.TotalBytes)
	}
	if info.AvailableBytes != expectedAvail {
		t.Errorf("expected AvailableBytes %d, got %d", expectedAvail, info.AvailableBytes)
	}
}
