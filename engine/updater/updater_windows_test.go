//go:build windows

package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDownloadReleaseAsset_RetryContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	destFile := filepath.Join(tempDir, "lock_cancel.zip")
	_ = os.WriteFile(destFile, []byte("existing"), 0644)

	// Lock the file permanently using Windows exclusive open
	lockFile, err := os.OpenFile(destFile, os.O_RDWR, 0666)
	if err != nil {
		t.Skip("unable to lock file on this platform")
	}
	defer lockFile.Close()

	// Explicitly prove that destination file cannot be renamed/replaced due to Windows sharing violation
	probeFile := filepath.Join(tempDir, "probe.zip")
	if rErr := os.Rename(destFile, probeFile); rErr == nil {
		_ = os.Remove(probeFile)
		t.Fatal("expected rename to fail while destination file is held open on Windows")
	}

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

	_, err = DownloadReleaseAsset(ts.URL, destFile, "", "", nil, ctx)
	if err == nil {
		t.Errorf("expected error due to context cancellation during lock, got nil")
	}

	// Verify .part file was cleaned up
	partFile := destFile + ".part"
	if _, err := os.Stat(partFile); !os.IsNotExist(err) {
		t.Errorf("expected .part file to be removed on error, but it still exists")
	}
}
