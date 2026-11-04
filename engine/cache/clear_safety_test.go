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
	for _, p := range []string{userDocPath, userNotesPath, nestedFilePath, userTmpDocPath, nestedTmpDocPath, userExtPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG DETECTED: user file %s was deleted by ClearAll: %v", p, err)
		}
	}

	// System junk files MUST NOT be deleted
	for _, p := range []string{dsStorePath, thumbsDbPath, desktopIniPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG DETECTED: system file %s was deleted by ClearAll: %v", p, err)
		}
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
			t.Errorf("expected legacy file to be preserved without root marker: %v", err)
		}
	})

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
			t.Errorf("expected legacy file to be deleted with root marker: %v", err)
		}
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

	deleted, freed, skipped := mgr.ClearAll()
	if deleted != 0 || freed != 0 || skipped != 0 {
		t.Errorf("expected ClearAll on user home to immediately return (0, 0, 0), got (%d, %d, %d)", deleted, freed, skipped)
	}
}

func TestClearAll_PreservesPreExistingEmptyUserDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", map[string]string{"content-type": "image/png"}, validPng) {
		t.Fatalf("failed to save cache asset")
	}

	emptyDirs := []string{
		filepath.Join(tmpDir, "personal_data"),
		filepath.Join(tmpDir, "user_workspace"),
		filepath.Join(tmpDir, "custom_project", "empty_sub"),
	}
	for _, d := range emptyDirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
	}

	deleted, _, _ := mgr.ClearAll()
	if deleted < 2 {
		t.Errorf("expected at least 2 deleted cache items, got %d", deleted)
	}

	for _, d := range emptyDirs {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
			t.Errorf("BUG: empty user directory %s was deleted: %v", d, err)
		}
	}

	prunedCacheDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	if _, err := os.Stat(prunedCacheDir); !os.IsNotExist(err) {
		t.Errorf("expected empty cache directory %s to be pruned", prunedCacheDir)
	}
}

func TestClearAll_PreservesUserTempFilePattern(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	userDir := filepath.Join(tmpDir, "personal_data")
	_ = os.MkdirAll(userDir, 0755)

	userFiles := []string{
		filepath.Join(userDir, "photo.png.tmp.123.456"),
		filepath.Join(userDir, "photo.png.tmp.123.456.789"),
	}
	for _, f := range userFiles {
		_ = os.WriteFile(f, []byte("temp data"), 0644)
	}

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	_ = mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", map[string]string{"content-type": "image/png"}, validPng)

	deleted, _, skipped := mgr.ClearAll()
	if deleted < 2 || skipped < len(userFiles) {
		t.Errorf("expected deleted>=2 and skipped>=%d, got deleted=%d, skipped=%d", len(userFiles), deleted, skipped)
	}

	for _, f := range userFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("BUG: user temp file %s was deleted: %v", f, err)
		}
	}
}

func TestClearAll_PreservesUserExtFile(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	userDir := filepath.Join(tmpDir, "personal_data")
	_ = os.MkdirAll(userDir, 0755)

	userPhoto := filepath.Join(userDir, "photo.png")
	_ = os.WriteFile(userPhoto, []byte("user photo contents"), 0644)

	validExtJSON := []byte(`{"v":1,"size":19,"mtime_nano":123456789,"ns":"gbf"}`)
	userPhotoExt := filepath.Join(userDir, "photo.png.ext")
	_ = os.WriteFile(userPhotoExt, validExtJSON, 0644)

	userWorkspaceDir := filepath.Join(tmpDir, "user_workspace")
	_ = os.MkdirAll(userWorkspaceDir, 0755)
	orphanUserExt := filepath.Join(userWorkspaceDir, "document.png.ext")
	_ = os.WriteFile(orphanUserExt, validExtJSON, 0644)

	_, _, skipped := mgr.ClearAll()
	if skipped < 3 {
		t.Errorf("expected at least 3 skipped user files, got skipped=%d", skipped)
	}

	for _, f := range []string{userPhoto, userPhotoExt, orphanUserExt} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("BUG: user file %s was deleted: %v", f, err)
		}
	}
}

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

	symlinkPng := filepath.Join(cacheSubDir, "symlink_asset.png")
	if err := os.Symlink(externalPng, symlinkPng); err != nil {
		t.Skipf("skipping symlink test due to OS permission: %v", err)
	}

	symlinkTmp := filepath.Join(cacheSubDir, "fake.png.tmp.1234.5678.1")
	_ = os.Symlink(externalPng, symlinkTmp)

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

	if content, err := os.ReadFile(externalPng); err != nil || len(content) != len(validPng) {
		t.Errorf("BUG: external file was deleted or corrupted by ClearAll: %v", err)
	}

	for _, s := range []string{symlinkPng, symlinkTmp, symlinkExt} {
		if _, err := os.Lstat(s); err != nil {
			t.Errorf("BUG: symlink %s was deleted by ClearAll: %v", s, err)
		}
	}
}

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

	mgr.SetBeforeClearAllRemoveHookForTest(func(relPath string) {
		if strings.HasSuffix(filepath.ToSlash(relPath), "replace_me.png") {
			_ = os.Remove(targetFullPath)
			_ = os.WriteFile(targetFullPath, []byte("attacker replaced content"), 0644)
		}
	})
	defer mgr.SetBeforeClearAllRemoveHookForTest(nil)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Errorf("expected 0 deleted files when object replaced, got %d", deleted)
	}
	if skipped < 1 {
		t.Errorf("expected at least 1 skipped file due to identity mismatch, got %d", skipped)
	}

	if content, err := os.ReadFile(targetFullPath); err != nil || string(content) != "attacker replaced content" {
		t.Errorf("expected replaced file to remain untouched, got err=%v, content=%q", err, string(content))
	}
}

