//go:build !windows

package patcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLSPatchJar_OpenFailure(t *testing.T) {
	tempDir := t.TempDir()
	lockedJar := filepath.Join(tempDir, "locked-lspatch.jar")
	if err := os.WriteFile(lockedJar, []byte("some non-empty binary content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(lockedJar, 0000); err != nil {
		t.Skipf("unable to chmod 0000: %v", err)
	}
	defer os.Chmod(lockedJar, 0644)
	if f, err := os.Open(lockedJar); err == nil {
		_ = f.Close()
		t.Skip("running as root; cannot test open failure via permission")
	}

	err := ValidateLSPatchJar(lockedJar)
	if err == nil {
		t.Fatalf("expected ValidateLSPatchJar to fail on unreadable file, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open lspatch.jar for hashing") {
		t.Errorf("expected 'failed to open lspatch.jar for hashing', got: %v", err)
	}
}
