package patcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestIsPermissionError_Classification(t *testing.T) {
	// 1. Positive permission cases (platform-independent)
	permissionCases := []error{
		os.ErrPermission,
		&os.PathError{Op: "open", Path: `/path/to/test.tmp`, Err: os.ErrPermission},
		fmt.Errorf("failed to create tools directory: mkdir tools/android: Access is denied"),
		fmt.Errorf("failed to create temp file for lspatch.jar: open ...: permission denied"),
		fmt.Errorf("failed to finalize component: rename C:\\test\\a.tmp C:\\test\\a: Access is denied"),
		fmt.Errorf("operation not permitted"),
		fmt.Errorf("write access denied for C:\\app: access is denied"),
		fmt.Errorf("Android 补丁环境安装失败：程序目录没有足够的写入权限。请将程序移动到有写权限的目录，或以管理员身份运行。"),
	}

	for _, err := range permissionCases {
		if !IsPermissionError(err) {
			t.Errorf("expected IsPermissionError(%v) to be true, got false", err)
		}
	}

	// Windows specific ERROR_ACCESS_DENIED = 5 vs Non-Windows EIO = 5
	rawErrno5 := &os.PathError{Op: "mkdir", Path: `tools`, Err: syscall.Errno(5)}
	if runtime.GOOS == "windows" {
		if !IsPermissionError(rawErrno5) {
			t.Errorf("on Windows, expected raw errno 5 (ERROR_ACCESS_DENIED) to be classified as permission error")
		}
	} else {
		if IsPermissionError(rawErrno5) {
			t.Errorf("on non-Windows, raw errno 5 (EIO) must NOT be classified as permission error")
		}
	}

	// 2. Negative non-permission cases (MUST not falsely blame permissions)
	nonPermissionCases := []error{
		nil,
		fmt.Errorf("dial tcp 140.82.121.4:443: connectex: A connection attempt failed"),
		fmt.Errorf("failed to download lspatch.jar: HTTP 404 Not Found"),
		fmt.Errorf("failed to download lspatch.jar: HTTP 500 Internal Server Error"),
		fmt.Errorf("checksum validation failed for lspatch.jar: expected a1b2, got c3d4"),
		fmt.Errorf("zip: not a valid zip file"),
		fmt.Errorf("read error during download of xposed-release.apk: unexpected EOF"),
		fmt.Errorf("failed to find adb on system"),
		fmt.Errorf("context canceled"),
	}

	for _, err := range nonPermissionCases {
		if IsPermissionError(err) {
			t.Errorf("expected IsPermissionError(%v) to be false, got true", err)
		}
	}
}

func TestTestDirectoryWriteAccess_Writable(t *testing.T) {
	tempDir := t.TempDir()
	targetDir := filepath.Join(tempDir, "sub", "tools")

	err := testDirectoryWriteAccess(targetDir)
	if err != nil {
		t.Fatalf("testDirectoryWriteAccess on writable dir failed: %v", err)
	}

	// Verify no probe file left behind
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		t.Fatalf("failed to read targetDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".perm_test_") {
			t.Errorf("probe file was left behind: %s", entry.Name())
		}
	}
}

func TestCheckAndroidEnvironmentWriteAccessForDir_Writable(t *testing.T) {
	tempDir := t.TempDir()

	err := CheckAndroidEnvironmentWriteAccessForDir(tempDir)
	if err != nil {
		t.Fatalf("CheckAndroidEnvironmentWriteAccessForDir failed: %v", err)
	}

	checkNoProbeFiles := func(d string) {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".perm_test_") {
				t.Errorf("probe file left in %s: %s", d, e.Name())
			}
		}
	}
	checkNoProbeFiles(tempDir)

	// On Windows, tools/android and jre should have been created and probed
	if runtime.GOOS == "windows" {
		toolsDir := filepath.Join(tempDir, "tools", "android")
		if fi, err := os.Stat(toolsDir); err != nil || !fi.IsDir() {
			t.Errorf("expected toolsDir %s to exist as dir", toolsDir)
		}
		jreDir := filepath.Join(tempDir, "jre")
		if fi, err := os.Stat(jreDir); err != nil || !fi.IsDir() {
			t.Errorf("expected jreDir %s to exist as dir", jreDir)
		}

		checkNoProbeFiles(toolsDir)
		checkNoProbeFiles(jreDir)
	}
}

