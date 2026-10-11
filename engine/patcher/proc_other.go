//go:build !windows

package patcher

import (
	"os/exec"
	"time"
)

// prepareCmd configures bounded pipe wait delay on non-Windows platforms.
func prepareCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 3 * time.Second
	}
}
