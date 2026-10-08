//go:build !windows

package patcher

// CheckAndroidEnvironmentWriteAccess is a no-op on non-Windows platforms.
func CheckAndroidEnvironmentWriteAccess() error {
	return nil
}

// CheckAndroidEnvironmentWriteAccessForDir is a no-op on non-Windows platforms.
func CheckAndroidEnvironmentWriteAccessForDir(baseDir string) error {
	return nil
}

// isPlatformPermissionErrno returns false on non-Windows platforms so Unix errno 5 (EIO) is not misclassified.
func isPlatformPermissionErrno(err error) bool {
	return false
}

