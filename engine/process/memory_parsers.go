package process

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var (
	reDarwinPageSize = regexp.MustCompile(`page size of (\d+) bytes`)
)

// ParseLinuxMeminfo parses lines from /proc/meminfo and calculates total and available memory.
func ParseLinuxMeminfo(r io.Reader) (MemoryInfo, error) {
	var (
		totalKB   uint64
		availKB   uint64
		freeKB    uint64
		buffersKB uint64
		cachedKB  uint64
		hasAvail  bool
	)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		val, parseErr := strconv.ParseUint(parts[1], 10, 64)
		if parseErr != nil {
			continue
		}

		switch key {
		case "MemTotal":
			totalKB = val
		case "MemAvailable":
			availKB = val
			hasAvail = true
		case "MemFree":
			freeKB = val
		case "Buffers":
			buffersKB = val
		case "Cached":
			cachedKB = val
		}
	}

	if err := scanner.Err(); err != nil {
		return MemoryInfo{}, fmt.Errorf("error reading meminfo: %w", err)
	}

	if totalKB == 0 {
		return MemoryInfo{}, fmt.Errorf("unable to determine MemTotal from meminfo")
	}

	if !hasAvail {
		availKB = freeKB + buffersKB + cachedKB
	}

	return MemoryInfo{
		TotalBytes:     totalKB * 1024,
		AvailableBytes: availKB * 1024,
		Estimated:      !hasAvail,
	}, nil
}

// ParseLinuxCgroups adjusts total and available memory based on container limits (cgroups v1/v2).
func ParseLinuxCgroups(cgroupMaxData, cgroupCurrentData []byte, hostTotal uint64, hostAvail uint64) (uint64, uint64) {
	total := hostTotal
	avail := hostAvail

	maxStr := strings.TrimSpace(string(cgroupMaxData))
	if maxStr != "" && maxStr != "max" {
		if limit, err := strconv.ParseUint(maxStr, 10, 64); err == nil && limit > 0 && limit < hostTotal {
			total = limit
			currStr := strings.TrimSpace(string(cgroupCurrentData))
			if used, err := strconv.ParseUint(currStr, 10, 64); err == nil && used <= total {
				cgroupAvail := total - used
				if cgroupAvail < avail {
					avail = cgroupAvail
				}
			} else {
				if total < avail {
					avail = total
				}
			}
		}
	}

	return total, avail
}

// ParseDarwinVMStat parses vm_stat command output and calculates total and reclaimable available bytes.
func ParseDarwinVMStat(vmOut []byte, totalBytes uint64) (MemoryInfo, error) {
	if totalBytes == 0 {
		return MemoryInfo{}, fmt.Errorf("totalBytes must be greater than 0")
	}

	var pageSize uint64 = 4096
	var freePages, inactivePages, speculativePages uint64

	scanner := bufio.NewScanner(bytes.NewReader(vmOut))
	for scanner.Scan() {
		line := scanner.Text()
		if m := reDarwinPageSize.FindStringSubmatch(line); len(m) == 2 {
			if ps, pErr := strconv.ParseUint(m[1], 10, 64); pErr == nil && ps > 0 {
				pageSize = ps
			}
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, ".")
		val, parseErr := strconv.ParseUint(valStr, 10, 64)
		if parseErr != nil {
			continue
		}

		switch key {
		case "Pages free":
			freePages = val
		case "Pages inactive":
			inactivePages = val
		case "Pages speculative":
			speculativePages = val
		}
	}

	availPages := freePages + inactivePages + speculativePages
	availBytes := availPages * pageSize
	if availBytes == 0 || availBytes > totalBytes {
		availBytes = totalBytes / 2
	}

	return MemoryInfo{
		TotalBytes:     totalBytes,
		AvailableBytes: availBytes,
		Estimated:      true,
	}, nil
}
