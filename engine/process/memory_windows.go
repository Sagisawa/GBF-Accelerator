//go:build windows
// +build windows

package process

import (
	"fmt"
	"syscall"
	"unsafe"
)

type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

func getSystemMemory() (MemoryInfo, error) {
	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))

	r1, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return MemoryInfo{}, fmt.Errorf("GlobalMemoryStatusEx failed: %w", err)
	}

	return MemoryInfo{
		TotalBytes:     ms.ullTotalPhys,
		AvailableBytes: ms.ullAvailPhys,
		Estimated:      false,
	}, nil
}
