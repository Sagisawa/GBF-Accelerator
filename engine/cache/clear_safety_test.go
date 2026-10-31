package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// User files with .tmp. in name (must NOT be misidentified as accelerator temp files)
	userTmpDocPath := filepath.Join(tmpDir, "draft.tmp.txt")
	if err := os.WriteFile(userTmpDocPath, []byte("important user draft with tmp in name"), 0644); err != nil {
		t.Fatalf("failed to create draft.tmp.txt: %v", err)
	}
	nestedTmpDocPath := filepath.Join(nestedUserDir, "notes.tmp.pdf")
	if err := os.WriteFile(nestedTmpDocPath, []byte("user notes with tmp in name"), 0644); err != nil {
		t.Fatalf("failed to create notes.tmp.pdf: %v", err)
	}

	// User files with .ext in name but not accelerator cache assets
	userExtPath := filepath.Join(tmpDir, "user_config.ext")
	if err := os.WriteFile(userExtPath, []byte(`{"v":1,"custom_user_setting":true}`), 0644); err != nil {
		t.Fatalf("failed to create user_config.ext: %v", err)
	}

	// Legitimate stale accelerator temp file that SHOULD be removed
	staleTmpPath := filepath.Join(tmpDir, "assets", "12345", "img", "sp", "stale.png.tmp.9999.123456789.1")
	if err := os.WriteFile(staleTmpPath, []byte("stale temp data"), 0644); err != nil {
		t.Fatalf("failed to create stale accelerator temp file: %v", err)
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
	// Legit cache file and its .ext metadata and stale temp file should be deleted
	cacheFilePath, _ := mgr.resolvePathWithNamespace("gbf", "assets/12345/img/sp/btn.png")
	if _, err := os.Stat(cacheFilePath); !os.IsNotExist(err) {
		t.Errorf("expected cache asset %s to be deleted, err: %v", cacheFilePath, err)
	}
	if _, err := os.Stat(cacheFilePath + ".ext"); !os.IsNotExist(err) {
		t.Errorf("expected cache metadata %s.ext to be deleted, err: %v", cacheFilePath, err)
	}
	if _, err := os.Stat(staleTmpPath); !os.IsNotExist(err) {
		t.Errorf("expected stale accelerator temp file %s to be deleted, err: %v", staleTmpPath, err)
	}

	if deleted < 3 {
		t.Errorf("expected at least 3 deleted files (asset + ext + stale tmp), got deleted=%d", deleted)
	}
	if freed <= 0 {
		t.Errorf("expected positive freed bytes, got freed=%d", freed)
	}
	if skipped < 5 {
		t.Errorf("expected at least 5 skipped non-cache user files, got skipped=%d", skipped)
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
	if _, err := os.Stat(userTmpDocPath); err != nil {
		t.Errorf("BUG DETECTED: user file with .tmp. %s was deleted by ClearAll: %v", userTmpDocPath, err)
	}
	if _, err := os.Stat(nestedTmpDocPath); err != nil {
		t.Errorf("BUG DETECTED: nested user file with .tmp. %s was deleted by ClearAll: %v", nestedTmpDocPath, err)
	}
	if _, err := os.Stat(userExtPath); err != nil {
		t.Errorf("BUG DETECTED: user file with .ext %s was deleted by ClearAll: %v", userExtPath, err)
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
		_ = os.Remove(filepath.Join(tmpDir, RootMarkerFileName))

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

// 1. 原本为空的用户目录，执行 ClearAll 后必须保留。
func TestClearAll_PreservesPreExistingEmptyUserDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	// Legitimate cache file that will be deleted
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	headers := map[string]string{"content-type": "image/png"}
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", headers, validPng) {
		t.Fatalf("failed to save cache asset")
	}

	// Pre-existing empty user directories
	emptyPersonal := filepath.Join(tmpDir, "personal_data")
	emptyWorkspace := filepath.Join(tmpDir, "user_workspace")
	nestedEmptyParent := filepath.Join(tmpDir, "custom_project")
	nestedEmptyChild := filepath.Join(nestedEmptyParent, "empty_sub")

	if err := os.MkdirAll(emptyPersonal, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.MkdirAll(emptyWorkspace, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.MkdirAll(nestedEmptyChild, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	deleted, _, _ := mgr.ClearAll()
	if deleted < 2 {
		t.Errorf("expected at least 2 deleted cache items (asset + ext), got %d", deleted)
	}

	// Pre-existing empty user directories MUST be preserved!
	if fi, err := os.Stat(emptyPersonal); err != nil || !fi.IsDir() {
		t.Errorf("BUG: pre-existing empty directory %s was deleted by ClearAll: %v", emptyPersonal, err)
	}
	if fi, err := os.Stat(emptyWorkspace); err != nil || !fi.IsDir() {
		t.Errorf("BUG: pre-existing empty directory %s was deleted by ClearAll: %v", emptyWorkspace, err)
	}
	if fi, err := os.Stat(nestedEmptyChild); err != nil || !fi.IsDir() {
		t.Errorf("BUG: pre-existing empty directory %s was deleted by ClearAll: %v", nestedEmptyChild, err)
	}
	if fi, err := os.Stat(nestedEmptyParent); err != nil || !fi.IsDir() {
		t.Errorf("BUG: pre-existing empty directory %s was deleted by ClearAll: %v", nestedEmptyParent, err)
	}

	// Empty cache subdirectory should be pruned
	prunedCacheDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	if _, err := os.Stat(prunedCacheDir); !os.IsNotExist(err) {
		t.Errorf("expected empty cache directory %s to be pruned", prunedCacheDir)
	}
}

// 2. personal_data/photo.png.tmp.123.456 必须保留。
func TestClearAll_PreservesUserTempFilePattern(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	userDir := filepath.Join(tmpDir, "personal_data")
	_ = os.MkdirAll(userDir, 0755)

	// User file matching .tmp. with 2 numeric fields
	userPhotoTmp := filepath.Join(userDir, "photo.png.tmp.123.456")
	if err := os.WriteFile(userPhotoTmp, []byte("user photo temp data"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// User file matching .tmp. with 3 numeric fields in non-cache dir
	userPhotoTmp3 := filepath.Join(userDir, "photo.png.tmp.123.456.789")
	if err := os.WriteFile(userPhotoTmp3, []byte("user photo temp data 3"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Also add a legit cache asset so ClearAll does real work
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	_ = mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", map[string]string{"content-type": "image/png"}, validPng)

	deleted, _, skipped := mgr.ClearAll()
	if deleted < 2 {
		t.Errorf("expected cache asset and ext to be deleted, got deleted=%d", deleted)
	}
	if skipped < 2 {
		t.Errorf("expected at least 2 skipped user temp files, got skipped=%d", skipped)
	}

	// User temp files MUST NOT be deleted!
	if _, err := os.Stat(userPhotoTmp); err != nil {
		t.Errorf("BUG: user temp file %s was deleted by ClearAll: %v", userPhotoTmp, err)
	}
	if _, err := os.Stat(userPhotoTmp3); err != nil {
		t.Errorf("BUG: user temp file %s was deleted by ClearAll: %v", userPhotoTmp3, err)
	}
	if _, err := os.Stat(userDir); err != nil {
		t.Errorf("BUG: user directory %s was deleted by ClearAll: %v", userDir, err)
	}
}

// 3. personal_data/photo.png.ext 必须保留。
func TestClearAll_PreservesUserExtFile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	userDir := filepath.Join(tmpDir, "personal_data")
	_ = os.MkdirAll(userDir, 0755)

	userPhoto := filepath.Join(userDir, "photo.png")
	_ = os.WriteFile(userPhoto, []byte("user photo contents"), 0644)

	// User .ext file that has valid JSON metadata
	userPhotoExt := filepath.Join(userDir, "photo.png.ext")
	validExtJSON := []byte(`{"v":1,"size":19,"mtime_nano":123456789,"ns":"gbf"}`)
	if err := os.WriteFile(userPhotoExt, validExtJSON, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Also add an orphan .ext in another user directory
	userWorkspaceDir := filepath.Join(tmpDir, "user_workspace")
	_ = os.MkdirAll(userWorkspaceDir, 0755)
	orphanUserExt := filepath.Join(userWorkspaceDir, "document.png.ext")
	_ = os.WriteFile(orphanUserExt, validExtJSON, 0644)

	_, _, skipped := mgr.ClearAll()
	if skipped < 3 {
		t.Errorf("expected at least 3 skipped user files, got skipped=%d", skipped)
	}

	// User files and .ext files in user directories MUST NOT be deleted!
	if _, err := os.Stat(userPhoto); err != nil {
		t.Errorf("BUG: user photo %s was deleted by ClearAll: %v", userPhoto, err)
	}
	if _, err := os.Stat(userPhotoExt); err != nil {
		t.Errorf("BUG: user ext file %s was deleted by ClearAll: %v", userPhotoExt, err)
	}
	if _, err := os.Stat(orphanUserExt); err != nil {
		t.Errorf("BUG: user orphan ext file %s was deleted by ClearAll: %v", orphanUserExt, err)
	}
}

// 4. 指向外部文件的符号链接不能被当作缓存删除。
func TestClearAll_RejectsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	extDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	externalPng := filepath.Join(extDir, "important_secret.png")
	if err := os.WriteFile(externalPng, validPng, 0644); err != nil {
		t.Fatalf("write external file failed: %v", err)
	}

	cacheSubDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	if err := os.MkdirAll(cacheSubDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 1. Symlink pointing to external PNG
	symlinkPng := filepath.Join(cacheSubDir, "symlink_asset.png")
	if err := os.Symlink(externalPng, symlinkPng); err != nil {
		t.Skipf("skipping symlink test due to OS permission: %v", err)
	}

	// 2. Symlink disguised as accelerator temp file
	symlinkTmp := filepath.Join(cacheSubDir, "fake.png.tmp.1234.5678.1")
	_ = os.Symlink(externalPng, symlinkTmp)

	// 3. Symlink disguised as .ext metadata file
	symlinkExt := filepath.Join(cacheSubDir, "symlink_asset.png.ext")
	_ = os.Symlink(externalPng, symlinkExt)

	_ = EnsureRootMarker(tmpDir)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Errorf("expected 0 deleted files for symlinks, got %d", deleted)
	}
	if skipped < 3 {
		t.Errorf("expected at least 3 skipped symlink entries, got %d", skipped)
	}

	// External file MUST remain intact
	if content, err := os.ReadFile(externalPng); err != nil || len(content) != len(validPng) {
		t.Errorf("BUG: external file was deleted or corrupted by ClearAll: %v", err)
	}

	// Symlinks MUST NOT be removed
	if _, err := os.Lstat(symlinkPng); err != nil {
		t.Errorf("BUG: symlink %s was deleted by ClearAll: %v", symlinkPng, err)
	}
	if _, err := os.Lstat(symlinkTmp); err != nil {
		t.Errorf("BUG: symlink %s was deleted by ClearAll: %v", symlinkTmp, err)
	}
	if _, err := os.Lstat(symlinkExt); err != nil {
		t.Errorf("BUG: symlink %s was deleted by ClearAll: %v", symlinkExt, err)
	}
}

// 5. 验证过程中替换文件对象，必须拒绝删除。
func TestClearAll_RejectsReplacedFileObject(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	headers := map[string]string{"content-type": "image/png"}
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/replace_me.png", headers, validPng) {
		t.Fatalf("failed to save cache asset")
	}

	targetFullPath, _ := mgr.resolvePathWithNamespace("gbf", "assets/12345/img/sp/replace_me.png")

	// Set hook to simulate TOCTOU replacement immediately after verification
	mgr.SetBeforeClearAllRemoveHookForTest(func(relPath string) {
		if strings.HasSuffix(filepath.ToSlash(relPath), "replace_me.png") {
			_ = os.Remove(targetFullPath)
			_ = os.WriteFile(targetFullPath, []byte("attacker replaced content"), 0644)
		}
	})
	defer func() {
		mgr.SetBeforeClearAllRemoveHookForTest(nil)
	}()

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Errorf("expected 0 deleted files when object replaced, got %d", deleted)
	}
	if skipped < 1 {
		t.Errorf("expected at least 1 skipped file due to identity mismatch, got %d", skipped)
	}

	// Replaced file must NOT be deleted
	if content, err := os.ReadFile(targetFullPath); err != nil || string(content) != "attacker replaced content" {
		t.Errorf("expected replaced file to remain untouched, got err=%v, content=%q", err, string(content))
	}
}

// 6. 非缓存目录中的空目录和系统垃圾必须保留。
func TestClearAll_PreservesNonCacheEmptySubdirsAndSystemJunk(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	userFolder := filepath.Join(tmpDir, "user_folder")
	emptySub := filepath.Join(userFolder, "empty_sub")
	if err := os.MkdirAll(emptySub, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	dsStore := filepath.Join(userFolder, ".DS_Store")
	_ = os.WriteFile(dsStore, []byte("fake .DS_Store"), 0644)
	thumbsDb := filepath.Join(userFolder, "Thumbs.db")
	_ = os.WriteFile(thumbsDb, []byte("fake Thumbs.db"), 0644)
	desktopIni := filepath.Join(userFolder, "desktop.ini")
	_ = os.WriteFile(desktopIni, []byte("fake desktop.ini"), 0644)

	// Also add a legitimate cache file so ClearAll runs real cleanup
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	_ = mgr.SaveWithNamespace("gbf", "assets/1000/sound/se.png", map[string]string{"content-type": "image/png"}, validPng)

	deleted, _, _ := mgr.ClearAll()
	if deleted < 2 {
		t.Errorf("expected legitimate cache files to be deleted, got %d", deleted)
	}

	// Non-cache empty subdirectory and system junk MUST be preserved!
	if fi, err := os.Stat(emptySub); err != nil || !fi.IsDir() {
		t.Errorf("BUG: non-cache empty directory %s was deleted: %v", emptySub, err)
	}
	if _, err := os.Stat(dsStore); err != nil {
		t.Errorf("BUG: system junk %s was deleted: %v", dsStore, err)
	}
	if _, err := os.Stat(thumbsDb); err != nil {
		t.Errorf("BUG: system junk %s was deleted: %v", thumbsDb, err)
	}
	if _, err := os.Stat(desktopIni); err != nil {
		t.Errorf("BUG: system junk %s was deleted: %v", desktopIni, err)
	}
	if fi, err := os.Stat(userFolder); err != nil || !fi.IsDir() {
		t.Errorf("BUG: non-cache directory %s was deleted: %v", userFolder, err)
	}
}

// 7. 即使合法资源存在伴生 .ext 文件，但如果该 .ext 为非 Accelerator 元数据（如用户笔记），必须保留该 .ext 并保留所在目录。
func TestClearAll_PreservesUserInvalidExtAdjacentToLegacyAsset(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	pngPath := filepath.Join(subDir, "btn.png")
	if err := os.WriteFile(pngPath, validPng, 0644); err != nil {
		t.Fatalf("write png failed: %v", err)
	}

	// User note file disguised with .ext suffix adjacent to legacy png
	userNotePath := filepath.Join(subDir, "btn.png.ext")
	userNoteContent := []byte("confidential user notes: not json metadata")
	if err := os.WriteFile(userNotePath, userNoteContent, 0644); err != nil {
		t.Fatalf("write user note failed: %v", err)
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Errorf("expected 0 deleted files (both btn.png and btn.png.ext preserved), got %d", deleted)
	}
	if skipped < 2 {
		t.Errorf("expected at least 2 skipped files (btn.png and btn.png.ext), got %d", skipped)
	}

	// btn.png must be preserved because companion .ext is invalid / untrusted
	if _, err := os.Stat(pngPath); err != nil {
		t.Errorf("BUG: file %s was deleted despite invalid companion .ext: %v", pngPath, err)
	}

	// btn.png.ext MUST be preserved
	if data, err := os.ReadFile(userNotePath); err != nil || string(data) != string(userNoteContent) {
		t.Errorf("BUG: user note %s was deleted or corrupted: %v, content=%q", userNotePath, err, string(data))
	}

	// Directory must be preserved since it still contains userNotePath
	if fi, err := os.Stat(subDir); err != nil || !fi.IsDir() {
		t.Errorf("BUG: directory %s containing user file was deleted: %v", subDir, err)
	}
}

// 8. 干净缓存清空时，删除的配对 .ext 不应误记入 skippedFiles。
func TestClearAll_CleanCacheNoFalseSkippedCount(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	headers := map[string]string{"content-type": "image/png"}
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/clean.png", headers, validPng) {
		t.Fatalf("failed to save cache asset")
	}

	deleted, freed, skipped := mgr.ClearAll()
	if deleted != 2 {
		t.Errorf("expected exactly 2 deleted items (data + paired ext), got deleted=%d", deleted)
	}
	if freed <= 0 {
		t.Errorf("expected positive freed bytes, got %d", freed)
	}
	if skipped != 0 {
		t.Errorf("expected 0 skipped files for a purely clean cache, got skipped=%d", skipped)
	}
}

// 9. 用户在非缓存路径或使用非素材扩展名创建的文件，执行 ClearAll 后必须保留。
func TestClearAll_PreservesUserFileUnderAssetsPersonalData(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// 1. User photo in personal_data/ without .ext (non-cache top-level shape)
	userPhotoDir := filepath.Join(tmpDir, "personal_data")
	if err := os.MkdirAll(userPhotoDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	userPhoto := filepath.Join(userPhotoDir, "photo.png")
	if err := os.WriteFile(userPhoto, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 2. User photo in personal_data/ WITH a valid paired .ext JSON metadata (still rejected due to non-cache top-level path)
	userPhoto2 := filepath.Join(userPhotoDir, "photo2.png")
	if err := os.WriteFile(userPhoto2, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	fi2, _ := os.Stat(userPhoto2)
	userPhoto2Ext := userPhoto2 + ".ext"
	meta2 := fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi2.Size(), fi2.ModTime().UnixNano())
	if err := os.WriteFile(userPhoto2Ext, []byte(meta2), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 3. User photo in user_workspace/personal_data/
	userPhotoVerDir := filepath.Join(tmpDir, "user_workspace", "personal_data")
	if err := os.MkdirAll(userPhotoVerDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	userPhotoVer := filepath.Join(userPhotoVerDir, "photo.png")
	if err := os.WriteFile(userPhotoVer, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 4. User document in assets/12345/document.docx (non-asset extension)
	userBareVerFile := filepath.Join(tmpDir, "assets", "12345", "document.docx")
	if err := os.MkdirAll(filepath.Dir(userBareVerFile), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(userBareVerFile, []byte("docx content"), 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 5. User file in 12345/personal/test.png (bare version folder with invalid category)
	userBareRootDir := filepath.Join(tmpDir, "12345", "personal")
	_ = os.MkdirAll(userBareRootDir, 0755)
	userBareRootFile := filepath.Join(userBareRootDir, "test.png")
	if err := os.WriteFile(userBareRootFile, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 6. Legitimate cache file that should be deleted
	headers := map[string]string{"content-type": "image/png"}
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/clean.png", headers, validPng) {
		t.Fatalf("save cache asset failed")
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 2 {
		t.Errorf("expected 2 deleted files (clean.png + clean.png.ext), got %d", deleted)
	}
	if skipped < 5 {
		t.Errorf("expected at least 5 skipped user files, got %d", skipped)
	}

	// personal_data/photo.png and photo2.png must remain intact
	if _, err := os.Stat(userPhoto); err != nil {
		t.Errorf("BUG: user photo %s was deleted by ClearAll: %v", userPhoto, err)
	}
	if _, err := os.Stat(userPhoto2); err != nil {
		t.Errorf("BUG: user photo with paired .ext %s was deleted: %v", userPhoto2, err)
	}
	if _, err := os.Stat(userPhoto2Ext); err != nil {
		t.Errorf("BUG: user photo .ext %s was deleted: %v", userPhoto2Ext, err)
	}
	if fi, err := os.Stat(userPhotoDir); err != nil || !fi.IsDir() {
		t.Errorf("BUG: user directory %s was pruned: %v", userPhotoDir, err)
	}

	// user_workspace/personal_data/photo.png must remain intact
	if _, err := os.Stat(userPhotoVer); err != nil {
		t.Errorf("BUG: user versioned photo %s was deleted by ClearAll: %v", userPhotoVer, err)
	}
	if fi, err := os.Stat(userPhotoVerDir); err != nil || !fi.IsDir() {
		t.Errorf("BUG: user directory %s was pruned: %v", userPhotoVerDir, err)
	}

	// assets/12345/document.docx must remain intact
	if _, err := os.Stat(userBareVerFile); err != nil {
		t.Errorf("BUG: user docx file %s was deleted by ClearAll: %v", userBareVerFile, err)
	}

	// 12345/personal/test.png must remain intact
	if _, err := os.Stat(userBareRootFile); err != nil {
		t.Errorf("BUG: user bare root file %s was deleted by ClearAll: %v", userBareRootFile, err)
	}
}

// 10. 伴生 .ext 元数据存在但 size、mtimeNano、版本、命名空间错误或内容损坏时，文件必须保留，绝对不能降级落入 Tier 2 删除。
func TestClearAll_PreservesFileWithMismatchedExtMetadata(t *testing.T) {
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Case A: Size mismatch - under simplified Tier-1 rules, size mismatch does NOT revoke cache attribution
	t.Run("size mismatch", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
		_ = os.MkdirAll(subDir, 0755)
		pngPath := filepath.Join(subDir, "wrong_size.png")
		_ = os.WriteFile(pngPath, validPng, 0644)
		fi, _ := os.Stat(pngPath)

		// Create paired .ext with incorrect size
		extPath := pngPath + ".ext"
		badMeta := []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size()+9999, fi.ModTime().UnixNano()))
		_ = os.WriteFile(extPath, badMeta, 0644)

		deleted, _, _ := mgr.ClearAll()
		if deleted != 2 {
			t.Errorf("expected 2 deleted files (asset + .ext) when size mismatches, got %d", deleted)
		}
		if _, err := os.Stat(pngPath); !os.IsNotExist(err) {
			t.Errorf("expected file %s with mismatched .ext size to be deleted by ClearAll: %v", pngPath, err)
		}
		if _, err := os.Stat(extPath); !os.IsNotExist(err) {
			t.Errorf("expected .ext %s to be deleted by ClearAll: %v", extPath, err)
		}
	})

	// Case B: MTimeNano mismatch - under simplified Tier-1 rules, mtime mismatch does NOT revoke cache attribution
	t.Run("mtime mismatch", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
		_ = os.MkdirAll(subDir, 0755)
		pngPath := filepath.Join(subDir, "wrong_mtime.png")
		_ = os.WriteFile(pngPath, validPng, 0644)
		fi, _ := os.Stat(pngPath)

		// Create paired .ext with incorrect mtimeNano
		extPath := pngPath + ".ext"
		badMeta := []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano()-999999))
		_ = os.WriteFile(extPath, badMeta, 0644)

		deleted, _, _ := mgr.ClearAll()
		if deleted != 2 {
			t.Errorf("expected 2 deleted files (asset + .ext) when mtime mismatches, got %d", deleted)
		}
		if _, err := os.Stat(pngPath); !os.IsNotExist(err) {
			t.Errorf("expected file %s with mismatched .ext mtime to be deleted by ClearAll: %v", pngPath, err)
		}
		if _, err := os.Stat(extPath); !os.IsNotExist(err) {
			t.Errorf("expected .ext %s to be deleted by ClearAll: %v", extPath, err)
		}
	})

	// Case C: Corrupted/non-JSON companion .ext -> MUST NOT fall through to Tier 2!
	t.Run("corrupted non-json ext", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
		_ = os.MkdirAll(subDir, 0755)
		pngPath := filepath.Join(subDir, "corrupted_ext.png")
		_ = os.WriteFile(pngPath, validPng, 0644)

		extPath := pngPath + ".ext"
		_ = os.WriteFile(extPath, []byte("broken non-json content"), 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when companion .ext is corrupted, got %d", deleted)
		}
		if skipped < 1 {
			t.Errorf("expected at least 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(pngPath); err != nil {
			t.Errorf("BUG: file %s with corrupted .ext was deleted: %v", pngPath, err)
		}
		if _, err := os.Stat(extPath); err != nil {
			t.Errorf("BUG: .ext %s was deleted: %v", extPath, err)
		}
	})

	// Case D: Empty 0-byte companion .ext -> MUST NOT fall through to Tier 2!
	t.Run("empty ext", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
		_ = os.MkdirAll(subDir, 0755)
		pngPath := filepath.Join(subDir, "empty_ext.png")
		_ = os.WriteFile(pngPath, validPng, 0644)

		extPath := pngPath + ".ext"
		_ = os.WriteFile(extPath, []byte{}, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when companion .ext is empty, got %d", deleted)
		}
		if skipped < 1 {
			t.Errorf("expected at least 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(pngPath); err != nil {
			t.Errorf("BUG: file %s with empty .ext was deleted: %v", pngPath, err)
		}
		if _, err := os.Stat(extPath); err != nil {
			t.Errorf("BUG: empty .ext %s was deleted: %v", extPath, err)
		}
	})

	// Case E: Version mismatch (v: 2) -> MUST NOT fall through to Tier 2!
	t.Run("version mismatch", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
		_ = os.MkdirAll(subDir, 0755)
		pngPath := filepath.Join(subDir, "wrong_v.png")
		_ = os.WriteFile(pngPath, validPng, 0644)
		fi, _ := os.Stat(pngPath)

		extPath := pngPath + ".ext"
		badMeta := []byte(fmt.Sprintf(`{"v":2,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano()))
		_ = os.WriteFile(extPath, badMeta, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when version mismatches, got %d", deleted)
		}
		if skipped < 1 {
			t.Errorf("expected at least 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(pngPath); err != nil {
			t.Errorf("BUG: file %s with mismatched version was deleted: %v", pngPath, err)
		}
	})

	// Case F: Namespace mismatch -> MUST NOT fall through to Tier 2!
	t.Run("namespace mismatch", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
		_ = os.MkdirAll(subDir, 0755)
		pngPath := filepath.Join(subDir, "wrong_ns.png")
		_ = os.WriteFile(pngPath, validPng, 0644)
		fi, _ := os.Stat(pngPath)

		extPath := pngPath + ".ext"
		badMeta := []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"other_host"}`, fi.Size(), fi.ModTime().UnixNano()))
		_ = os.WriteFile(extPath, badMeta, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when namespace mismatches, got %d", deleted)
		}
		if skipped < 1 {
			t.Errorf("expected at least 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(pngPath); err != nil {
			t.Errorf("BUG: file %s with mismatched namespace was deleted: %v", pngPath, err)
		}
	})
}

// 11. 对应资源不存在的孤立 .ext 文件，即使 JSON 格式正确、字段完整（如包含 verified=true、正确版本与非零字段），
// 亦不能证明由 Accelerator 生成（缺乏物理资产文件的大小、修改时间及魔数交叉比对凭据）。
// 依照保守原则，孤立 .ext 默认全部保留。
func TestClearAll_PreservesOrphanExtFilesEvenIfValidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	_ = os.MkdirAll(subDir, 0755)

	// Incomplete / forged orphan .ext files (no paired image file exists)
	forged1 := filepath.Join(subDir, "forged_no_verified.png.ext")
	_ = os.WriteFile(forged1, []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","ns":"gbf"}`), 0644)

	forged2 := filepath.Join(subDir, "forged_no_mtime.png.ext")
	_ = os.WriteFile(forged2, []byte(`{"v":1,"size":100,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`), 0644)

	forged3 := filepath.Join(subDir, "forged_no_size.png.ext")
	_ = os.WriteFile(forged3, []byte(`{"v":1,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`), 0644)

	forged4 := filepath.Join(subDir, "forged_no_at.png.ext")
	_ = os.WriteFile(forged4, []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`), 0644)

	forged5 := filepath.Join(subDir, "forged_no_ct.png.ext")
	_ = os.WriteFile(forged5, []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"verified":true,"ns":"gbf"}`), 0644)

	forged6 := filepath.Join(subDir, "forged_v2.png.ext")
	_ = os.WriteFile(forged6, []byte(`{"v":2,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`), 0644)

	// Orphan .ext in non-cache user path (even with complete valid JSON) -> MUST BE PRESERVED!
	userDir := filepath.Join(tmpDir, "personal_data")
	_ = os.MkdirAll(userDir, 0755)
	orphanUserPath := filepath.Join(userDir, "orphan_photo.png.ext")
	_ = os.WriteFile(orphanUserPath, []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`), 0644)

	// Handcrafted orphan .ext in canonical cache path with complete valid JSON (no paired image file exists) -> MUST BE PRESERVED!
	handcraftedOrphanGBF := filepath.Join(subDir, "handcrafted_orphan.png.ext")
	_ = os.WriteFile(handcraftedOrphanGBF, []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`), 0644)

	// Handcrafted orphan .ext in external host path with complete valid JSON (no paired file exists) -> MUST BE PRESERVED!
	hostsSubDir := filepath.Join(tmpDir, "hosts", "example.com", "assets", "12345", "img", "sp")
	_ = os.MkdirAll(hostsSubDir, 0755)
	handcraftedOrphanHosts := filepath.Join(hostsSubDir, "handcrafted_host.png.ext")
	_ = os.WriteFile(handcraftedOrphanHosts, []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"example.com"}`), 0644)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Errorf("expected 0 deleted orphan ext files (conservative retention), got %d", deleted)
	}
	if skipped < 9 {
		t.Errorf("expected at least 9 skipped orphan ext files, got %d", skipped)
	}

	// All orphan .ext files must be preserved!
	for _, p := range []string{forged1, forged2, forged3, forged4, forged5, forged6, orphanUserPath, handcraftedOrphanGBF, handcraftedOrphanHosts} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG: orphan .ext %s was deleted: %v", p, err)
		}
	}
}

// 12. 真实历史缓存（无 .ext 但有根标记与合法魔数）仍可正常清理，未发生回归。
func TestClearAll_LegacyCacheNormalCleaning(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	validJS := []byte("console.log('legacy js');")

	// 1. Versioned legacy asset: assets/12345/img/sp/valid_legacy.png
	file1 := filepath.Join(tmpDir, "assets", "12345", "img", "sp", "valid_legacy.png")
	_ = os.MkdirAll(filepath.Dir(file1), 0755)
	_ = os.WriteFile(file1, validPng, 0644)

	// 2. Direct category legacy asset: assets/img/sp/valid_legacy2.png
	file2 := filepath.Join(tmpDir, "assets", "img", "sp", "valid_legacy2.png")
	_ = os.MkdirAll(filepath.Dir(file2), 0755)
	_ = os.WriteFile(file2, validPng, 0644)

	// 3. Versioned js legacy asset: assets/12345/js/bundle.js
	file3 := filepath.Join(tmpDir, "assets", "12345", "js", "bundle.js")
	_ = os.MkdirAll(filepath.Dir(file3), 0755)
	_ = os.WriteFile(file3, validJS, 0644)

	// 4. Root fallback legacy asset: sound/voice/sample.mp3
	file4 := filepath.Join(tmpDir, "sound", "voice", "sample.mp3")
	_ = os.MkdirAll(filepath.Dir(file4), 0755)
	_ = os.WriteFile(file4, []byte("ID3dummy_audio_bytes"), 0644)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 4 {
		t.Errorf("expected 4 deleted legacy files, got %d", deleted)
	}
	if skipped != 0 {
		t.Errorf("expected 0 skipped files, got %d", skipped)
	}

	for _, p := range []string{file1, file2, file3, file4} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legacy file %s to be deleted, got: %v", p, err)
		}
	}

	// Root marker must remain intact
	if !HasRootMarker(tmpDir) {
		t.Errorf("expected root marker to remain intact")
	}
}

// 13. 根标记反向测试：非普通文件（如目录、符号链接）或内容不匹配的 .gbf_cache 拒绝授权 Tier 2 清理。
func TestClearAll_RootMarkerValidationRejectsInvalid(t *testing.T) {
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Case A: 标记内容不正确 -> 拒绝授权 Tier 2
	t.Run("wrong marker content", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		markerPath := filepath.Join(tmpDir, RootMarkerFileName)
		_ = os.WriteFile(markerPath, []byte("Wrong root marker content\n"), 0644)

		legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
		_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
		_ = os.WriteFile(legacyFile, validPng, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files with invalid marker content, got %d", deleted)
		}
		if skipped != 1 {
			t.Errorf("expected 1 skipped legacy file, got %d", skipped)
		}
		if _, err := os.Stat(legacyFile); err != nil {
			t.Errorf("expected legacy file to be preserved when marker content is invalid: %v", err)
		}
	})

	// Case B: 标记为空文件 (0 字节) -> 拒绝授权 Tier 2
	t.Run("empty marker file", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		markerPath := filepath.Join(tmpDir, RootMarkerFileName)
		_ = os.WriteFile(markerPath, []byte{}, 0644)

		legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
		_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
		_ = os.WriteFile(legacyFile, validPng, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files with 0-byte marker file, got %d", deleted)
		}
		if skipped != 1 {
			t.Errorf("expected 1 skipped legacy file, got %d", skipped)
		}
		if _, err := os.Stat(legacyFile); err != nil {
			t.Errorf("expected legacy file to be preserved when marker file is 0 bytes: %v", err)
		}
	})

	// Case C: 标记为目录 -> 拒绝授权 Tier 2
	t.Run("directory as marker", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		markerPath := filepath.Join(tmpDir, RootMarkerFileName)
		_ = os.Remove(markerPath)
		_ = os.Mkdir(markerPath, 0755)

		legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
		_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
		_ = os.WriteFile(legacyFile, validPng, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when marker is a directory, got %d", deleted)
		}
		if skipped != 1 {
			t.Errorf("expected 1 skipped legacy file, got %d", skipped)
		}
		if _, err := os.Stat(legacyFile); err != nil {
			t.Errorf("expected legacy file to be preserved when marker is a directory: %v", err)
		}
	})

	// Case D: 标记为符号链接 -> 拒绝授权 Tier 2
	t.Run("symlink as marker", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		markerPath := filepath.Join(tmpDir, RootMarkerFileName)
		_ = os.Remove(markerPath)

		extDir := t.TempDir()
		extTarget := filepath.Join(extDir, "valid_marker_target.txt")
		_ = os.WriteFile(extTarget, []byte(RootMarkerExpectedContent), 0644)
		if err := os.Symlink(extTarget, markerPath); err != nil {
			t.Skipf("skipping symlink test due to OS permission: %v", err)
		}

		legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
		_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
		_ = os.WriteFile(legacyFile, validPng, 0644)

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when marker is a symlink, got %d", deleted)
		}
		if skipped != 1 {
			t.Errorf("expected 1 skipped legacy file, got %d", skipped)
		}
		if _, err := os.Stat(legacyFile); err != nil {
			t.Errorf("expected legacy file to be preserved when marker is a symlink: %v", err)
		}
	})
}

// 14. 合法类目内部的用户文件反向测试：
// 覆盖 assets/img/personal_data/photo.png、assets/12345/img/personal_data/photo.png、
// img/personal_data/photo.png、hosts/example.com/assets/12345/img/personal_data/photo.png 等场景。
// 即使文件具有合法 PNG 魔数并存在有效的 .gbf_cache 根标记，亦必须安全保留且不被删除；
// 对应的用户目录（personal_data）亦不得被误回收。
// 同时验证真实合法缓存路径下的文件仍能正常清理，未发生漏清回归。
func TestClearAll_PreservesUserFilesUnderCategoryPaths(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// 1. User files that must be preserved (non-cache top-level paths or non-asset extensions)
	userFiles := []string{
		filepath.Join(tmpDir, "personal_data", "photo.png"),
		filepath.Join(tmpDir, "user_workspace", "photo.png"),
		filepath.Join(tmpDir, "my_backup", "photo.png"),
		filepath.Join(tmpDir, "hosts", "gbf", "photo.png"),
		filepath.Join(tmpDir, "assets", "sound", "document.docx"),
		filepath.Join(tmpDir, "assets", "img", "sp", "notes.txt"),
	}

	for _, p := range userFiles {
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if strings.HasSuffix(p, ".docx") || strings.HasSuffix(p, ".txt") {
			_ = os.WriteFile(p, []byte("user non-asset text content"), 0644)
		} else {
			_ = os.WriteFile(p, validPng, 0644)
		}
	}

	// 2. Real cache files that should be deleted
	legitCacheFiles := []string{
		filepath.Join(tmpDir, "assets", "12345", "img", "sp", "btn.png"),
		filepath.Join(tmpDir, "assets", "img", "sp", "quest.png"),
		filepath.Join(tmpDir, "sound", "voice", "attack.mp3"),
	}
	for _, p := range legitCacheFiles {
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if strings.HasSuffix(p, ".mp3") {
			_ = os.WriteFile(p, []byte("ID3valid_audio_magic_bytes"), 0644)
		} else {
			_ = os.WriteFile(p, validPng, 0644)
		}
	}

	deleted, _, skipped := mgr.ClearAll()

	if deleted != len(legitCacheFiles) {
		t.Errorf("expected %d deleted legit cache files, got deleted=%d", len(legitCacheFiles), deleted)
	}
	for _, p := range legitCacheFiles {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legit cache file %s to be deleted, got err: %v", p, err)
		}
	}

	if skipped < len(userFiles) {
		t.Errorf("expected at least %d skipped user files, got skipped=%d", len(userFiles), skipped)
	}
	for _, p := range userFiles {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG DETECTED: user file %s was deleted by ClearAll: %v", p, err)
		}
	}

	userDirs := []string{
		filepath.Join(tmpDir, "personal_data"),
		filepath.Join(tmpDir, "user_workspace"),
		filepath.Join(tmpDir, "my_backup"),
	}
	for _, d := range userDirs {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("BUG DETECTED: user directory %s was deleted by ClearAll: %v", d, err)
		}
	}
}

func TestClearAll_PreservesUserFilesUnderDeepCategoryPaths(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("failed to ensure root marker: %v", err)
	}

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	validMp3 := []byte("ID3\x04\x00\x00\x00\x00\x00#TIT2\x00\x00\x00\x05\x00\x00\x03test\x00")
	validJs := []byte(`(function(){ console.log("custom backup script"); })();`)
	validCss := []byte(`body { margin: 0; padding: 0; background-color: #ffffff; }`)

	// 1. User files that must be preserved (non-cache top-level paths or non-asset extensions)
	type userFileEntry struct {
		rel  string
		data []byte
	}
	userFiles := []userFileEntry{
		{"personal_data/quest/photo.png", validPng},
		{"user_workspace/private/photo.png", validPng},
		{"my_backup/sound/audio.mp3", validMp3},
		{"user_scripts/custom.js", validJs},
		{"my_styles/theme.css", validCss},
		{"2026/photos/event.png", validPng},
		{"assets/document.docx", []byte("docx content")},
		{"assets/12345/archive.zip", []byte("zip content")},
	}
	for _, uf := range userFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(uf.rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", uf.rel, err)
		}
		if err := os.WriteFile(p, uf.data, 0644); err != nil {
			t.Fatalf("failed to write user file %s: %v", uf.rel, err)
		}
	}

	// 2. Real cache files that should be deleted
	type legitEntry struct {
		rel  string
		data []byte
	}
	legitCacheFiles := []legitEntry{
		{"assets/12345/img/sp/quest/scene/character/body/3040001000.png", validPng},
		{"assets/img/sp/event/common/parts/btn_ok.png", validPng},
		{"assets/12345/sound/voice/3040001_v.mp3", validMp3},
		{"assets/12345/js/model/manifest/npc_3040001000.js", validJs},
		{"assets/css/common/index.css", validCss},
		{"assets/img/sp/event/raid01/top.png", validPng},
		{"assets/img/sp/event/teamraid075/top.png", validPng},
		{"assets/img/sp/event/ev01/banner.png", validPng},
	}
	for _, lf := range legitCacheFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(lf.rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", lf.rel, err)
		}
		if err := os.WriteFile(p, lf.data, 0644); err != nil {
			t.Fatalf("failed to write legit cache file %s: %v", lf.rel, err)
		}
	}

	deleted, _, skipped := mgr.ClearAll()

	if deleted != len(legitCacheFiles) {
		t.Errorf("expected %d deleted legit cache files, got deleted=%d", len(legitCacheFiles), deleted)
	}
	for _, lf := range legitCacheFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(lf.rel))
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legit cache file %s to be deleted, got err: %v", lf.rel, err)
		}
	}

	if skipped < len(userFiles) {
		t.Errorf("expected at least %d skipped user files, got skipped=%d", len(userFiles), skipped)
	}
	for _, uf := range userFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(uf.rel))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG DETECTED: user file %s was deleted by ClearAll: %v", uf.rel, err)
		}
	}

	userDirs := []string{
		filepath.Join(tmpDir, "personal_data"),
		filepath.Join(tmpDir, "user_workspace"),
		filepath.Join(tmpDir, "my_backup"),
		filepath.Join(tmpDir, "user_scripts"),
		filepath.Join(tmpDir, "my_styles"),
		filepath.Join(tmpDir, "2026"),
	}
	for _, d := range userDirs {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("BUG DETECTED: user directory %s was deleted by ClearAll: %v", d, err)
		}
	}
}

