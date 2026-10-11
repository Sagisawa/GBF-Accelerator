//go:build windows

package updater

import (
	"fmt"
	"syscall"
	"time"
)

const processSynchronize = 0x00100000

func waitForParentExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	handle, err := syscall.OpenProcess(processSynchronize, false, uint32(pid))
	if err != nil {
		// Process has already exited or handle is no longer valid.
		return nil
	}
	defer syscall.CloseHandle(handle)

	ms := uint32(timeout.Milliseconds())
	event, err := syscall.WaitForSingleObject(handle, ms)
	if err != nil {
		return fmt.Errorf("等待旧进程 (PID %d) 退出失败: %w", pid, err)
	}
	if event == syscall.WAIT_TIMEOUT {
		return fmt.Errorf("等待旧进程 (PID %d) 退出超时 (%s)", pid, timeout)
	}
	return nil
}
