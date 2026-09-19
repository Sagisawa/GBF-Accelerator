//go:build windows
// +build windows

package startup

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func isStartupEnabled() bool {
	cmd := exec.Command("reg", "query", runKey, "/v", AppName)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run() == nil
}

func setStartupEnabled(enabled bool) error {
	if enabled {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		cmdStr := fmt.Sprintf(`"%s" --minimized`, exe)
		cmd := exec.Command("reg", "add", runKey, "/v", AppName, "/t", "REG_SZ", "/d", cmdStr, "/f")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Run()
	}

	cmd := exec.Command("reg", "delete", runKey, "/v", AppName, "/f")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Run()
	return nil
}