func TestClearAll_VersionFallbackSafety(t *testing.T) {
	tmpDir := t.TempDir()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	// 1. 合法 Tier-1 fallback 文件及匹配 sidecar (300/js/app.js)
	legitFallback := filepath.Join(tmpDir, "300", "js", "app.js")
	if err := os.MkdirAll(filepath.Dir(legitFallback), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	appCode := []byte("console.log('legit_fallback');")
	if err := os.WriteFile(legitFallback, appCode, 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	fiLegit, err := os.Stat(legitFallback)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if err := writeDiskExtFile(legitFallback, diskMeta{
		ContentType: "application/javascript",
		Namespace:   "gbf",
		Version:     1,
		Verified:    true,
		Size:        int64(len(appCode)),
		MTimeNano:   fiLegit.ModTime().UnixNano(),
	}); err != nil {
		t.Fatalf("write ext failed: %v", err)
	}

	// 2. 孤立 .ext 保留 (300/js/orphan.js.ext - 数据文件不存在)
	orphanExt := filepath.Join(tmpDir, "300", "js", "orphan.js.ext")
	if err := writeDiskExtFile(strings.TrimSuffix(orphanExt, ".ext"), diskMeta{
		ContentType: "application/javascript",
		Namespace:   "gbf",
		Version:     1,
		Verified:    true,
		Size:        123,
		MTimeNano:   time.Now().UnixNano(),
	}); err != nil {
		t.Fatalf("write orphan ext failed: %v", err)
	}

	// 3. 不匹配 sidecar 保留
	// 3a. 版本回退路径 (300/img/sp/quest/scene/mismatch_ns.png) 配合不匹配命名空间的 .ext -> Tier-1 拒绝，必须保留
	pngMagic := []byte("\x89PNG\r\n\x1a\nmismatch_ns")
	mismatchNSFile := filepath.Join(tmpDir, "300", "img", "sp", "quest", "scene", "mismatch_ns.png")
	if err := os.MkdirAll(filepath.Dir(mismatchNSFile), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(mismatchNSFile, pngMagic, 0644); err != nil {
		t.Fatalf("write mismatch_ns file failed: %v", err)
	}
	fiMismatch, err := os.Stat(mismatchNSFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if err := writeDiskExtFile(mismatchNSFile, diskMeta{
		ContentType: "image/png",
		Namespace:   "external.host.com",
		Version:     1,
		Verified:    true,
		Size:        int64(len(pngMagic)),
		MTimeNano:   fiMismatch.ModTime().UnixNano(),
	}); err != nil {
		t.Fatalf("write mismatch ext failed: %v", err)
	}

	// 3b. 版本回退路径 (300/js/mismatch_size.js) 配合 size 不匹配的 .ext -> Tier-1 拒绝，数据文件与 sidecar 均必须保留
	mismatchSizeFile := filepath.Join(tmpDir, "300", "js", "mismatch_size.js")
	if err := os.WriteFile(mismatchSizeFile, appCode, 0644); err != nil {
		t.Fatalf("write mismatch_size file failed: %v", err)
	}
	fiMismatchSize, err := os.Stat(mismatchSizeFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if err := writeDiskExtFile(mismatchSizeFile, diskMeta{
		ContentType: "application/javascript",
		Namespace:   "gbf",
		Version:     1,
		Verified:    true,
		Size:        int64(len(appCode)) + 999,
		MTimeNano:   fiMismatchSize.ModTime().UnixNano(),
	}); err != nil {
		t.Fatalf("write mismatch_size ext failed: %v", err)
	}

	// 4. 未确认数字目录用户文件保留
	// 4a. 2026/photos/family.png (数字版本目录 + 未知用户子目录 photos，无有效缓存归属)
	userPhoto := filepath.Join(tmpDir, "2026", "photos", "family.png")
	if err := os.MkdirAll(filepath.Dir(userPhoto), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(userPhoto, pngMagic, 0644); err != nil {
		t.Fatalf("write user photo failed: %v", err)
	}

	// 4b. 300/personal/notes.txt (合法数字目录 + 未知分类 personal)
	userNotes := filepath.Join(tmpDir, "300", "personal", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(userNotes), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(userNotes, []byte("private notes"), 0644); err != nil {
		t.Fatalf("write user notes failed: %v", err)
	}

	// 4c. 300/js/no_ext.js (版本回退路径但无 .ext 元数据，Tier-2 严禁放行)
	noExtFile := filepath.Join(tmpDir, "300", "js", "no_ext.js")
	if err := os.WriteFile(noExtFile, appCode, 0644); err != nil {
		t.Fatalf("write no_ext file failed: %v", err)
	}

	// 执行 ClearAll
	deleted, freed, skipped := mgr.ClearAll()
	_ = freed

	// 断言 1: 合法 Tier-1 fallback 文件及匹配 sidecar 必须被清理 (app.js + app.js.ext = 2 个文件)
	if _, err := os.Stat(legitFallback); !os.IsNotExist(err) {
		t.Fatalf("expected legit Tier-1 fallback %s to be deleted, got err: %v", legitFallback, err)
	}
	if _, err := os.Stat(legitFallback + ".ext"); !os.IsNotExist(err) {
		t.Fatalf("expected legit Tier-1 fallback sidecar %s to be deleted, got err: %v", legitFallback+".ext", err)
	}
	if deleted < 2 {
		t.Fatalf("expected at least 2 deleted files (asset + paired ext), got deleted=%d", deleted)
	}

	// 断言 2: 孤立 .ext 必须完整保留
	if _, err := os.Stat(orphanExt); err != nil {
		t.Fatalf("expected orphan .ext %s to be preserved, got err: %v", orphanExt, err)
	}

	// 断言 3: 不匹配 sidecar 必须完整保留
	if _, err := os.Stat(mismatchNSFile); err != nil {
		t.Fatalf("expected mismatch NS data file %s to be preserved, got err: %v", mismatchNSFile, err)
	}
	if _, err := os.Stat(mismatchNSFile + ".ext"); err != nil {
		t.Fatalf("expected mismatch NS sidecar %s to be preserved, got err: %v", mismatchNSFile+".ext", err)
	}
	if _, err := os.Stat(mismatchSizeFile); !os.IsNotExist(err) {
		t.Fatalf("expected mismatch size data file %s to be deleted under Tier-1: %v", mismatchSizeFile, err)
	}
	if _, err := os.Stat(mismatchSizeFile + ".ext"); !os.IsNotExist(err) {
		t.Fatalf("expected mismatch size sidecar %s to be deleted under Tier-1: %v", mismatchSizeFile+".ext", err)
	}

	// 断言 4: 未确认数字目录用户文件必须完整保留
	for _, keep := range []string{userPhoto, userNotes, noExtFile} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected unconfirmed user file %s in numeric directory to be preserved, got err: %v", keep, err)
		}
	}

	if skipped == 0 {
		t.Fatalf("expected skipped > 0 for unconfirmed files and orphan/mismatched sidecars, got skipped=%d", skipped)
	}
}

// TestClearAll_NamespaceSafetyMatrix 全量验证 ClearAll 对 8 种 Namespace 场景的删除/保留行为：
// 1. 普通 GBF 路径 + 有效 GBF Namespace -> 物理文件与 sidecar 均删除
// 2. 普通 GBF 路径 + 空 Namespace -> 物理文件与 sidecar 均删除 (兼容旧版历史元数据)
// 3. 版本回退路径 + 有效 GBF Namespace -> 物理文件与 sidecar 均删除
// 4. 版本回退路径 + 空 Namespace -> 物理文件与 sidecar 均保留
// 5. hosts 路径 + GBF Namespace -> 物理文件与 sidecar 均删除 (Form A 入口)
// 6. hosts 路径 + hostname Namespace -> 物理文件与 sidecar 均删除 (Form B 入口)
// 7. hosts 路径 + 空 Namespace -> 物理文件与 sidecar 均保留
// 8. hosts 路径 + 不匹配的 hostname Namespace -> 物理文件与 sidecar 均保留
func TestClearAll_NamespaceSafetyMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	payload := []byte("console.log('clear_all_ns_matrix');")

	type testCase struct {
		name        string
		relPath     string
		metaNS      string
		shouldClean bool
	}

	cases := []testCase{
		{"1_CanonicalGBF_ValidNS", "assets/300/js/canonical_valid.js", "gbf", true},
		{"2_CanonicalGBF_EmptyNS", "assets/300/js/canonical_empty.js", "", true},
		{"3_Fallback_ValidNS", "300/js/fallback_valid.js", "gbf", true},
		{"4_Fallback_EmptyNS", "300/js/fallback_empty.js", "", false},
		{"5_Hosts_GBF_NS", "hosts/sub.example.com/assets/js/host_gbf.js", "gbf", true},
		{"6_Hosts_Hostname_NS", "hosts/sub.example.com/assets/js/host_direct.js", "sub.example.com", true},
		{"7_Hosts_EmptyNS", "hosts/sub.example.com/assets/js/host_empty.js", "", false},
		{"8_Hosts_MismatchNS", "hosts/sub.example.com/assets/js/host_mismatch.js", "other.example.com", false},
	}

	for _, tc := range cases {
		fullPath := filepath.Join(tmpDir, filepath.FromSlash(tc.relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("mkdir %s failed: %v", tc.relPath, err)
		}
		if err := os.WriteFile(fullPath, payload, 0644); err != nil {
			t.Fatalf("write %s failed: %v", tc.relPath, err)
		}
		fi, err := os.Stat(fullPath)
		if err != nil {
			t.Fatalf("stat %s failed: %v", tc.relPath, err)
		}
		if err := writeDiskExtFile(fullPath, diskMeta{
			ContentType: "application/javascript",
			Version:     1,
			Verified:    true,
			Size:        fi.Size(),
			MTimeNano:   fi.ModTime().UnixNano(),
			Namespace:   tc.metaNS,
		}); err != nil {
			t.Fatalf("write ext for %s failed: %v", tc.relPath, err)
		}
	}

	deleted, _, skipped := mgr.ClearAll()

	// 5 个合法文件及各自配对 .ext 被清理 -> 5 * 2 = 10 文件被删除
	if deleted != 10 {
		t.Fatalf("expected exactly 10 deleted files (5 pairs), got deleted=%d", deleted)
	}
	// 3 个应保留文件及其 .ext 未被删除
	if skipped == 0 {
		t.Fatalf("expected skipped > 0 for rejected/preserved files, got skipped=%d", skipped)
	}

	for _, tc := range cases {
		fullPath := filepath.Join(tmpDir, filepath.FromSlash(tc.relPath))
		extPath := fullPath + ".ext"
		if tc.shouldClean {
			if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
				t.Errorf("[%s] expected data file %s to be deleted, got err: %v", tc.name, fullPath, err)
			}
			if _, err := os.Stat(extPath); !os.IsNotExist(err) {
				t.Errorf("[%s] expected sidecar %s to be deleted, got err: %v", tc.name, extPath, err)
			}
		} else {
			if _, err := os.Stat(fullPath); err != nil {
				t.Errorf("[%s] expected data file %s to be PRESERVED, got err: %v", tc.name, fullPath, err)
			}
			if _, err := os.Stat(extPath); err != nil {
				t.Errorf("[%s] expected sidecar %s to be PRESERVED, got err: %v", tc.name, extPath, err)
			}
		}
	}
}

// End-to-end test: pure ACGPower directory (no .ext) -> confirm & write marker -> ClearAll cleans up
func TestClearAll_PureACGPowerDirectoryE2E(t *testing.T) {
	tmpDir := t.TempDir()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	acgFiles := []string{
		"assets/12345/img/sp/quest.png",
		"assets/img/sp/btn.png",
		"assets/common/img.png",
	}

	for _, rel := range acgFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(full, validPng, 0644); err != nil {
			t.Fatalf("write acg file failed: %v", err)
		}
	}

	// 1. Initialise Manager on existing non-empty directory (does NOT auto-write marker)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	if mgr.HasRootMarker() {
		t.Fatalf("expected HasRootMarker to be false before confirmation on existing non-empty directory")
	}

	// 2. Before marker confirmation, ClearAll must NOT delete legacy ACGPower files without .ext (Tier-2 requires marker)
	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Fatalf("expected 0 deleted files before confirmation, got %d", deleted)
	}
	if skipped < len(acgFiles) {
		t.Fatalf("expected at least %d skipped files, got %d", len(acgFiles), skipped)
	}
	for _, rel := range acgFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("expected file %s to be preserved before confirmation: %v", rel, err)
		}
	}

	// 3. Check allFilesConfirmed assuming marker exists (breaks circular dependency)
	allConfirmed, err := AreAllFilesConfirmed(tmpDir)
	if err != nil {
		t.Fatalf("AreAllFilesConfirmed failed: %v", err)
	}
	if !allConfirmed {
		t.Fatalf("expected allFilesConfirmed to be true for pure ACGPower assets under assumed marker")
	}

	// 4. Explicitly confirm and write marker
	if err := mgr.EnsureRootMarker(); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	if !mgr.HasRootMarker() {
		t.Fatalf("expected HasRootMarker to be true after EnsureRootMarker")
	}

	// 5. After marker, ClearAll identifies pure ACGPower files under Tier-2 and deletes them
	deletedAfter, _, _ := mgr.ClearAll()
	if deletedAfter != len(acgFiles) {
		t.Fatalf("expected %d deleted files after confirmation, got %d", len(acgFiles), deletedAfter)
	}
	for _, rel := range acgFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Fatalf("expected file %s to be deleted after confirmation", rel)
		}
	}

	// Root marker must remain intact after ClearAll
	if !mgr.HasRootMarker() {
		t.Fatalf("expected root marker to remain intact after ClearAll")
	}
}

