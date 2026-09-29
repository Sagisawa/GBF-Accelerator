//go:build linux
// +build linux

package process

import (
	"fmt"
	"os"
)

func getSystemMemory() (MemoryInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("failed to open /proc/meminfo: %w", err)
	}
	defer f.Close()

	info, err := ParseLinuxMeminfo(f)
	if err != nil {
		return MemoryInfo{}, err
	}

	// Optional check for cgroup v2 / v1 memory limits in container environments
	cgroupMax, errMax := os.ReadFile("/sys/fs/cgroup/memory.max")
	cgroupCur, errCur := os.ReadFile("/sys/fs/cgroup/memory.current")
	if errMax == nil && errCur == nil {
		tot, av := ParseLinuxCgroups(cgroupMax, cgroupCur, info.TotalBytes, info.AvailableBytes)
		info.TotalBytes = tot
		info.AvailableBytes = av
	}

	return info, nil
}
