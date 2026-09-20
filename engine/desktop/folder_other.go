//go:build !windows

package desktop

import (
	"os/exec"
	"runtime"
)

func openFolderNative(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
