//go:build darwin
// +build darwin

package process

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	darwinTotalPhysRAM uint64
	darwinSampler      = NewMemorySampler(300*time.Millisecond, fetchDarwinSystemMemory)
)

func getSystemMemory() (MemoryInfo, error) {
	return darwinSampler.Get()
}

func fetchDarwinSystemMemory() (MemoryInfo, error) {
	totalBytes := darwinTotalPhysRAM
	if totalBytes == 0 {
		// 1. Try sysctl command
		out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
		if err == nil {
			if val, parseErr := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64); parseErr == nil && val > 0 {
				totalBytes = val
			}
		}

		// 2. Fallback to syscall.Sysctl if command fails
		if totalBytes == 0 {
			if sVal, sErr := syscall.Sysctl("hw.memsize"); sErr == nil {
				if val, parseErr := strconv.ParseUint(strings.TrimSpace(sVal), 10, 64); parseErr == nil && val > 0 {
					totalBytes = val
				}
			}
		}

		if totalBytes == 0 {
			return MemoryInfo{}, fmt.Errorf("failed to query hw.memsize via sysctl")
		}
		darwinTotalPhysRAM = totalBytes
	}

	vmOut, vmErr := exec.Command("vm_stat").Output()
	if vmErr != nil {
		// Fallback to conservative estimate (50% of total) if vm_stat fails
		return MemoryInfo{
			TotalBytes:     totalBytes,
			AvailableBytes: totalBytes / 2,
			Estimated:      true,
		}, nil
	}

	return ParseDarwinVMStat(vmOut, totalBytes)
}