func TestClearAll_PreservesUserInvalidExtAdjacentToLegacyAsset(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	_ = os.MkdirAll(subDir, 0755)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	pngPath := filepath.Join(subDir, "btn.png")
	_ = os.WriteFile(pngPath, validPng, 0644)

	userNotePath := filepath.Join(subDir, "btn.png.ext")
	userNoteContent := []byte("confidential user notes: not json metadata")
	_ = os.WriteFile(userNotePath, userNoteContent, 0644)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 || skipped < 2 {
		t.Errorf("expected 0 deleted files and skipped>=2, got deleted=%d, skipped=%d", deleted, skipped)
	}

	if _, err := os.Stat(pngPath); err != nil {
		t.Errorf("BUG: file %s was deleted despite invalid companion .ext: %v", pngPath, err)
	}
	if data, err := os.ReadFile(userNotePath); err != nil || string(data) != string(userNoteContent) {
		t.Errorf("BUG: user note %s was deleted or corrupted: %v", userNotePath, err)
	}
	if fi, err := os.Stat(subDir); err != nil || !fi.IsDir() {
		t.Errorf("BUG: directory %s containing user file was deleted: %v", subDir, err)
	}
}

func TestClearAll_CleanCacheNoFalseSkippedCount(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	_ = mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/clean.png", map[string]string{"content-type": "image/png"}, validPng)

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

func TestClearAll_PreservesUserFilesUnderCategoryPaths(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = EnsureRootMarker(tmpDir)

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	validMp3 := []byte("ID3\x04\x00\x00\x00\x00\x00#TIT2\x00\x00\x00\x05\x00\x00\x03test\x00")
	validJs := []byte(`(function(){ console.log("custom script"); })();`)
	validCss := []byte(`body { margin: 0; background-color: #ffffff; }`)

	// 1. User files that MUST be preserved across various directory shapes
	type userFileEntry struct {
		rel  string
		data []byte
	}
	userFiles := []userFileEntry{
		{"personal_data/quest/photo.png", validPng},
		{"personal_data/photo.png", validPng},
		{"user_workspace/private/photo.png", validPng},
		{"my_backup/sound/audio.mp3", validMp3},
		{"user_scripts/custom.js", validJs},
		{"my_styles/theme.css", validCss},
		{"2026/photos/event.png", validPng},
		{"assets/document.docx", []byte("docx content")},
		{"assets/12345/archive.zip", []byte("zip content")},
		{"assets/12345/document.docx", []byte("docx content 2")},
		{"12345/personal/test.png", validPng},
	}

	for _, uf := range userFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(uf.rel))
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		_ = os.WriteFile(p, uf.data, 0644)
	}

	// Also add user photo with paired .ext in non-cache path
	pairedUserPhoto := filepath.Join(tmpDir, "personal_data", "paired.png")
	_ = os.WriteFile(pairedUserPhoto, validPng, 0644)
	fi, _ := os.Stat(pairedUserPhoto)
	_ = os.WriteFile(pairedUserPhoto+".ext", []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano())), 0644)

	// 2. Real cache files that SHOULD be deleted
	legitCacheFiles := []struct {
		rel  string
		data []byte
	}{
		{"assets/12345/img/sp/btn.png", validPng},
		{"assets/img/sp/quest.png", validPng},
		{"sound/voice/attack.mp3", validMp3},
	}
	for _, cf := range legitCacheFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(cf.rel))
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		_ = os.WriteFile(p, cf.data, 0644)
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != len(legitCacheFiles) {
		t.Errorf("expected %d deleted legit cache files, got %d", len(legitCacheFiles), deleted)
	}

	for _, cf := range legitCacheFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(cf.rel))
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legit cache file %s to be deleted", p)
		}
	}

	if skipped < len(userFiles) {
		t.Errorf("expected at least %d skipped user files, got %d", len(userFiles), skipped)
	}

	for _, uf := range userFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(uf.rel))
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG DETECTED: user file %s was deleted: %v", uf.rel, err)
		}
	}

	if _, err := os.Stat(pairedUserPhoto); err != nil {
		t.Errorf("BUG DETECTED: paired user photo was deleted: %v", err)
	}
	if _, err := os.Stat(pairedUserPhoto + ".ext"); err != nil {
		t.Errorf("BUG DETECTED: paired user photo .ext was deleted: %v", err)
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
			t.Errorf("BUG DETECTED: user directory %s was pruned: %v", d, err)
		}
	}
}

