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
	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", headers, testValidPNG) {
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

	// User temp files & companion .ext in user directory
	userTempFiles := []string{
		filepath.Join(nestedUserDir, "photo.png.tmp.123.456"),
		filepath.Join(nestedUserDir, "photo.png.tmp.123.456.789"),
	}
	for _, f := range userTempFiles {
		if err := os.WriteFile(f, []byte("temp data"), 0644); err != nil {
			t.Fatalf("failed to create user temp file: %v", err)
		}
	}
	userPhotoExt := filepath.Join(nestedUserDir, "photo.png.ext")
	_ = os.WriteFile(userPhotoExt, []byte(`{"v":1,"size":19,"mtime_nano":123456789,"ns":"gbf"}`), 0644)

	// Pre-existing empty user directory (must NOT be pruned)
	emptyUserDir := filepath.Join(tmpDir, "user_workspace", "empty_sub")
	if err := os.MkdirAll(emptyUserDir, 0755); err != nil {
		t.Fatalf("failed to create empty user dir: %v", err)
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
	if skipped < 8 {
		t.Errorf("expected at least 8 skipped non-cache user files, got skipped=%d", skipped)
	}

	// User files MUST NOT be deleted
	allUserFiles := append([]string{userDocPath, userNotesPath, nestedFilePath, userTmpDocPath, nestedTmpDocPath, userExtPath, userPhotoExt}, userTempFiles...)
	for _, p := range allUserFiles {
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

	// Pre-existing empty user directory MUST NOT be pruned
	if fi, err := os.Stat(emptyUserDir); err != nil || !fi.IsDir() {
		t.Errorf("BUG: empty user directory %s was pruned: %v", emptyUserDir, err)
	}
}

func TestClearAll_FilesWithoutExtStrictlyPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()
	_ = EnsureRootMarker(tmpDir)

	legacyFile := writeTestDiskFile(t, tmpDir, "assets/img/sp/legacy.png", testValidPNG)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 {
		t.Errorf("expected 0 deleted files without .ext metadata, got %d", deleted)
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped file, got %d", skipped)
	}
	if _, err := os.Stat(legacyFile); err != nil {
		t.Errorf("expected file without .ext to be preserved: %v", err)
	}
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

func TestClearAll_RejectsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	extDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	externalPng := writeTestDiskFile(t, extDir, "important_secret.png", testValidPNG)

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

	if content, err := os.ReadFile(externalPng); err != nil || len(content) != len(testValidPNG) {
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

	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/replace_me.png", testPNGHeaders, testValidPNG) {
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

func TestClearAll_CleanCacheNoFalseSkippedCount(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	_ = mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/clean.png", testPNGHeaders, testValidPNG)

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

	validMp3 := []byte("ID3\x04\x00\x00\x00\x00\x00#TIT2\x00\x00\x00\x05\x00\x00\x03test\x00")
	validJs := []byte(`(function(){ console.log("custom script"); })();`)
	validCss := []byte(`body { margin: 0; background-color: #ffffff; }`)

	// 1. User files that MUST be preserved across various directory shapes
	type userFileEntry struct {
		rel  string
		data []byte
	}
	userFiles := []userFileEntry{
		{"personal_data/quest/photo.png", testValidPNG},
		{"personal_data/photo.png", testValidPNG},
		{"user_workspace/private/photo.png", testValidPNG},
		{"my_backup/sound/audio.mp3", validMp3},
		{"user_scripts/custom.js", validJs},
		{"my_styles/theme.css", validCss},
		{"2026/photos/event.png", testValidPNG},
		{"assets/document.docx", []byte("docx content")},
		{"assets/12345/archive.zip", []byte("zip content")},
		{"assets/12345/document.docx", []byte("docx content 2")},
		{"12345/personal/test.png", testValidPNG},
	}

	for _, uf := range userFiles {
		writeTestDiskFile(t, tmpDir, uf.rel, uf.data)
	}

	// Also add user photo with paired .ext in non-cache path
	pairedUserPhoto := writeTestDiskFile(t, tmpDir, "personal_data/paired.png", testValidPNG)
	fi, _ := os.Stat(pairedUserPhoto)
	_ = os.WriteFile(pairedUserPhoto+".ext", []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano())), 0644)

	// 2. Real cache files that SHOULD be deleted
	legitCacheFiles := []struct {
		rel  string
		data []byte
	}{
		{"assets/12345/img/sp/btn.png", testValidPNG},
		{"assets/img/sp/quest.png", testValidPNG},
		{"sound/voice/attack.mp3", validMp3},
	}
	for _, cf := range legitCacheFiles {
		p := writeTestDiskFile(t, tmpDir, cf.rel, cf.data)
		_ = os.WriteFile(p+".ext", []byte(`{"v":1,"ns":"gbf"}`), 0644)
	}

	deleted, _, skipped := mgr.ClearAll()
	if deleted != len(legitCacheFiles)*2 {
		t.Errorf("expected %d deleted legit cache files (data+ext), got %d", len(legitCacheFiles)*2, deleted)
	}

	for _, cf := range legitCacheFiles {
		p := filepath.Join(tmpDir, filepath.FromSlash(cf.rel))
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected legit cache file %s to be deleted", p)
		}
		if _, err := os.Stat(p + ".ext"); !os.IsNotExist(err) {
			t.Errorf("expected legit cache metadata %s.ext to be deleted", p)
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

			pngPath := writeTestDiskFile(t, tmpDir, "assets/12345/img/sp/target.png", testValidPNG)
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

func TestClearAll_RootMarkerValidationRejectsInvalid(t *testing.T) {
	tests := []struct {
		name      string
		setupRoot func(t *testing.T, tmpDir string)
	}{
		{"wrong marker content", func(t *testing.T, tmpDir string) {
			_ = os.WriteFile(filepath.Join(tmpDir, RootMarkerFileName), []byte("Wrong content\n"), 0644)
		}},
		{"empty marker file", func(t *testing.T, tmpDir string) {
			_ = os.WriteFile(filepath.Join(tmpDir, RootMarkerFileName), nil, 0644)
		}},
		{"directory as marker", func(t *testing.T, tmpDir string) {
			markerPath := filepath.Join(tmpDir, RootMarkerFileName)
			_ = os.Remove(markerPath)
			_ = os.Mkdir(markerPath, 0755)
		}},
		{"symlink as marker", func(t *testing.T, tmpDir string) {
			markerPath := filepath.Join(tmpDir, RootMarkerFileName)
			_ = os.Remove(markerPath)
			extTarget := filepath.Join(t.TempDir(), "target.txt")
			_ = os.WriteFile(extTarget, []byte(RootMarkerExpectedContent), 0644)
			if err := os.Symlink(extTarget, markerPath); err != nil {
				t.Skipf("skipping symlink: %v", err)
			}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			mgr := NewManager(tmpDir, 64)
			defer mgr.Close()

			tc.setupRoot(t, tmpDir)
			legacyFile := writeTestDiskFile(t, tmpDir, "assets/img/sp/legacy.png", testValidPNG)

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
	if deleted != 2 {
		t.Fatalf("expected exactly 2 deleted files (app.js + app.js.ext), got %d", deleted)
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
	for _, keep := range []string{userPhoto, userNotes, noExtFile} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected unconfirmed file %s to be preserved", keep)
		}
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
		fullPath := writeTestDiskFile(t, tmpDir, tc.relPath, payload)
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

	acgFiles := []string{
		"assets/12345/img/sp/quest.png",
		"assets/img/sp/btn.png",
		"assets/common/img.png",
	}

	for _, rel := range acgFiles {
		writeTestDiskFile(t, tmpDir, rel, testValidPNG)
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

	deletedAfter, _, skippedAfter := mgr.ClearAll()
	if deletedAfter != 0 || skippedAfter < len(acgFiles) {
		t.Fatalf("expected 0 deleted files after confirmation for files without .ext, got deleted=%d, skipped=%d", deletedAfter, skippedAfter)
	}
	for _, rel := range acgFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("expected file %s to be preserved without .ext", rel)
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

	quarData := writeTestDiskFile(t, tmpDir, "assets/12345/js/tampered.js.quarantine.1730000000", []byte("tampered content"))
	quarExt := writeTestDiskFile(t, tmpDir, "assets/12345/js/tampered.js.ext.quarantine.1730000000", []byte(`{"v":1}`))
	quarPng := writeTestDiskFile(t, tmpDir, "assets/img/sp/bad.png.quarantine.1730000001", testValidPNG)

	userDoc := writeTestDiskFile(t, tmpDir, "x.quarantine.docx", []byte("user docx content"))
	userFolderDoc := writeTestDiskFile(t, tmpDir, "personal_data/notes.quarantine.123", []byte("user notes content"))
	userAssetDoc := writeTestDiskFile(t, tmpDir, "assets/12345/user.docx.quarantine.123", []byte("user docx in assets"))

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

func TestClearAll_HistoricalCompatibilitySafetyAndSeparation(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

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
		{"hosts/connect.mobage.jp/static/icon.png", testValidPNG, `{"v":1,"ct":"image/png","verified":true}`},
		{"hosts/steam.granbluefantasy.com/icon.ico", validIco, `{"v":1,"ct":"image/x-icon","verified":true}`},
		{"jssdk/mobage-jssdk-client.3.10.1.min.js", validJS, `{"v":1,"ct":"application/javascript","ns":"gbf"}`},
		{"favicon.ico", validIco, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`},
		{"assets/icon.png", testValidPNG, `{"v":1,"ct":"image/png","ns":"gbf"}`},
		{"assets/common/img.png", testValidPNG, `{"v":1,"ct":"image/png","ns":"gbf"}`},
		{"assets/random_module/asset.png", testValidPNG, `{"v":1,"ct":"image/png","ns":"gbf"}`},
	}

	for _, item := range mustDeleteFiles {
		p := writeTestDiskFile(t, tmpDir, item.rel, item.content)
		_ = os.WriteFile(p+".ext", []byte(item.meta), 0644)
	}

	mustPreserveFiles := []struct {
		rel     string
		content []byte
		meta    string
	}{
		{"hosts/untrusted-domain.com/photo.png", testValidPNG, `{"v":1,"ct":"image/png","verified":true}`},
		{"hosts/malicious.org/script.js", validJS, `{"v":1,"ct":"application/javascript","verified":true}`},
		{"hosts/granbluefantasy.com/unverified.js", validJS, `{"v":1,"ct":"application/javascript","verified":false}`},
		{"notes.txt", []byte("personal notes"), ""},
		{"root_photo.png", testValidPNG, ""},
		{"my_favicon.ico", validIco, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`},
		{"favicon.png", testValidPNG, `{"v":1,"ct":"image/png","ns":"gbf"}`},
		{"jssdk_notes.txt", []byte("notes on jssdk"), ""},
		{"jssdk_backup/app.js", validJS, `{"v":1,"ct":"application/javascript","ns":"gbf"}`},
		{"jssdk/document.docx", []byte("secret document"), ""},
	}

	for _, item := range mustPreserveFiles {
		p := writeTestDiskFile(t, tmpDir, item.rel, item.content)
		if item.meta != "" {
			_ = os.WriteFile(p+".ext", []byte(item.meta), 0644)
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