// Quarantine files strict matching, deletion during ClearAll, and preserving user x.quarantine.docx
func TestClearAll_QuarantineFilesStrictMatchingAndUserPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// 1. Legitimate quarantine files to be removed by ClearAll
	quarData := filepath.Join(tmpDir, "assets", "12345", "js", "tampered.js.quarantine.1730000000")
	quarExt := filepath.Join(tmpDir, "assets", "12345", "js", "tampered.js.ext.quarantine.1730000000")
	quarPng := filepath.Join(tmpDir, "assets", "img", "sp", "bad.png.quarantine.1730000001")
	_ = os.MkdirAll(filepath.Dir(quarData), 0755)
	_ = os.MkdirAll(filepath.Dir(quarPng), 0755)
	_ = os.WriteFile(quarData, []byte("tampered content"), 0644)
	_ = os.WriteFile(quarExt, []byte(`{"v":1}`), 0644)
	_ = os.WriteFile(quarPng, validPng, 0644)

	// 2. User files that contain quarantine in name but are not valid quarantine files
	userDoc := filepath.Join(tmpDir, "x.quarantine.docx")
	userFolderDoc := filepath.Join(tmpDir, "personal_data", "notes.quarantine.123")
	userAssetDoc := filepath.Join(tmpDir, "assets", "12345", "user.docx.quarantine.123")
	_ = os.MkdirAll(filepath.Dir(userFolderDoc), 0755)
	_ = os.WriteFile(userDoc, []byte("user docx content"), 0644)
	_ = os.WriteFile(userFolderDoc, []byte("user notes content"), 0644)
	_ = os.WriteFile(userAssetDoc, []byte("user docx in assets"), 0644)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 3 {
		t.Fatalf("expected exactly 3 deleted quarantine files, got %d", deleted)
	}
	if skipped < 3 {
		t.Fatalf("expected at least 3 skipped user files, got %d", skipped)
	}

	// Verify quarantine files are deleted
	for _, p := range []string{quarData, quarExt, quarPng} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected quarantine file %s to be deleted", p)
		}
	}

	// Verify user files are 100% preserved
	for _, p := range []string{userDoc, userFolderDoc, userAssetDoc} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG: user quarantine-like file %s was deleted: %v", p, err)
		}
	}
}

