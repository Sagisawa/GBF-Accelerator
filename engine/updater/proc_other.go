//go:build !windows

package updater

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func waitForParentExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(pid, 0)
		if err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待旧进程 (PID %d) 退出超时 (%s)", pid, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
