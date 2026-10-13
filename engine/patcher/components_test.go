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

func TestCheckComponents_EmptySHA_NeverVerified(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test-tool.zip")
	_ = os.WriteFile(filePath, []byte("some-arbitrary-binary-data"), 0644)

	specs := []ComponentSpec{
		{
			ID:       "unverified-tool",
			FileName: "test-tool.zip",
			Size:     int64(len("some-arbitrary-binary-data")),
			SHA256:   "", // Empty SHA256 must never be treated as verified!
		},
	}

	installed, verified, statuses, _ := CheckComponentsWithSpecs(tempDir, "", specs)
	if !installed {
		t.Fatalf("expected installed=true")
	}
	if verified {
		t.Fatalf("expected verified=false when spec.SHA256 is empty")
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].Verified {
		t.Fatalf("component status must have Verified=false when SHA256 is empty")
	}
}

func TestFullEnvironmentSpecs_PlatformBoundary(t *testing.T) {
	// 1. Linux must not include unverified platform-tools or jre archives, and every spec must have a non-empty SHA-256.
	linuxSpecs := fullEnvironmentSpecsForPlatform("linux", "amd64")
	if len(linuxSpecs) != 3 {
		t.Fatalf("expected 3 specs on linux/amd64, got %d", len(linuxSpecs))
	}
	for _, s := range linuxSpecs {
		if strings.TrimSpace(s.SHA256) == "" {
			t.Errorf("linux spec %s (%s) has empty SHA256", s.ID, s.FileName)
		}
		if s.ID == "platform-tools" || s.ID == "jre" {
			t.Errorf("unexpected platform-specific archive spec %q on linux/amd64", s.ID)
		}
	}

	// 2. Windows x64 must retain platform-tools and jre with canonical SHA-256 checksums.
	winSpecs := fullEnvironmentSpecsForPlatform("windows", "amd64")
	var winPT, winJRE *ComponentSpec
	for i := range winSpecs {
		if strings.TrimSpace(winSpecs[i].SHA256) == "" {
			t.Errorf("windows spec %s (%s) has empty SHA256", winSpecs[i].ID, winSpecs[i].FileName)
		}
		switch winSpecs[i].ID {
		case "platform-tools":
			winPT = &winSpecs[i]
		case "jre":
			winJRE = &winSpecs[i]
		}
	}
	if winPT == nil || winPT.FileName != "platform-tools-windows.zip" || winPT.SHA256 != CanonicalPlatformToolsWindowsSHA256 {
		t.Fatalf("unexpected windows platform-tools spec: %+v", winPT)
	}
	if winJRE == nil || winJRE.FileName != "jre-windows-x64.zip" || winJRE.SHA256 != CanonicalJreWindowsSHA256 {
		t.Fatalf("unexpected windows jre spec: %+v", winJRE)
	}

	// 3. macOS ARM64 must retain platform-tools and jre with canonical SHA-256 checksums.
	macSpecs := fullEnvironmentSpecsForPlatform("darwin", "arm64")
	var macPT, macJRE *ComponentSpec
	for i := range macSpecs {
		if strings.TrimSpace(macSpecs[i].SHA256) == "" {
			t.Errorf("darwin/arm64 spec %s (%s) has empty SHA256", macSpecs[i].ID, macSpecs[i].FileName)
		}
		switch macSpecs[i].ID {
		case "platform-tools":
			macPT = &macSpecs[i]
		case "jre":
			macJRE = &macSpecs[i]
		}
	}
	if macPT == nil || macPT.FileName != "platform-tools-darwin.zip" || macPT.SHA256 != CanonicalPlatformToolsDarwinSHA256 {
		t.Fatalf("unexpected darwin platform-tools spec: %+v", macPT)
	}
	if macJRE == nil || macJRE.FileName != "jre-macos-arm64.zip" || strings.TrimSpace(macJRE.SHA256) == "" {
		t.Fatalf("unexpected darwin/arm64 jre spec: %+v", macJRE)
	}

	// 4. Canonical platform-tools SHA-256 helper boundary check.
	if got := canonicalPlatformToolsSHA256ForOS("linux"); got != "" {
		t.Errorf("expected empty canonical platform-tools SHA256 on linux, got %q", got)
	}
	if got := canonicalPlatformToolsSHA256ForOS("windows"); got != CanonicalPlatformToolsWindowsSHA256 {
		t.Errorf("expected %q for windows, got %q", CanonicalPlatformToolsWindowsSHA256, got)
	}
	if got := canonicalPlatformToolsSHA256ForOS("darwin"); got != CanonicalPlatformToolsDarwinSHA256 {
		t.Errorf("expected %q for darwin, got %q", CanonicalPlatformToolsDarwinSHA256, got)
	}
}

func TestDownloadComponentsWithSpecs_EmptySHA_FailsBeforeNetworkDownload(t *testing.T) {
	t.Setenv("GBF_DISABLE_SYSTEM_TOOLS", "1")
	tempDir := t.TempDir()

	var requestCount int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("unverified-archive-payload"))
	}))
	defer srv.Close()

	specs := []ComponentSpec{
		{
			ID:       "unverified-tool",
			FileName: "unverified-tool.zip",
			Size:     26,
			SHA256:   "",
			URL:      srv.URL + "/unverified-tool.zip",
		},
	}

	var lastProg DownloadProgress
	err := DownloadComponentsWithSpecs(context.Background(), tempDir, specs, nil, func(p DownloadProgress) {
		lastProg = p
	})
	if err == nil || !strings.Contains(err.Error(), "missing expected SHA-256 checksum") {
		t.Fatalf("expected missing SHA-256 checksum error, got: %v", err)
	}
	if lastProg.Stage != "error" {
		t.Errorf("expected progress stage 'error', got %q", lastProg.Stage)
	}
	mu.Lock()
	gotReqs := requestCount
	mu.Unlock()
	if gotReqs != 0 {
		t.Errorf("expected 0 HTTP requests when spec.SHA256 is empty, got %d", gotReqs)
	}
}
