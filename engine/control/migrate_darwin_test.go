//go:build darwin

package control

import (
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

	userFile := filepath.Join(root, "assets", "personal_data", "profile.png")
	if err := os.MkdirAll(filepath.Dir(userFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userFile, png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("migration must reject a user file in an unrecognized directory")
	}
	if err := os.RemoveAll(filepath.Join(root, "assets", "personal_data")); err != nil {
		t.Fatal(err)
	}

	orphan := filepath.Join(root, "assets", "img", "sp", "ui", "orphan.png.ext")
	if err := os.WriteFile(orphan, []byte(`{"v":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateCacheMigrationTree(root); err == nil {
		t.Fatal("migration must reject an orphan .ext sidecar")
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
