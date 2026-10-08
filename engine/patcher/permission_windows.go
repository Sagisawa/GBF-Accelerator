//go:build windows

package patcher

import (
	"errors"
	"fmt"
	"path/filepath"
	"syscall"

	"gbf-proxy/config"
)

// CheckAndroidEnvironmentWriteAccess verifies write permissions on the base directory,
// tools/android directory, and jre directory required for Android environment installation on Windows.
func CheckAndroidEnvironmentWriteAccess() error {
	return CheckAndroidEnvironmentWriteAccessForDir(config.GetBaseDir())
}

// CheckAndroidEnvironmentWriteAccessForDir checks write access against a specific base directory.
func CheckAndroidEnvironmentWriteAccessForDir(baseDir string) error {
	dirsToCheck := []string{
		baseDir,
		filepath.Join(baseDir, "tools", "android"),
		filepath.Join(baseDir, "jre"),
	}

	for _, dir := range dirsToCheck {
		if err := testDirectoryWriteAccess(dir); err != nil {
			return fmt.Errorf("write access denied for %s: %w", dir, err)
		}
	}
	return nil
}

// isPlatformPermissionErrno checks whether an error is a Windows ERROR_ACCESS_DENIED (errno 5).
func isPlatformPermissionErrno(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		// Windows ERROR_ACCESS_DENIED = 5
		return errno == 5
	}
	return false
}

