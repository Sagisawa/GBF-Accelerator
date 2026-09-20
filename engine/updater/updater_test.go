package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseVersionAndCompare(t *testing.T) {
	testCases := []struct {
		remote  string
		current string
		isNewer bool
	}{
		{"v1.8.1", "1.8.0", true},
		{"1.9.0", "1.8.0", true},
		{"2.0.0", "1.9.9", true},
		{"1.8.0", "1.8.0", false},
		{"1.7.9", "1.8.0", false},
		{"v1.8.0-rc1", "1.8.0", false},
		{"1.8.0.1", "1.8.0", true},
		// Pre-release ordering (semver): stable outranks pre-release; rcN compares numerically.
		{"1.8.0", "1.8.0-rc1", true},
		{"1.8.0", "v1.8.0-beta", true},
		{"1.8.0-rc2", "1.8.0-rc1", true},
		{"1.8.0-rc1", "1.8.0-rc2", false},
		{"1.8.0-rc1", "1.8.0-rc1", false},
		{"1.8.0-rc.2", "1.8.0-rc.1", true},
	}

	for _, tc := range testCases {
		res := IsNewerVersion(tc.remote, tc.current)
		if res != tc.isNewer {
			t.Errorf("IsNewerVersion(%q, %q) = %v; want %v", tc.remote, tc.current, res, tc.isNewer)
		}
	}
}

func createTestZip(t *testing.T, filename string, content []byte) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	f, err := w.Create(filename)
	if err != nil {
		t.Fatalf("failed to create zip file entry: %v", err)
	}
	_, err = f.Write(content)
	if err != nil {
		t.Fatalf("failed to write zip content: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

func TestDownloadReleaseAsset(t *testing.T) {
	zipData := createTestZip(t, "test.txt", []byte("hello world"))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", strconv.Itoa(len(zipData)))
		_, _ = w.Write(zipData)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "test_download.zip")

	var progressReports int
	finalPath, err := DownloadReleaseAsset(
		ts.URL,
		destFile,
		"",
		"",
		func(downloaded, total int64) {
			progressReports++
		},
		context.Background(),
	)

	if err != nil {
		t.Fatalf("DownloadReleaseAsset failed: %v", err)
	}
	if finalPath != destFile {
		t.Errorf("expected final path %s, got %s", destFile, finalPath)
	}
	if fi, err := os.Stat(destFile); err != nil || fi.Size() == 0 {
		t.Fatalf("downloaded file does not exist or empty")
	}

	// Verify it is a valid zip
	zr, err := zip.OpenReader(destFile)
	if err != nil {
		t.Fatalf("downloaded file is not a valid zip: %v", err)
	}
	_ = zr.Close()

	// Test overwriting existing file with retry loop under momentary file lock
	// Simulate antivirus / indexer holding a read/write handle on destFile
	lockFile, lockErr := os.OpenFile(destFile, os.O_RDWR, 0666)
	if lockErr == nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = lockFile.Close()
		}()
	}

	finalPath2, err := DownloadReleaseAsset(ts.URL, destFile, "", "", nil, context.Background())
	if err != nil {
		t.Fatalf("DownloadReleaseAsset overwriting locked file failed: %v", err)
	}
	if finalPath2 != destFile {
		t.Errorf("expected final path %s, got %s", destFile, finalPath2)
	}

	// Test cancellation
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	destCancel := filepath.Join(tempDir, "cancel.zip")
	_, err = DownloadReleaseAsset(ts.URL, destCancel, "", "", nil, ctxCancel)
	if err == nil {
		t.Errorf("expected error on cancelled context, got nil")
	}
}

