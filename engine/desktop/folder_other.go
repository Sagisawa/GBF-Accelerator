//go:build !windows

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
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

func showInFolderNative(path string) error {
	switch runtime.GOOS {
	case "darwin":
		fi, err := os.Stat(path)
		if err == nil && !fi.IsDir() {
			return exec.Command("open", "-R", path).Start()
		}
		targetDir := path
		if err == nil && !fi.IsDir() {
			targetDir = filepath.Dir(path)
		} else if err != nil {
			targetDir = filepath.Dir(path)
		}
		return exec.Command("open", targetDir).Start()
	default:
		targetDir := path
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			targetDir = filepath.Dir(path)
		} else if err != nil {
			targetDir = filepath.Dir(path)
		}
		return exec.Command("xdg-open", targetDir).Start()
	}
}

