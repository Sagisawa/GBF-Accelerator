package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearAll_PreservesNonCacheFilesAndSystemJunk(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	// 1. Create legitimate cache asset with paired .ext file
	headers := map[string]string{
		"content-type": "image/png",
		"etag":         `"test-etag"`,
	}
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", headers, validPng) {
		t.Fatalf("failed to save valid cache asset")
	}

	// 2. Create user non-cache files that must NOT be deleted
	userDocPath := filepath.Join(tmpDir, "doc.txt")
	if err := os.WriteFile(userDocPath, []byte("important user document"), 0644); err != nil {
		t.Fatalf("failed to create user doc.txt: %v", err)
	}

	userNotesPath := filepath.Join(tmpDir, "notes.pdf")
	if err := os.WriteFile(userNotesPath, []byte("%PDF-1.4 user notes"), 0644); err != nil {
		t.Fatalf("failed to create user notes.pdf: %v", err)
	}

	nestedUserDir := filepath.Join(tmpDir, "personal_data")
	if err := os.MkdirAll(nestedUserDir, 0755); err != nil {
		t.Fatalf("failed to create nested user dir: %v", err)
	}
	nestedFilePath := filepath.Join(nestedUserDir, "report.docx")
	if err := os.WriteFile(nestedFilePath, []byte("nested user document"), 0644); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}

	// 3. Create system junk files that must remain untouched
	dsStorePath := filepath.Join(tmpDir, ".DS_Store")
	if err := os.WriteFile(dsStorePath, []byte("fake .DS_Store"), 0644); err != nil {
		t.Fatalf("failed to create .DS_Store: %v", err)
	}

	thumbsDbPath := filepath.Join(tmpDir, "Thumbs.db")
	if err := os.WriteFile(thumbsDbPath, []byte("fake Thumbs.db"), 0644); err != nil {
		t.Fatalf("failed to create Thumbs.db: %v", err)
	}

	desktopIniPath := filepath.Join(tmpDir, "desktop.ini")
	if err := os.WriteFile(desktopIniPath, []byte("fake desktop.ini"), 0644); err != nil {
		t.Fatalf("failed to create desktop.ini: %v", err)
	}

	// 4. Run ClearAll
	deleted, freed, skipped := mgr.ClearAll()

	// 5. Assertions:
	// Legit cache file and its .ext metadata should be deleted
	cacheFilePath, _ := mgr.resolvePathWithNamespace("gbf", "assets/12345/img/sp/btn.png")
	if _, err := os.Stat(cacheFilePath); !os.IsNotExist(err) {
		t.Errorf("expected cache asset %s to be deleted, err: %v", cacheFilePath, err)
	}
	if _, err := os.Stat(cacheFilePath + ".ext"); !os.IsNotExist(err) {
		t.Errorf("expected cache metadata %s.ext to be deleted, err: %v", cacheFilePath, err)
	}

	if deleted < 2 {
		t.Errorf("expected at least 2 deleted files (asset + ext), got deleted=%d", deleted)
	}
	if freed <= 0 {
		t.Errorf("expected positive freed bytes, got freed=%d", freed)
	}
	if skipped < 3 {
		t.Errorf("expected at least 3 skipped non-cache user files, got skipped=%d", skipped)
	}

	// User files MUST NOT be deleted
	if _, err := os.Stat(userDocPath); err != nil {
		t.Errorf("BUG DETECTED: user file %s was deleted by ClearAll: %v", userDocPath, err)
	}
	if _, err := os.Stat(userNotesPath); err != nil {
		t.Errorf("BUG DETECTED: user file %s was deleted by ClearAll: %v", userNotesPath, err)
	}
	if _, err := os.Stat(nestedFilePath); err != nil {
		t.Errorf("BUG DETECTED: nested user file %s was deleted by ClearAll: %v", nestedFilePath, err)
	}

	// System junk files MUST NOT be deleted
	if _, err := os.Stat(dsStorePath); err != nil {
		t.Errorf("BUG DETECTED: system file %s was deleted by ClearAll: %v", dsStorePath, err)
	}
	if _, err := os.Stat(thumbsDbPath); err != nil {
		t.Errorf("BUG DETECTED: system file %s was deleted by ClearAll: %v", thumbsDbPath, err)
	}
	if _, err := os.Stat(desktopIniPath); err != nil {
		t.Errorf("BUG DETECTED: system file %s was deleted by ClearAll: %v", desktopIniPath, err)
	}

	// Empty cache subdirectory assets/12345/img/sp should be pruned
	prunedDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	if _, err := os.Stat(prunedDir); !os.IsNotExist(err) {
		t.Errorf("expected empty cache directory %s to be pruned", prunedDir)
	}

	// Non-empty user directory personal_data MUST NOT be pruned
	if _, err := os.Stat(nestedUserDir); err != nil {
		t.Errorf("expected non-empty user directory %s to remain, err: %v", nestedUserDir, err)
	}
}

func TestClearAll_LegacyAttributionWithAndWithoutMarker(t *testing.T) {
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Subtest 1: Without root marker -> legacy file without .ext is NOT deleted
	t.Run("without root marker", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
		_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
		_ = os.WriteFile(legacyFile, validPng, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files without root marker, got %d", deleted)
		}
		if skipped != 1 {
			t.Errorf("expected 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(legacyFile); err != nil {
			t.Errorf("expected legacy file to be preserved without root marker, got: %v", err)
		}
	})

	// Subtest 2: With root marker -> legacy file without .ext IS deleted
	t.Run("with root marker", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		_ = EnsureRootMarker(tmpDir)

		legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
		_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
		_ = os.WriteFile(legacyFile, validPng, 0644)

		deleted, _, _ := mgr.ClearAll()
		if deleted != 1 {
			t.Errorf("expected 1 deleted file with root marker, got %d", deleted)
		}
		if _, err := os.Stat(legacyFile); !os.IsNotExist(err) {
			t.Errorf("expected legacy file to be deleted with root marker, got err: %v", err)
		}

		// Root marker itself must still exist!
		if !HasRootMarker(tmpDir) {
			t.Errorf("expected root marker to remain intact after ClearAll")
		}
	})
}

func TestClearAll_RefusesUnsafeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("skipping user home test due to empty home")
	}

	mgr := NewManager(home, 64)
	defer mgr.Close()

	// ClearAll on user home must refuse destructive deletion
	deleted, freed, skipped := mgr.ClearAll()
	if deleted != 0 || freed != 0 || skipped != 0 {
		t.Errorf("expected ClearAll on user home to immediately return (0, 0, 0), got (%d, %d, %d)", deleted, freed, skipped)
	}
}