func TestClearAll_PreservesFileWithMismatchedExtMetadata(t *testing.T) {
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	tests := []struct {
		name          string
		metaGen       func(fi os.FileInfo) []byte
		shouldDelete  bool
		expectDeleted int
	}{
		{
			name: "size mismatch (tier 1 still deletes)",
			metaGen: func(fi os.FileInfo) []byte {
				return []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size()+9999, fi.ModTime().UnixNano()))
			},
			shouldDelete:  true,
			expectDeleted: 2,
		},
		{
			name: "mtime mismatch (tier 1 still deletes)",
			metaGen: func(fi os.FileInfo) []byte {
				return []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano()-999999))
			},
			shouldDelete:  true,
			expectDeleted: 2,
		},
		{
			name: "corrupted non-json (must preserve)",
			metaGen: func(fi os.FileInfo) []byte {
				return []byte("broken non-json content")
			},
			shouldDelete:  false,
			expectDeleted: 0,
		},
		{
			name: "empty ext (must preserve)",
			metaGen: func(fi os.FileInfo) []byte {
				return []byte{}
			},
			shouldDelete:  false,
			expectDeleted: 0,
		},
		{
			name: "version mismatch (must preserve)",
			metaGen: func(fi os.FileInfo) []byte {
				return []byte(fmt.Sprintf(`{"v":2,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano()))
			},
			shouldDelete:  false,
			expectDeleted: 0,
		},
		{
			name: "namespace mismatch (must preserve)",
			metaGen: func(fi os.FileInfo) []byte {
				return []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"other_host"}`, fi.Size(), fi.ModTime().UnixNano()))
			},
			shouldDelete:  false,
			expectDeleted: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			mgr := NewManager(tmpDir, 64)
			defer mgr.Close()
			_ = EnsureRootMarker(tmpDir)

			subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
			_ = os.MkdirAll(subDir, 0755)
			pngPath := filepath.Join(subDir, "target.png")
			_ = os.WriteFile(pngPath, validPng, 0644)
			fi, _ := os.Stat(pngPath)

			extPath := pngPath + ".ext"
			_ = os.WriteFile(extPath, tc.metaGen(fi), 0644)

			deleted, _, skipped := mgr.ClearAll()
			if deleted != tc.expectDeleted {
				t.Fatalf("expected deleted=%d, got %d (skipped=%d)", tc.expectDeleted, deleted, skipped)
			}

			if tc.shouldDelete {
				if _, err := os.Stat(pngPath); !os.IsNotExist(err) {
					t.Errorf("expected asset %s to be deleted", pngPath)
				}
				if _, err := os.Stat(extPath); !os.IsNotExist(err) {
					t.Errorf("expected ext %s to be deleted", extPath)
				}
			} else {
				if _, err := os.Stat(pngPath); err != nil {
					t.Errorf("BUG: asset %s was deleted: %v", pngPath, err)
				}
				if _, err := os.Stat(extPath); err != nil {
					t.Errorf("BUG: ext %s was deleted: %v", extPath, err)
				}
			}
		})
	}
}

