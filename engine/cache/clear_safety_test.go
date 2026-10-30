package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	testBeforeClearAllRemoveHook = func(relPath string) {
		if strings.HasSuffix(filepath.ToSlash(relPath), "replace_me.png") {
			_ = os.Remove(targetFullPath)
			_ = os.WriteFile(targetFullPath, []byte("attacker replaced content"), 0644)
		}
	}
	defer func() {
		testBeforeClearAllRemoveHook = nil
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

// 9. 用户在 assets/personal_data/ 等非缓存路径下创建的文件（即使配有合法 .ext），执行 ClearAll 后必须保留。
func TestClearAll_PreservesUserFileUnderAssetsPersonalData(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// 1. User photo in assets/personal_data/ without .ext (valid PNG bytes and extension)
	userPhotoDir := filepath.Join(tmpDir, "assets", "personal_data")
	if err := os.MkdirAll(userPhotoDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	userPhoto := filepath.Join(userPhotoDir, "photo.png")
	if err := os.WriteFile(userPhoto, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 2. User photo in assets/personal_data/ WITH a valid paired .ext JSON metadata
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

	// 3. User photo in assets/12345/personal_data/
	userPhotoVerDir := filepath.Join(tmpDir, "assets", "12345", "personal_data")
	if err := os.MkdirAll(userPhotoVerDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	userPhotoVer := filepath.Join(userPhotoVerDir, "photo.png")
	if err := os.WriteFile(userPhotoVer, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 4. User file in assets/12345/btn.png (inside version folder without category)
	userBareVerFile := filepath.Join(tmpDir, "assets", "12345", "btn.png")
	if err := os.WriteFile(userBareVerFile, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 5. User file in 12345/img/sp/test.png (bare version folder at cache root)
	userBareRootDir := filepath.Join(tmpDir, "12345", "img", "sp")
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

	// assets/personal_data/photo.png and photo2.png must remain intact
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

	// assets/12345/personal_data/photo.png must remain intact
	if _, err := os.Stat(userPhotoVer); err != nil {
		t.Errorf("BUG: user versioned photo %s was deleted by ClearAll: %v", userPhotoVer, err)
	}
	if fi, err := os.Stat(userPhotoVerDir); err != nil || !fi.IsDir() {
		t.Errorf("BUG: user directory %s was pruned: %v", userPhotoVerDir, err)
	}

	// assets/12345/btn.png must remain intact
	if _, err := os.Stat(userBareVerFile); err != nil {
		t.Errorf("BUG: user bare version file %s was deleted: %v", userBareVerFile, err)
	}

	// 12345/img/sp/test.png must remain intact
	if _, err := os.Stat(userBareRootFile); err != nil {
		t.Errorf("BUG: user bare root file %s was deleted: %v", userBareRootFile, err)
	}
}

// 10. 伴生 .ext 元数据存在但 size、mtimeNano、版本、命名空间错误或内容损坏时，文件必须保留，绝对不能降级落入 Tier 2 删除。
func TestClearAll_PreservesFileWithMismatchedExtMetadata(t *testing.T) {
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Case A: Size mismatch
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

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when size mismatches, got %d", deleted)
		}
		if skipped < 1 {
			t.Errorf("expected at least 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(pngPath); err != nil {
			t.Errorf("BUG: file %s with mismatched .ext size was deleted: %v", pngPath, err)
		}
		if _, err := os.Stat(extPath); err != nil {
			t.Errorf("BUG: .ext %s was deleted: %v", extPath, err)
		}
	})

	// Case B: MTimeNano mismatch
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

		deleted, _, skipped := mgr.ClearAll()
		if deleted != 0 {
			t.Errorf("expected 0 deleted files when mtime mismatches, got %d", deleted)
		}
		if skipped < 1 {
			t.Errorf("expected at least 1 skipped file, got %d", skipped)
		}
		if _, err := os.Stat(pngPath); err != nil {
			t.Errorf("BUG: file %s with mismatched .ext mtime was deleted: %v", pngPath, err)
		}
		if _, err := os.Stat(extPath); err != nil {
			t.Errorf("BUG: .ext %s was deleted: %v", extPath, err)
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
	userDir := filepath.Join(tmpDir, "assets", "personal_data")
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

		extDir := t.TempDir()
		extTarget := filepath.Join(extDir, "valid_marker_target.txt")
		_ = os.WriteFile(extTarget, []byte(RootMarkerExpectedContent), 0644)
		markerPath := filepath.Join(tmpDir, RootMarkerFileName)
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

	// 1. 用户文件场景（必须保留）
	userFiles := []string{
		filepath.Join(tmpDir, "assets", "img", "personal_data", "photo.png"),
		filepath.Join(tmpDir, "assets", "12345", "img", "personal_data", "photo.png"),
		filepath.Join(tmpDir, "img", "personal_data", "photo.png"),
		filepath.Join(tmpDir, "hosts", "example.com", "assets", "12345", "img", "personal_data", "photo.png"),
		filepath.Join(tmpDir, "assets", "sound", "personal_data", "audio.mp3"),
		filepath.Join(tmpDir, "assets", "img", "sp", "personal_data", "photo.png"),
	}

	for _, p := range userFiles {
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		if strings.HasSuffix(p, ".mp3") {
			_ = os.WriteFile(p, []byte("ID3valid_audio_magic_bytes"), 0644)
		} else {
			_ = os.WriteFile(p, validPng, 0644)
		}
	}

	// 2. 真实合法缓存素材（必须能被正常清理）
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

	// 验证合法缓存全部被清理
	if deleted != len(legitCacheFiles) {
		t.Errorf("expected %d deleted legit cache files, got deleted=%d", len(legitCacheFiles), deleted)
	}
	for _, p := range legitCacheFiles {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legit cache file %s to be deleted, got err: %v", p, err)
		}
	}

	// 验证所有用户文件全部安全保留
	if skipped < len(userFiles) {
		t.Errorf("expected at least %d skipped user files, got skipped=%d", len(userFiles), skipped)
	}
	for _, p := range userFiles {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG DETECTED: user file %s was deleted by ClearAll: %v", p, err)
		}
	}

	// 验证 personal_data 用户目录未被误删除
	userDirs := []string{
		filepath.Join(tmpDir, "assets", "img", "personal_data"),
		filepath.Join(tmpDir, "assets", "12345", "img", "personal_data"),
		filepath.Join(tmpDir, "img", "personal_data"),
		filepath.Join(tmpDir, "hosts", "example.com", "assets", "12345", "img", "personal_data"),
		filepath.Join(tmpDir, "assets", "sound", "personal_data"),
		filepath.Join(tmpDir, "assets", "img", "sp", "personal_data"),
	}
	for _, d := range userDirs {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("BUG DETECTED: user directory %s was deleted by ClearAll: %v", d, err)
		}
	}
}
