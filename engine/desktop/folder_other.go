//go:build !windows

package desktop

import (
	"os"
	"path/filepath"
	"runtime"
)

func openFolderNative(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return startDetachedCmd("open", path)
	default:
		return startDetachedCmd("xdg-open", path)
	}
}

func showInFolderNative(path string) error {
	switch runtime.GOOS {
	case "darwin":
		fi, err := os.Stat(path)
		if err == nil && !fi.IsDir() {
			return startDetachedCmd("open", "-R", path)
		}
		targetDir := path
		if err == nil && !fi.IsDir() {
			targetDir = filepath.Dir(path)
		} else if err != nil {
			targetDir = filepath.Dir(path)
		}
		return startDetachedCmd("open", targetDir)
	default:
		targetDir := path
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			targetDir = filepath.Dir(path)
		} else if err != nil {
			targetDir = filepath.Dir(path)
		}
		return startDetachedCmd("xdg-open", targetDir)
	}
}