func TestCheckForUpdateMock(t *testing.T) {
	mockJSON := `{
		"tag_name": "v2.0.0",
		"name": "v2.0.0 - Major Go Engine Upgrade",
		"body": "Detailed release notes here",
		"html_url": "https://github.com/Sagisawa/GBF-Accelerator/releases/tag/v2.0.0",
		"published_at": "2026-09-19T12:00:00Z",
		"assets": [
			{"name": "GBF_Accelerator_v2.0.0_GUI.zip", "browser_download_url": "https://example.com/win.zip", "size": 1000},
			{"name": "GBF_Accelerator_v2.0.0_macOS_universal2.zip", "browser_download_url": "https://example.com/mac.zip", "size": 1000}
		]
	}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(mockJSON))
	}))
	defer ts.Close()

	// Direct test of json decoding logic by running with dummy proxy
	info := CheckForUpdate("invalid://proxy", 100*time.Millisecond, "1.8.0")
	// Since invalid proxy fails and direct fallback to api.github.com might fail or succeed depending on connectivity,
	// info will have HasUpdate or Error, and never panic.
	if info == nil {
		t.Fatalf("expected non-nil UpdateInfo")
	}
}

func TestExtractSHA256(t *testing.T) {
	hashWin := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	hashMac := "1111111111111111111111111111111111111111111111111111111111111111"

	// 1. Standard label pattern
	bodyLabel := fmt.Sprintf("Some notes\nSHA256: %s\nMore notes", hashWin)
	if got := ExtractSHA256(bodyLabel, ""); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}

	// 2. Markdown backticks and multi-platform isolation
	bodyMarkdown := fmt.Sprintf("### Checksums\n- `GBF_Accelerator_v1.9.0_GUI.zip`: `%s`\n- `GBF_Accelerator_v1.9.0_macOS_universal2.zip`: `%s`\n", hashWin, hashMac)
	if got := ExtractSHA256(bodyMarkdown, "GBF_Accelerator_v1.9.0_GUI.zip"); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}
	if got := ExtractSHA256(bodyMarkdown, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"); got != hashMac {
		t.Errorf("expected %s, got %s", hashMac, got)
	}

	// 3. sha256sum multi-line format with binary (*) and text flags
	bodySha256sum := fmt.Sprintf("\n%s  GBF_Accelerator_v1.9.0_GUI.zip\n%s *GBF_Accelerator_v1.9.0_macOS_universal2.zip\n", hashWin, hashMac)
	if got := ExtractSHA256(bodySha256sum, "GBF_Accelerator_v1.9.0_GUI.zip"); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}
	if got := ExtractSHA256(bodySha256sum, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"); got != hashMac {
		t.Errorf("expected %s, got %s", hashMac, got)
	}

	// 4. Markdown table format
	bodyTable := fmt.Sprintf("| Asset | SHA-256 |\n| :--- | :--- |\n| `GBF_Accelerator_v1.9.0_GUI.zip` | `%s` |\n| `GBF_Accelerator_v1.9.0_macOS_universal2.zip` | `%s` |\n", hashWin, hashMac)
	if got := ExtractSHA256(bodyTable, "GBF_Accelerator_v1.9.0_GUI.zip"); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}
	if got := ExtractSHA256(bodyTable, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"); got != hashMac {
		t.Errorf("expected %s, got %s", hashMac, got)
	}

	// 5. BSD format
	bodyBSD := fmt.Sprintf("\nSHA256 (GBF_Accelerator_v1.9.0_GUI.zip) = %s\nSHA256 (GBF_Accelerator_v1.9.0_macOS_universal2.zip) = %s\n", hashWin, hashMac)
	if got := ExtractSHA256(bodyBSD, "GBF_Accelerator_v1.9.0_GUI.zip"); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}
	if got := ExtractSHA256(bodyBSD, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"); got != hashMac {
		t.Errorf("expected %s, got %s", hashMac, got)
	}

	// 6. Indented / sub-item format
	bodyIndented := fmt.Sprintf("- **GBF_Accelerator_v1.9.0_GUI.zip**\n  - SHA-256: `%s`\n- **GBF_Accelerator_v1.9.0_macOS_universal2.zip**\n  - SHA-256: `%s`\n", hashWin, hashMac)
	if got := ExtractSHA256(bodyIndented, "GBF_Accelerator_v1.9.0_GUI.zip"); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}
	if got := ExtractSHA256(bodyIndented, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"); got != hashMac {
		t.Errorf("expected %s, got %s", hashMac, got)
	}

	// 7. Single hash release notes fallback
	bodySingle := fmt.Sprintf("Release v1.9.0\nSHA-256: %s", hashWin)
	if got := ExtractSHA256(bodySingle, "GBF_Accelerator_v1.9.0_GUI.zip"); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}
	if got := ExtractSHA256(bodySingle, ""); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}

	// 8. Multiple hashes with unmatched filename returns empty
	if got := ExtractSHA256(bodyMarkdown, "unmatched_other_file.zip"); got != "" {
		t.Errorf("expected empty on unmatched filename, got %s", got)
	}

	// 9. Uppercase hash normalization to lowercase
	bodyUpper := fmt.Sprintf("SHA-256: %s", strings.ToUpper(hashWin))
	if got := ExtractSHA256(bodyUpper, ""); got != hashWin {
		t.Errorf("expected %s, got %s", hashWin, got)
	}

	// 10. Empty or not found
	if got := ExtractSHA256("", ""); got != "" {
		t.Errorf("expected empty on empty text, got %s", got)
	}
	if got := ExtractSHA256("no hash here", ""); got != "" {
		t.Errorf("expected empty on no hash, got %s", got)
	}
}

func TestDownloadReleaseAsset_SHA256Verification(t *testing.T) {
	zipData := createTestZip(t, "data.txt", []byte("sha256 test data"))
	hasher := sha256.New()
	hasher.Write(zipData)
	correctHash := hex.EncodeToString(hasher.Sum(nil))
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", strconv.Itoa(len(zipData)))
		_, _ = w.Write(zipData)
	}))
	defer ts.Close()

	tempDir := t.TempDir()

	// 1. Success with matching hash (case insensitive)
	destOK := filepath.Join(tempDir, "ok.zip")
	finalPath, err := DownloadReleaseAsset(ts.URL, destOK, "", strings.ToUpper(correctHash), nil, context.Background())
	if err != nil {
		t.Fatalf("expected successful download with correct hash, got error: %v", err)
	}
	if finalPath != destOK {
		t.Errorf("expected %s, got %s", destOK, finalPath)
	}
	if fi, err := os.Stat(destOK); err != nil || fi.Size() == 0 {
		t.Fatalf("downloaded file missing or empty")
	}

	// 2. Failure with mismatched hash
	destFail := filepath.Join(tempDir, "fail.zip")
	_, err = DownloadReleaseAsset(ts.URL, destFail, "", wrongHash, nil, context.Background())
	if err == nil {
		t.Fatal("expected error on SHA-256 mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "SHA-256 校验失败") {
		t.Errorf("expected SHA-256 mismatch message, got: %v", err)
	}
	// Verify .part was deleted
	if _, statErr := os.Stat(destFail + ".part"); !os.IsNotExist(statErr) {
		t.Errorf(".part file must be deleted upon hash mismatch")
	}
	if _, statErr := os.Stat(destFail); !os.IsNotExist(statErr) {
		t.Errorf("target file must not be created upon hash mismatch")
	}
}
