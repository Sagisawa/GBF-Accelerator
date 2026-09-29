//go:build !windows && !linux && !darwin
// +build !windows,!linux,!darwin

package process

func getSystemMemory() (MemoryInfo, error) {
	// Fallback for unsupported platforms: safe defaults
	const defaultTotal = uint64(8 * 1024 * 1024 * 1024)     // 8 GB
	const defaultAvail = uint64(4 * 1024 * 1024 * 1024)     // 4 GB
	return MemoryInfo{
		TotalBytes:     defaultTotal,
		AvailableBytes: defaultAvail,
		Estimated:      true,
	}, nil
}
