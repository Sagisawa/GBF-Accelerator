package patcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrPermissionUserNotice is the unified user-facing message when write access to the application directory is denied.
const ErrPermissionUserNotice = "Android 补丁环境安装失败：程序目录没有足够的写入权限。请将程序移动到有写权限的目录，或以管理员身份运行。"

// IsPermissionError determines whether the provided error was caused by filesystem permission / access denial.
func IsPermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) || os.IsPermission(err) {
		return true
	}
	if isPlatformPermissionErrno(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "write access denied") ||
		strings.Contains(msg, "没有足够的写入权限") {
		return true
	}
	return false
}

// testDirectoryWriteAccess tests creating the directory (if not existent), creating a small temporary probe file,
// writing content, closing it, and deleting it cleanly. No test files are left behind.
func testDirectoryWriteAccess(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	testFile := filepath.Join(dir, fmt.Sprintf(".perm_test_%d_%d.tmp", os.Getpid(), time.Now().UnixNano()))
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	_, writeErr := f.Write([]byte("probe"))
	closeErr := f.Close()
	removeErr := os.Remove(testFile)

	if writeErr != nil {
		_ = os.Remove(testFile)
		return writeErr
	}
	if closeErr != nil {
		_ = os.Remove(testFile)
		return closeErr
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}

	return nil
}
