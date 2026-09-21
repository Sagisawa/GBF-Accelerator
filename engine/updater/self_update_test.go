package updater

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestIsSafeZipPath(t *testing.T) {
	allowed := []string{
		"GBF_Accelerator.exe",
		"GBF_Accelerator.app/Contents/MacOS/GBF_Accelerator",
		"GBF_Accelerator.app/Contents/Resources/icon.icns",
	}
	for _, name := range allowed {
		if !isSafeZipPath(name) {
			t.Errorf("expected safe path: %q", name)
		}
	}

	blocked := []string{
		"../evil.exe",
		"GBF_Accelerator.app/../../evil",
		"/absolute/path",
		"C:/evil.exe",
	}
	for _, name := range blocked {
		if isSafeZipPath(name) {
			t.Errorf("expected blocked path: %q", name)
		}
	}
}

func TestExtractWindowsExecutable(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	f, err := zw.Create("GBF_Accelerator.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("fake-pe")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	archive := filepath.Join(tempDir, "release.zip")
	if err := os.WriteFile(archive, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	stage, err := extractWindowsExecutable(archive, tempDir)
	if err != nil {
		t.Fatalf("extractWindowsExecutable failed: %v", err)
	}
	defer os.Remove(stage)

	got, err := os.ReadFile(stage)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fake-pe" {
		t.Fatalf("unexpected extracted data: %q", string(got))
	}
}

func TestVerifyArchiveSHA256(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "archive.zip")
	data := []byte("update archive")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])

	if err := verifyArchiveSHA256(path, hash); err != nil {
		t.Fatalf("expected matching checksum, got %v", err)
	}
	if err := verifyArchiveSHA256(path, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}
