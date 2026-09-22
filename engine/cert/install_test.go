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

func TestGetInstallStore(t *testing.T) {
	tempDir := t.TempDir()
	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	// A freshly generated CA in a temp dir must not be present in any system
	// Root store, so the store diagnostic must be empty.
	if store := mgr.GetInstallStore(); store != "" {
		t.Errorf("expected empty store for non-installed CA, got %q", store)
	}
}

func TestDetectCAStoreInvalidInput(t *testing.T) {
	if got := detectCAStore(""); got != "" {
		t.Errorf("expected empty store for empty sha1, got %q", got)
	}
	if got := detectCAStore("   "); got != "" {
		t.Errorf("expected empty store for blank sha1, got %q", got)
	}
	// A well-formed thumbprint that cannot exist in any real Root store
	// (avoid all-zeros: certutil treats it as a wildcard and enumerates).
	if got := detectCAStore("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); got != "" {
		t.Errorf("expected empty store for absent thumbprint, got %q", got)
	}
}
