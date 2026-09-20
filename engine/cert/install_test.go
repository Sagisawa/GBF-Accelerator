package cert

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCertFingerprint(t *testing.T) {
	tempDir := t.TempDir()
	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	fp256 := mgr.GetFingerprintSHA256()
	if fp256 == "" {
		t.Fatalf("expected non-empty SHA-256 fingerprint")
	}
	parts := strings.Split(fp256, ":")
	if len(parts) != 32 {
		t.Errorf("expected 32 colon-separated hex bytes in SHA-256 fingerprint, got %d: %s", len(parts), fp256)
	}

	fp1 := mgr.GetFingerprintSHA1()
	if len(fp1) != 40 {
		t.Errorf("expected 40 hex characters in SHA-1 thumbprint, got %d: %s", len(fp1), fp1)
	}

	// Legacy leaked CA constant check
	if LegacyLeakedCASHA1 != "51E9AA40A64FB8DC63F18F4B1A11B98D1CF8D3FF" {
		t.Errorf("unexpected LegacyLeakedCASHA1 value: %s", LegacyLeakedCASHA1)
	}

	// Empty input handling
	if GetCAFingerprintSHA256(nil) != "" {
		t.Errorf("expected empty string for nil PEM")
	}
	if GetCAFingerprintSHA1([]byte("bad pem")) != "" {
		t.Errorf("expected empty string for bad PEM")
	}

	// Test IsInstalled doesn't panic
	_ = mgr.IsInstalled()

	// Test Install on non-existent file errors
	err = mgr.Install(filepath.Join(tempDir, "non_existent"))
	if err == nil {
		t.Errorf("expected error installing non-existent cert")
	}
}
