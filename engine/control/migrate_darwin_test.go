//go:build darwin

package control

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gbf-proxy/cache"
)

func setMigrationTargetForTest(t *testing.T, path string) {
	t.Helper()
	previous := standardCacheDirProvider
	standardCacheDirProvider = func() string { return path }
	t.Cleanup(func() { standardCacheDirProvider = previous })
}

func TestValidateCacheMigrationTreeRejectsUnconfirmedFiles(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := cache.EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	asset := filepath.Join(root, "assets", "img", "sp", "ui", "known.png")
	if err := os.MkdirAll(filepath.Dir(asset), 0755); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRmigration")
	if err := os.WriteFile(asset, png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("confirmed Tier-2 source tree should pass: %v", err)
	}

	userFile := filepath.Join(root, "personal_data", "profile.png")
	if err := os.MkdirAll(filepath.Dir(userFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("migration must reject a user file in an unrecognized directory")
	}
	if err := os.RemoveAll(filepath.Join(root, "personal_data")); err != nil {
		t.Fatal(err)
	}

	orphan := filepath.Join(root, "assets", "img", "sp", "ui", "orphan.png.ext")
	if err := os.WriteFile(orphan, []byte(`{"v":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("migration must allow orphan .ext sidecar without blocking: %v", err)
	}
	if err := os.Remove(orphan); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(root, "assets", "img", "sp", "ui", "linked.png")
	if err := os.Symlink(asset, link); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("migration must reject symbolic links")
	}
}

func TestValidateEmptyMigrationTargetRejectsAnyExistingEntry(t *testing.T) {
	target := t.TempDir()
	if err := validateEmptyMigrationTarget(target); err != nil {
		t.Fatalf("empty target should pass: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, ".DS_Store"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateEmptyMigrationTarget(target); err == nil {
		t.Fatal("migration must not replace an existing target containing even system metadata")
	}
}

// TestValidateCacheMigrationTree_Over50FilesBoundary verifies that Darwin cache migration
// samples at most 50 legacy files for magic byte validation, while enforcing directory structure
// and asset file extensions across ALL files (including files beyond the 50-file threshold).
// It also verifies that residual temporary, quarantine, and orphan .ext files do not block migration.
func TestValidateCacheMigrationTree_Over50FilesBoundary(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := cache.EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	pngMagic := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x01}
	uiDir := filepath.Join(root, "assets", "img", "sp", "ui")
	if err := os.MkdirAll(uiDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create 60 valid legacy cache files without .ext
	for i := 1; i <= 60; i++ {
		p := filepath.Join(uiDir, fmt.Sprintf("item_%02d.png", i))
		if err := os.WriteFile(p, pngMagic, 0644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. All 60 valid files: validation must succeed
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("expected 60 valid legacy files to pass migration validation: %v", err)
	}

	// 2. Add residual temporary streaming file, quarantine file, and orphan .ext: must NOT block
	tempFile := filepath.Join(uiDir, "item_temp.png.tmp.1234.1700000000.1")
	quarFile := filepath.Join(uiDir, "item_quar.png.quarantine.1700000000")
	orphanExt := filepath.Join(uiDir, "item_orphan.png.ext")
	for _, f := range []string{tempFile, quarFile, orphanExt} {
		if err := os.WriteFile(f, []byte("junk"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("expected residual temp, quarantine, and orphan .ext not to block migration: %v", err)
	}
	for _, f := range []string{tempFile, quarFile, orphanExt} {
		_ = os.Remove(f)
	}

	// 3. 55th file (beyond 50 sample threshold) with invalid asset extension -> must fail
	p55 := filepath.Join(uiDir, "item_55.png")
	p55Docx := filepath.Join(uiDir, "item_55.docx")
	if err := os.Rename(p55, p55Docx); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("expected migration validation to fail when 55th file has an invalid asset extension")
	}
	if err := os.Rename(p55Docx, p55); err != nil {
		t.Fatal(err)
	}

	// 4. 55th file in an unconfirmed directory structure -> must fail
	personalDir := filepath.Join(root, "personal_notes")
	if err := os.MkdirAll(personalDir, 0755); err != nil {
		t.Fatal(err)
	}
	p55Personal := filepath.Join(personalDir, "item_55.png")
	if err := os.Rename(p55, p55Personal); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("expected migration validation to fail when 55th file is in an unconfirmed directory")
	}
	if err := os.Rename(p55Personal, p55); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(personalDir)

	// 5. 55th file is empty (size == 0) -> must fail
	if err := os.WriteFile(p55, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("expected migration validation to fail when 55th file is empty")
	}
	if err := os.WriteFile(p55, pngMagic, 0644); err != nil {
		t.Fatal(err)
	}

	// 6. Restored 60 files must pass cleanly
	if err := validateCacheMigrationTree(root); err != nil {
		t.Fatalf("expected restored 60 files to pass: %v", err)
	}
}
