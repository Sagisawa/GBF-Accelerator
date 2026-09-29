package process

// MemoryInfo holds system-level physical memory statistics in bytes.
type MemoryInfo struct {
	TotalBytes     uint64 `json:"total_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	Estimated      bool   `json:"estimated"`
}

// GetSystemMemory queries the host operating system for total and available physical RAM.
func GetSystemMemory() (MemoryInfo, error) {
	return getSystemMemory()
}
