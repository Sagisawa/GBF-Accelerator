//go:build windows

package patcher

import (
	"os/exec"
	"syscall"
)

// prepareCmd configures the command to run completely silently on Windows without spawning or flashing a console window.
func prepareCmd(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
