package patcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCheckComponents_Missing(t *testing.T) {
	tempDir := t.TempDir()
	installed, verified, statuses, _ := CheckComponents(tempDir, "")

	if installed {
		t.Errorf("expected installed=false for empty directory")
	}
	if verified {
		t.Errorf("expected verified=false for empty directory")
	}
	if len(statuses) != 3 {
		t.Fatalf("expected 3 component statuses, got %d", len(statuses))
	}
	for _, s := range statuses {
		if s.Installed || s.Verified {
			t.Errorf("component %s should not be installed or verified", s.ID)
		}
	}
}

func TestCheckComponents_ChecksumMismatch(t *testing.T) {
	tempDir := t.TempDir()

	// Create files with corrupted / dummy content
	_ = os.WriteFile(filepath.Join(tempDir, "lspatch.jar"), []byte("corrupt-lspatch"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "xposed-release.apk"), []byte("corrupt-module"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "THIRD_PARTY_LICENSES.md"), []byte("corrupt-licenses"), 0644)

	installed, verified, statuses, errStr := CheckComponents(tempDir, "")
	if !installed {
		t.Errorf("expected installed=true because files exist")
	}
	if verified {
		t.Errorf("expected verified=false due to hash mismatch")
	}
	if errStr == "" {
		t.Errorf("expected non-empty errStr")
	}
	for _, s := range statuses {
		if !s.Installed {
			t.Errorf("component %s should be marked installed", s.ID)
		}
		if s.Verified {
			t.Errorf("component %s should fail verification", s.ID)
		}
		if !strings.Contains(s.Error, "哈希校验不匹配") {
			t.Errorf("expected mismatch error, got %q", s.Error)
		}
	}
}

func TestDownloadComponents_SuccessAndReuse(t *testing.T) {
	// Construct test payloads matching canonical hashes
	// For testing, we mock 3 files and calculate their actual hashes, then mock specs or use custom URLs
	tempDir := t.TempDir()

	lspatchContent := "canonical-lspatch-test-payload-bytes"
	moduleContent := "canonical-module-test-payload-bytes"
	licensesContent := "canonical-licenses-test-payload-bytes"

	calcSHA := func(s string) string {
		h := sha256.Sum256([]byte(s))
		return hex.EncodeToString(h[:])
	}

	lspatchSHA := calcSHA(lspatchContent)
	moduleSHA := calcSHA(moduleContent)
	licensesSHA := calcSHA(licensesContent)

	downloadCount := 0
	var countMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		countMu.Lock()
		downloadCount++
		countMu.Unlock()

		switch r.URL.Path {
		case "/lspatch.jar":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(lspatchContent))
		case "/xposed-release.apk":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(moduleContent))
		case "/THIRD_PARTY_LICENSES.md":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(licensesContent))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	customURLs := map[string]string{
		"lspatch":  srv.URL + "/lspatch.jar",
		"module":   srv.URL + "/xposed-release.apk",
		"licenses": srv.URL + "/THIRD_PARTY_LICENSES.md",
	}

	// Temporarily override canonical hashes for test
	specs := GetDefaultComponentSpecs()
	specs[0].SHA256 = lspatchSHA
	specs[0].Size = int64(len(lspatchContent))
	specs[1].SHA256 = moduleSHA
	specs[1].Size = int64(len(moduleContent))
	specs[2].SHA256 = licensesSHA
	specs[2].Size = int64(len(licensesContent))

	// Run download with custom progress tracker
	var lastProgress DownloadProgress
	err := DownloadComponentsWithSpecs(context.Background(), tempDir, specs, customURLs, func(p DownloadProgress) {
		lastProgress = p
	})
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	if !lastProgress.Done || lastProgress.Percent != 100.0 {
		t.Errorf("expected download done with 100%%, got %+v", lastProgress)
	}

	// Verify manifest.json was written
	if _, err := os.Stat(filepath.Join(tempDir, "manifest.json")); err != nil {
		t.Errorf("manifest.json was not created: %v", err)
	}

	// Verify CheckComponentsWithSpecs reports everything installed and verified
	installed, verified, statuses, errStr := CheckComponentsWithSpecs(tempDir, "", specs)
	if !installed || !verified || errStr != "" {
		t.Fatalf("expected components verified, got installed=%v verified=%v err=%s statuses=%+v", installed, verified, errStr, statuses)
	}

	// Verify second call reuses existing files without re-downloading
	countBefore := downloadCount
	errSecond := DownloadComponentsWithSpecs(context.Background(), tempDir, specs, customURLs, nil)
	if errSecond != nil {
		t.Fatalf("second download call failed: %v", errSecond)
	}
	if downloadCount != countBefore {
		t.Errorf("expected no additional downloads for already verified files: before=%d after=%d", countBefore, downloadCount)
	}

	// Verify no .tmp files remain on disk after completion
	files, _ := os.ReadDir(tempDir)
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".tmp") {
			t.Errorf("found leftover temp file: %s", f.Name())
		}
	}
}

func TestDownloadComponents_FailureCleansTemp(t *testing.T) {
	tempDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send some bytes then fail
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial content that fails checksum"))
	}))
	defer srv.Close()

	customURLs := map[string]string{
		"lspatch":  srv.URL + "/lspatch.jar",
		"module":   srv.URL + "/xposed-release.apk",
		"licenses": srv.URL + "/THIRD_PARTY_LICENSES.md",
	}

	err := DownloadComponents(context.Background(), tempDir, customURLs, nil)
	if err == nil {
		t.Fatalf("expected error due to checksum failure, got nil")
	}

	// Ensure no .tmp files exist
	entries, _ := os.ReadDir(tempDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file was not cleaned up: %s", e.Name())
		}
	}
}

func TestDownloadComponents_CancelContext(t *testing.T) {
	tempDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := DownloadComponents(ctx, tempDir, nil, nil)
	if err == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}

	entries, _ := os.ReadDir(tempDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file was not cleaned up on cancellation: %s", e.Name())
		}
	}
}
