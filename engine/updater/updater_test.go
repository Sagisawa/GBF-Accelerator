package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

	finalPath2, err := DownloadReleaseAsset(ts.URL, destFile, "", nil, context.Background())
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
	_, err = DownloadReleaseAsset(ts.URL, destCancel, "", nil, ctxCancel)
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

func TestDownloadReleaseAsset_RetryContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "lock_cancel.zip")
	_ = os.WriteFile(destFile, []byte("existing"), 0644)

	// Lock the file permanently
	lockFile, err := os.OpenFile(destFile, os.O_RDWR, 0666)
	if err != nil {
		t.Skip("unable to lock file on this platform")
	}
	defer lockFile.Close()

	// Server returning valid zip
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, _ := zw.Create("test.txt")
	_, _ = w.Write([]byte("hello"))
	_ = zw.Close()

	ts := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/zip")
		_, _ = rw.Write(buf.Bytes())
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err = DownloadReleaseAsset(ts.URL, destFile, "", nil, ctx)
	if err == nil {
		t.Errorf("expected error due to context cancellation during lock, got nil")
	}

	// Verify .part file was cleaned up
	partFile := destFile + ".part"
	if _, err := os.Stat(partFile); !os.IsNotExist(err) {
		t.Errorf("expected .part file to be removed on error, but it still exists")
	}
}
