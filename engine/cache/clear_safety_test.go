package cache

import (
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