func TestClearAll_PreservesOrphanExtFilesEvenIfValidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()
	_ = EnsureRootMarker(tmpDir)

	subDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	_ = os.MkdirAll(subDir, 0755)
	userDir := filepath.Join(tmpDir, "personal_data")
	_ = os.MkdirAll(userDir, 0755)

	orphanFiles := []struct {
		path string
		data []byte
	}{
		{filepath.Join(subDir, "no_verified.png.ext"), []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","ns":"gbf"}`)},
		{filepath.Join(subDir, "no_mtime.png.ext"), []byte(`{"v":1,"size":100,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`)},
		{filepath.Join(subDir, "v2.png.ext"), []byte(`{"v":2,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`)},
		{filepath.Join(userDir, "orphan_photo.png.ext"), []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`)},
		{filepath.Join(subDir, "complete_orphan.png.ext"), []byte(`{"v":1,"size":100,"mtime":12345,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`)},
	}

	for _, of := range orphanFiles {
		_ = os.WriteFile(of.path, of.data, 0644)
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 || skipped < len(orphanFiles) {
		t.Fatalf("expected 0 deleted and skipped>=%d, got deleted=%d, skipped=%d", len(orphanFiles), deleted, skipped)
	}

	for _, of := range orphanFiles {
		if _, err := os.Stat(of.path); err != nil {
			t.Errorf("BUG: orphan ext file %s was deleted: %v", of.path, err)
		}
	}
}

func TestClearAll_LegacyCacheNormalCleaning(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()
	_ = EnsureRootMarker(tmpDir)

	files := []struct {
		rel  string
		data []byte
	}{
		{"assets/12345/img/sp/btn.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")},
		{"assets/js/bundle.js", []byte("function test(){ console.log(1); }")},
		{"assets/css/main.css", []byte(".main { color: red; }")},
		{"sound/voice/sample.mp3", []byte("ID3dummy_audio_bytes")},
	}

	for _, f := range files {
		full := filepath.Join(tmpDir, filepath.FromSlash(f.rel))
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, f.data, 0644)
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != len(files) || skipped != 0 {
		t.Fatalf("expected deleted=%d, skipped=0; got deleted=%d, skipped=%d", len(files), deleted, skipped)
	}

	for _, f := range files {
		full := filepath.Join(tmpDir, filepath.FromSlash(f.rel))
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Errorf("expected legacy file %s to be deleted", full)
		}
	}

	if !HasRootMarker(tmpDir) {
		t.Errorf("expected root marker to remain intact")
	}
}

func TestClearAll_RootMarkerValidationRejectsInvalid(t *testing.T) {
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	tests := []struct {
		name      string
		setupRoot func(t *testing.T, tmpDir string)
	}{
		{
			name: "wrong marker content",
			setupRoot: func(t *testing.T, tmpDir string) {
				_ = os.WriteFile(filepath.Join(tmpDir, RootMarkerFileName), []byte("Wrong content\n"), 0644)
			},
		},
		{
			name: "empty marker file",
			setupRoot: func(t *testing.T, tmpDir string) {
				_ = os.WriteFile(filepath.Join(tmpDir, RootMarkerFileName), []byte{}, 0644)
			},
		},
		{
			name: "directory as marker",
			setupRoot: func(t *testing.T, tmpDir string) {
				markerPath := filepath.Join(tmpDir, RootMarkerFileName)
				_ = os.Remove(markerPath)
				_ = os.Mkdir(markerPath, 0755)
			},
		},
		{
			name: "symlink as marker",
			setupRoot: func(t *testing.T, tmpDir string) {
				markerPath := filepath.Join(tmpDir, RootMarkerFileName)
				_ = os.Remove(markerPath)
				extTarget := filepath.Join(t.TempDir(), "target.txt")
				_ = os.WriteFile(extTarget, []byte(RootMarkerExpectedContent), 0644)
				if err := os.Symlink(extTarget, markerPath); err != nil {
					t.Skipf("skipping symlink: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			mgr := NewManager(tmpDir, 64)
			defer mgr.Close()

			tc.setupRoot(t, tmpDir)

			legacyFile := filepath.Join(tmpDir, "assets", "img", "sp", "legacy.png")
			_ = os.MkdirAll(filepath.Dir(legacyFile), 0755)
			_ = os.WriteFile(legacyFile, validPng, 0644)

			deleted, _, skipped := mgr.ClearAll()
			if deleted != 0 || skipped != 1 {
				t.Fatalf("expected deleted=0, skipped=1; got deleted=%d, skipped=%d", deleted, skipped)
			}
			if _, err := os.Stat(legacyFile); err != nil {
				t.Errorf("expected legacy file to be preserved: %v", err)
			}
		})
	}
}

func TestClearAll_VersionFallbackSafety(t *testing.T) {
	tmpDir := t.TempDir()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	appCode := []byte("console.log('legit_fallback');")
	pngMagic := []byte("\x89PNG\r\n\x1a\nmismatch_ns")

	// 1. Legit Tier-1 fallback with matching sidecar (300/js/app.js)
	legitFallback := filepath.Join(tmpDir, "300", "js", "app.js")
	_ = os.MkdirAll(filepath.Dir(legitFallback), 0755)
	_ = os.WriteFile(legitFallback, appCode, 0644)
	fiLegit, _ := os.Stat(legitFallback)
	_ = writeDiskExtFile(legitFallback, diskMeta{
		ContentType: "application/javascript",
		Namespace:   "gbf",
		Version:     1,
		Verified:    true,
		Size:        int64(len(appCode)),
		MTimeNano:   fiLegit.ModTime().UnixNano(),
	})

	// 2. Orphan .ext in fallback dir (300/js/orphan.js.ext)
	orphanExt := filepath.Join(tmpDir, "300", "js", "orphan.js.ext")
	_ = writeDiskExtFile(strings.TrimSuffix(orphanExt, ".ext"), diskMeta{
		ContentType: "application/javascript",
		Namespace:   "gbf",
		Version:     1,
		Verified:    true,
		Size:        123,
		MTimeNano:   time.Now().UnixNano(),
	})

	// 3. Mismatched NS in fallback dir (300/img/sp/quest/scene/mismatch_ns.png)
	mismatchNSFile := filepath.Join(tmpDir, "300", "img", "sp", "quest", "scene", "mismatch_ns.png")
	_ = os.MkdirAll(filepath.Dir(mismatchNSFile), 0755)
	_ = os.WriteFile(mismatchNSFile, pngMagic, 0644)
	fiMismatch, _ := os.Stat(mismatchNSFile)
	_ = writeDiskExtFile(mismatchNSFile, diskMeta{
		ContentType: "image/png",
		Namespace:   "external.host.com",
		Version:     1,
		Verified:    true,
		Size:        int64(len(pngMagic)),
		MTimeNano:   fiMismatch.ModTime().UnixNano(),
	})

	// 4. Numeric user files: 2026/photos/family.png & 300/personal/notes.txt
	userPhoto := filepath.Join(tmpDir, "2026", "photos", "family.png")
	_ = os.MkdirAll(filepath.Dir(userPhoto), 0755)
	_ = os.WriteFile(userPhoto, pngMagic, 0644)

	userNotes := filepath.Join(tmpDir, "300", "personal", "notes.txt")
	_ = os.MkdirAll(filepath.Dir(userNotes), 0755)
	_ = os.WriteFile(userNotes, []byte("private notes"), 0644)

	// 5. Tier-2 version fallback without .ext (300/js/no_ext.js)
	noExtFile := filepath.Join(tmpDir, "300", "js", "no_ext.js")
	_ = os.WriteFile(noExtFile, appCode, 0644)

	deleted, _, skipped := mgr.ClearAll()
	if deleted < 3 {
		t.Fatalf("expected at least 3 deleted files (app.js + app.js.ext + no_ext.js), got %d", deleted)
	}

	// Assertions
	if _, err := os.Stat(legitFallback); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be deleted", legitFallback)
	}
	if _, err := os.Stat(orphanExt); err != nil {
		t.Fatalf("expected orphan .ext %s to be preserved", orphanExt)
	}
	if _, err := os.Stat(mismatchNSFile); err != nil {
		t.Fatalf("expected mismatch NS file %s to be preserved", mismatchNSFile)
	}
	for _, keep := range []string{userPhoto, userNotes} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected unconfirmed user file %s to be preserved", keep)
		}
	}
	if _, err := os.Stat(noExtFile); !os.IsNotExist(err) {
		t.Fatalf("expected Tier-2 legacy version file %s to be deleted under root marker", noExtFile)
	}
	if skipped == 0 {
		t.Fatalf("expected skipped > 0, got %d", skipped)
	}
}

func TestClearAll_NamespaceSafetyMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	payload := []byte("console.log('clear_all_ns_matrix');")

	cases := []struct {
		name        string
		relPath     string
		metaNS      string
		shouldClean bool
	}{
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
		_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
		_ = os.WriteFile(fullPath, payload, 0644)
		fi, _ := os.Stat(fullPath)
		_ = writeDiskExtFile(fullPath, diskMeta{
			ContentType: "application/javascript",
			Version:     1,
			Verified:    true,
			Size:        fi.Size(),
			MTimeNano:   fi.ModTime().UnixNano(),
			Namespace:   tc.metaNS,
		})
	}

	mgr.ClearAll()

	for _, tc := range cases {
		fullPath := filepath.Join(tmpDir, filepath.FromSlash(tc.relPath))
		extPath := fullPath + ".ext"
		if tc.shouldClean {
			if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
				t.Errorf("[%s] expected data file %s to be deleted", tc.name, tc.relPath)
			}
			if _, err := os.Stat(extPath); !os.IsNotExist(err) {
				t.Errorf("[%s] expected sidecar %s to be deleted", tc.name, extPath)
			}
		} else {
			if _, err := os.Stat(fullPath); err != nil {
				t.Errorf("[%s] expected data file %s to be preserved: %v", tc.name, tc.relPath, err)
			}
			if _, err := os.Stat(extPath); err != nil {
				t.Errorf("[%s] expected sidecar %s to be preserved: %v", tc.name, extPath, err)
			}
		}
	}
}

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
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, validPng, 0644)
	}

	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	if mgr.HasRootMarker() {
		t.Fatalf("expected HasRootMarker to be false before confirmation")
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 || skipped < len(acgFiles) {
		t.Fatalf("expected deleted=0, skipped>=%d; got deleted=%d, skipped=%d", len(acgFiles), deleted, skipped)
	}

	if err := mgr.EnsureRootMarker(); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	if !mgr.HasRootMarker() {
		t.Fatalf("expected HasRootMarker to be true after EnsureRootMarker")
	}

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

	if !mgr.HasRootMarker() {
		t.Fatalf("expected root marker to remain intact after ClearAll")
	}
}

func TestClearAll_QuarantineFilesStrictMatchingAndUserPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	quarData := filepath.Join(tmpDir, "assets", "12345", "js", "tampered.js.quarantine.1730000000")
	quarExt := filepath.Join(tmpDir, "assets", "12345", "js", "tampered.js.ext.quarantine.1730000000")
	quarPng := filepath.Join(tmpDir, "assets", "img", "sp", "bad.png.quarantine.1730000001")
	_ = os.MkdirAll(filepath.Dir(quarData), 0755)
	_ = os.MkdirAll(filepath.Dir(quarPng), 0755)
	_ = os.WriteFile(quarData, []byte("tampered content"), 0644)
	_ = os.WriteFile(quarExt, []byte(`{"v":1}`), 0644)
	_ = os.WriteFile(quarPng, validPng, 0644)

	userDoc := filepath.Join(tmpDir, "x.quarantine.docx")
	userFolderDoc := filepath.Join(tmpDir, "personal_data", "notes.quarantine.123")
	userAssetDoc := filepath.Join(tmpDir, "assets", "12345", "user.docx.quarantine.123")
	_ = os.MkdirAll(filepath.Dir(userFolderDoc), 0755)
	_ = os.WriteFile(userDoc, []byte("user docx content"), 0644)
	_ = os.WriteFile(userFolderDoc, []byte("user notes content"), 0644)
	_ = os.WriteFile(userAssetDoc, []byte("user docx in assets"), 0644)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 3 || skipped < 3 {
		t.Fatalf("expected deleted=3, skipped>=3; got deleted=%d, skipped=%d", deleted, skipped)
	}

	for _, p := range []string{quarData, quarExt, quarPng} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected quarantine file %s to be deleted", p)
		}
	}

	for _, p := range []string{userDoc, userFolderDoc, userAssetDoc} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("BUG: user quarantine-like file %s was deleted: %v", p, err)
		}
	}
}

func TestClearAll_NonSyntaxPathWithValidExtDeleted(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	nonSyntaxFiles := []string{
		"assets/icon.png",
		"assets/common/img.png",
		"assets/random_module/asset.png",
	}

	for _, rel := range nonSyntaxFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, validPng, 0644)
		_ = os.WriteFile(full+".ext", []byte(`{"v":1,"ct":"image/png","ns":"gbf"}`), 0644)
	}

	deleted, _, _ := mgr.ClearAll()
	if deleted != len(nonSyntaxFiles)*2 {
		t.Fatalf("expected %d deleted files, got %d", len(nonSyntaxFiles)*2, deleted)
	}

	for _, rel := range nonSyntaxFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Errorf("expected %s to be deleted", rel)
		}
		if _, err := os.Stat(full + ".ext"); !os.IsNotExist(err) {
			t.Errorf("expected %s.ext to be deleted", rel)
		}
	}
}

func TestClearAll_HistoricalCompatibilitySafetyAndSeparation(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	validJS := []byte("console.log('client-code');")
	validIco := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}

	mustDeleteFiles := []struct {
		rel     string
		content []byte
		meta    string
	}{
		{"hosts/granbluefantasy.com/_app/style.css", []byte("body{margin:0}"), `{"v":1,"ct":"text/css","verified":true}`},
		{"hosts/game.granbluefantasy.jp/assets/font/basic.woff", []byte("woff-data"), `{"v":1,"ct":"application/font-woff","verified":true}`},
		{"hosts/ava-a.sp.mbga.jp/avatar.gif", []byte("GIF89a"), `{"v":1,"ct":"image/gif","verified":true}`},
		{"hosts/connect.mobage.jp/static/icon.png", validPng, `{"v":1,"ct":"image/png","verified":true}`},
		{"hosts/steam.granbluefantasy.com/icon.ico", validIco, `{"v":1,"ct":"image/x-icon","verified":true}`},
		{"jssdk/mobage-jssdk-client.3.10.1.min.js", validJS, `{"v":1,"ct":"application/javascript","ns":"gbf"}`},
		{"favicon.ico", validIco, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`},
	}

	for _, item := range mustDeleteFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(item.rel))
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, item.content, 0644)
		_ = os.WriteFile(full+".ext", []byte(item.meta), 0644)
	}

	mustPreserveFiles := []struct {
		rel     string
		content []byte
		meta    string
	}{
		{"hosts/untrusted-domain.com/photo.png", validPng, `{"v":1,"ct":"image/png","verified":true}`},
		{"hosts/malicious.org/script.js", validJS, `{"v":1,"ct":"application/javascript","verified":true}`},
		{"hosts/granbluefantasy.com/unverified.js", validJS, `{"v":1,"ct":"application/javascript","verified":false}`},
		{"notes.txt", []byte("personal notes"), ""},
		{"root_photo.png", validPng, ""},
		{"my_favicon.ico", validIco, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`},
		{"favicon.png", validPng, `{"v":1,"ct":"image/png","ns":"gbf"}`},
		{"jssdk_notes.txt", []byte("notes on jssdk"), ""},
		{"jssdk_backup/app.js", validJS, `{"v":1,"ct":"application/javascript","ns":"gbf"}`},
		{"jssdk/document.docx", []byte("secret document"), ""},
	}

	for _, item := range mustPreserveFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(item.rel))
		_ = os.MkdirAll(filepath.Dir(full), 0755)
		_ = os.WriteFile(full, item.content, 0644)
		if item.meta != "" {
			_ = os.WriteFile(full+".ext", []byte(item.meta), 0644)
		}
	}

	deleted, _, _ := mgr.ClearAll()
	expectedDeleted := len(mustDeleteFiles) * 2
	if deleted != expectedDeleted {
		t.Fatalf("expected exactly %d deleted files, got deleted=%d", expectedDeleted, deleted)
	}

	for _, item := range mustDeleteFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(item.rel))
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Errorf("expected %s to be deleted", item.rel)
		}
		if _, err := os.Stat(full + ".ext"); !os.IsNotExist(err) {
			t.Errorf("expected %s.ext to be deleted", item.rel)
		}
	}

	for _, item := range mustPreserveFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(item.rel))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected preserved file %s to exist", item.rel)
		}
		if item.meta != "" {
			if _, err := os.Stat(full + ".ext"); err != nil {
				t.Errorf("expected preserved sidecar %s.ext to exist", item.rel)
			}
		}
	}
}