// Assert non-syntax paths (e.g. assets/icon.png) with valid .ext are deleted by ClearAll
func TestClearAll_NonSyntaxPathWithValidExtDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Paths that were outside deep directory grammar but have valid top-level shape and .ext
	nonSyntaxFiles := []string{
		"assets/icon.png",
		"assets/common/img.png",
		"assets/random_module/asset.png",
	}

	for _, rel := range nonSyntaxFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(full, validPng, 0644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		ext := full + ".ext"
		if err := os.WriteFile(ext, []byte(`{"v":1,"ct":"image/png","ns":"gbf"}`), 0644); err != nil {
			t.Fatalf("write ext failed: %v", err)
		}
	}

	deleted, _, skipped := mgr.ClearAll()
	// Each file + paired .ext -> 3 * 2 = 6 files deleted
	if deleted != len(nonSyntaxFiles)*2 {
		t.Fatalf("expected %d deleted files for non-syntax paths with valid ext, got %d (skipped=%d)", len(nonSyntaxFiles)*2, deleted, skipped)
	}

	for _, rel := range nonSyntaxFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Errorf("expected %s to be deleted by ClearAll", rel)
		}
		if _, err := os.Stat(full + ".ext"); !os.IsNotExist(err) {
			t.Errorf("expected %s.ext to be deleted by ClearAll", rel)
		}
	}
}
