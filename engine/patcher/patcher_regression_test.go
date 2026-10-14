package patcher

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultComponentSpecs_UsesAssetsTag(t *testing.T) {
	specs := GetDefaultComponentSpecs()
	found := false
	for _, spec := range specs {
		if spec.ID == "module" {
			found = true
			if !strings.Contains(spec.Description, AssetsTag) {
				t.Fatalf("expected module description to contain %s, got %q", AssetsTag, spec.Description)
			}
		}
	}
	if !found {
		t.Fatal("module spec not found in GetDefaultComponentSpecs")
	}
}

func TestValidateHostAppApk_RejectsInvalidOrNonHostApk(t *testing.T) {
	tempDir := t.TempDir()
	fakeApk := filepath.Join(tempDir, "fake-host.apk")
	// Write > 1 MiB of non-APK bytes so size check passes but manifest validation fails
	largeBytes := make([]byte, 1024*1024+128)
	if err := os.WriteFile(fakeApk, largeBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHostAppApk(fakeApk); err == nil {
		t.Fatal("expected ValidateHostAppApk to reject non-APK file")
	}
}

func TestUnzipArchive_SkipsSymlinks(t *testing.T) {
	tempDir := t.TempDir()
	zipPath := filepath.Join(tempDir, "bundle.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)

	symHdr := &zip.FileHeader{
		Name:   "evil_link.apk",
		Method: zip.Deflate,
	}
	symHdr.SetMode(os.ModeSymlink | 0777)
	wSym, err := zw.CreateHeader(symHdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wSym.Write([]byte("/etc/passwd"))

	regHdr := &zip.FileHeader{
		Name:   "base.apk",
		Method: zip.Deflate,
	}
	regHdr.SetMode(0644)
	wReg, err := zw.CreateHeader(regHdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = wReg.Write([]byte("regular-apk-bytes"))

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	destDir := filepath.Join(tempDir, "extracted")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := unzipArchive(zipPath, destDir); err != nil {
		t.Fatalf("unzipArchive failed: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(destDir, "evil_link.apk")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink entry to be skipped, got stat err: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destDir, "base.apk"))
	if err != nil || string(got) != "regular-apk-bytes" {
		t.Fatalf("unexpected regular file content: %q (err=%v)", string(got), err)
	}
}