func TestCheckAndroidEnvironmentWriteAccess_Denied(t *testing.T) {
	tempDir := t.TempDir()
	readOnlyDir := filepath.Join(tempDir, "readonly_app")
	if err := os.MkdirAll(readOnlyDir, 0755); err != nil {
		t.Fatalf("failed to create readOnlyDir: %v", err)
	}

	if runtime.GOOS == "windows" {
		username := os.Getenv("USERNAME")
		if username == "" {
			t.Skip("skipping, USERNAME env not found")
		}
		// Deny write permissions via icacls
		cmd := exec.Command("icacls", readOnlyDir, "/deny", fmt.Sprintf("%s:(OI)(CI)(W)", username))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("icacls failed: %v (%s)", err, string(out))
		}
		defer func() {
			// Restore permissions before TempDir cleanup
			cmdRestore := exec.Command("icacls", readOnlyDir, "/remove:d", username)
			_ = cmdRestore.Run()
		}()
	} else {
		_ = os.Chmod(readOnlyDir, 0555)
		defer os.Chmod(readOnlyDir, 0755)
	}

	err := testDirectoryWriteAccess(readOnlyDir)
	if err == nil {
		t.Fatalf("expected write access error on denied directory, got nil")
	}
	if !IsPermissionError(err) {
		t.Errorf("expected IsPermissionError to be true, got error: %v", err)
	}

	errDir := CheckAndroidEnvironmentWriteAccessForDir(readOnlyDir)
	if runtime.GOOS == "windows" {
		if errDir == nil {
			t.Fatalf("expected CheckAndroidEnvironmentWriteAccessForDir to fail on denied directory, got nil")
		}
		if !IsPermissionError(errDir) {
			t.Errorf("expected IsPermissionError for CheckAndroidEnvironmentWriteAccessForDir to be true, got: %v", errDir)
		}
	}
}

func TestDownloadComponentsWithSpecs_NetworkErrorNotMisclassified(t *testing.T) {
	tempDir := t.TempDir()
	toolsDir := filepath.Join(tempDir, "tools", "android")

	// Server that returns HTTP 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	specs := []ComponentSpec{
		{
			ID:       "lspatch",
			FileName: "lspatch.jar",
			Size:     100,
			SHA256:   "abcd",
			URL:      server.URL + "/lspatch.jar",
		},
	}

	var lastProg DownloadProgress
	err := DownloadComponentsWithSpecs(context.Background(), toolsDir, specs, nil, func(p DownloadProgress) {
		lastProg = p
	})

	if err == nil {
		t.Fatalf("expected download error, got nil")
	}
	if lastProg.Stage != "error" {
		t.Errorf("expected stage to be error, got %s", lastProg.Stage)
	}
	if lastProg.IsPermissionErr {
		t.Errorf("network HTTP 500 should NOT be marked as permission error")
	}
	if lastProg.Error == ErrPermissionUserNotice {
		t.Errorf("network HTTP 500 should NOT show permission notice")
	}
	if !strings.Contains(lastProg.Error, "HTTP 500") {
		t.Errorf("expected error to mention HTTP 500, got: %s", lastProg.Error)
	}
}

func TestDownloadComponentsWithSpecs_ChecksumMismatchNotMisclassified(t *testing.T) {
	tempDir := t.TempDir()
	toolsDir := filepath.Join(tempDir, "tools", "android")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("different payload content"))
	}))
	defer server.Close()

	specs := []ComponentSpec{
		{
			ID:       "lspatch",
			FileName: "lspatch.jar",
			Size:     25,
			SHA256:   "0000000000000000000000000000000000000000000000000000000000000000",
			URL:      server.URL + "/lspatch.jar",
		},
	}

	var lastProg DownloadProgress
	err := DownloadComponentsWithSpecs(context.Background(), toolsDir, specs, nil, func(p DownloadProgress) {
		lastProg = p
	})

	if err == nil {
		t.Fatalf("expected checksum error, got nil")
	}
	if lastProg.Stage != "error" {
		t.Errorf("expected stage error, got %s", lastProg.Stage)
	}
	if lastProg.IsPermissionErr {
		t.Errorf("checksum failure should NOT be marked as permission error")
	}
	if lastProg.Error == ErrPermissionUserNotice {
		t.Errorf("checksum failure should NOT show permission notice")
	}
	if !strings.Contains(lastProg.Error, "checksum validation failed") {
		t.Errorf("expected error to mention checksum validation failed, got: %s", lastProg.Error)
	}
}

func TestDownloadComponentsWithSpecs_ExtractFailureNotMisclassified(t *testing.T) {
	t.Setenv("GBF_DISABLE_SYSTEM_TOOLS", "1")
	tempDir := t.TempDir()
	toolsDir := filepath.Join(tempDir, "tools", "android")

	// Corrupt zip content
	corruptZip := []byte("this is not a valid zip archive header")
	hash := computeSHA256Bytes(corruptZip)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(corruptZip)
	}))
	defer server.Close()

	specs := []ComponentSpec{
		{
			ID:       "platform-tools",
			FileName: "platform-tools.zip",
			Size:     int64(len(corruptZip)),
			SHA256:   hash,
			URL:      server.URL + "/platform-tools.zip",
		},
	}

	var lastProg DownloadProgress
	err := DownloadComponentsWithSpecs(context.Background(), toolsDir, specs, nil, func(p DownloadProgress) {
		lastProg = p
	})

	if err == nil {
		t.Fatalf("expected extract error, got nil")
	}
	if lastProg.Stage != "error" {
		t.Errorf("expected stage error, got %s", lastProg.Stage)
	}
	if lastProg.IsPermissionErr {
		t.Errorf("corrupt zip extract failure should NOT be marked as permission error")
	}
	if lastProg.Error == ErrPermissionUserNotice {
		t.Errorf("corrupt zip failure should NOT show permission notice")
	}
	if !strings.Contains(lastProg.Error, "failed to extract") {
		t.Errorf("expected error to mention failed to extract, got: %s", lastProg.Error)
	}
}

func computeSHA256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
