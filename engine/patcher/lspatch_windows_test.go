//go:build windows

package patcher

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestValidateLSPatchJar_OpenFailure(t *testing.T) {
	tempDir := t.TempDir()
	lockedJar := filepath.Join(tempDir, "locked-lspatch.jar")
	if err := os.WriteFile(lockedJar, []byte("some non-empty binary content"), 0644); err != nil {
		t.Fatal(err)
	}

	path16, err := syscall.UTF16PtrFromString(lockedJar)
	if err != nil {
		t.Fatal(err)
	}
	// Open with 0 share mode -> exclusive lock, preventing subsequent os.Open
	handle, err := syscall.CreateFile(path16, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Skipf("unable to exclusively lock file on Windows: %v", err)
	}
	defer syscall.CloseHandle(handle)

	err = ValidateLSPatchJar(lockedJar)
	if err == nil {
		t.Fatalf("expected ValidateLSPatchJar to fail on locked file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open lspatch.jar for hashing") {
		t.Errorf("expected 'failed to open lspatch.jar for hashing', got: %v", err)
	}
}

func TestValidateLSPatchJar_ComputeChecksumFailure(t *testing.T) {
	tempDir := t.TempDir()
	lockedJar := filepath.Join(tempDir, "read-locked-lspatch.jar")
	if err := os.WriteFile(lockedJar, []byte("some non-empty binary content"), 0644); err != nil {
		t.Fatal(err)
	}

	path16, err := syscall.UTF16PtrFromString(lockedJar)
	if err != nil {
		t.Fatal(err)
	}
	// Open allowing shared read and write so os.Open succeeds
	handle, err := syscall.CreateFile(path16, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Skipf("unable to open file on Windows: %v", err)
	}
	defer syscall.CloseHandle(handle)

	procLockFile := syscall.NewLazyDLL("kernel32.dll").NewProc("LockFile")
	// Lock the first 100 bytes exclusively
	r1, _, errSys := procLockFile.Call(uintptr(handle), 0, 0, 100, 0)
	if r1 == 0 {
		t.Skipf("LockFile failed: %v", errSys)
	}
	defer func() {
		procUnlockFile := syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFile")
		procUnlockFile.Call(uintptr(handle), 0, 0, 100, 0)
	}()

	err = ValidateLSPatchJar(lockedJar)
	if err == nil {
		t.Fatalf("expected ValidateLSPatchJar to fail on read-locked file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to compute lspatch.jar checksum") {
		t.Errorf("expected 'failed to compute lspatch.jar checksum', got: %v", err)
	}
}
