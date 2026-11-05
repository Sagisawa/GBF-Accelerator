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

	// User files in paths containing category words or numeric names (must NOT be misidentified)
	userBackupAudio := filepath.Join(tmpDir, "my_backup", "sound", "audio.mp3")
	_ = os.MkdirAll(filepath.Dir(userBackupAudio), 0755)
	_ = os.WriteFile(userBackupAudio, []byte("ID3audio"), 0644)

	userCategoryDoc := filepath.Join(tmpDir, "assets", "document.docx")
	_ = os.MkdirAll(filepath.Dir(userCategoryDoc), 0755)
	_ = os.WriteFile(userCategoryDoc, []byte("docx content"), 0644)

	userYearPhoto := filepath.Join(tmpDir, "2026", "photos", "event.png")
	_ = os.MkdirAll(filepath.Dir(userYearPhoto), 0755)
	_ = os.WriteFile(userYearPhoto, testValidPNG, 0644)

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
	if skipped < 11 {
		t.Errorf("expected at least 11 skipped non-cache user files, got skipped=%d", skipped)
	}

	// User files MUST NOT be deleted
	allUserFiles := append([]string{userDocPath, userNotesPath, nestedFilePath, userTmpDocPath, nestedTmpDocPath, userExtPath, userPhotoExt, userBackupAudio, userCategoryDoc, userYearPhoto}, userTempFiles...)
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

	// User directories MUST NOT be pruned
	for _, ud := range []string{nestedUserDir, filepath.Join(tmpDir, "my_backup"), filepath.Join(tmpDir, "2026"), emptyUserDir} {
		if fi, err := os.Stat(ud); err != nil || !fi.IsDir() {
			t.Errorf("BUG: user directory %s was pruned: %v", ud, err)
		}
	}
}

func TestClearAll_FilesWithoutExtStrictlyPreserved(t *testing.T) {
	tmpDir := t.TempDir()

	legacyFiles := []string{
		"assets/12345/img/sp/quest.png",
		"assets/img/sp/btn.png",
		"assets/common/img.png",
	}
	for _, rel := range legacyFiles {
		writeTestDiskFile(t, tmpDir, rel, testValidPNG)
	}

	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	// 1. Without root marker: ClearAll must delete 0 files
	if mgr.HasRootMarker() {
		t.Fatalf("expected HasRootMarker to be false initially")
	}
	deleted, _, skipped := mgr.ClearAll()
	if deleted != 0 || skipped < len(legacyFiles) {
		t.Errorf("expected deleted=0, skipped>=%d; got deleted=%d, skipped=%d", len(legacyFiles), deleted, skipped)
	}

	// 2. With root marker: files without .ext must STILL be strictly preserved (0 deleted)
	if err := mgr.EnsureRootMarker(); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	deletedAfter, _, skippedAfter := mgr.ClearAll()
	if deletedAfter != 0 || skippedAfter < len(legacyFiles) {
		t.Errorf("expected 0 deleted files for files without .ext even with marker, got deleted=%d, skipped=%d", deletedAfter, skippedAfter)
	}
	for _, rel := range legacyFiles {
		full := filepath.Join(tmpDir, filepath.FromSlash(rel))
		if _, err := os.Stat(full); err != nil {
			t.Errorf("expected file %s without .ext to be preserved: %v", rel, err)
		}
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
	pngMagic := []byte("\x89PNG\r\n\x1a\nphoto")

	// 1. Legit version fallback with matching sidecar (300/js/app.js) -> deleted
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

	// 2. Numeric user files & unconfirmed version assets (2026/photos/family.png & 300/js/no_ext.js) -> preserved
	userPhoto := writeTestDiskFile(t, tmpDir, "2026/photos/family.png", pngMagic)
	userNotes := writeTestDiskFile(t, tmpDir, "300/personal/notes.txt", []byte("private notes"))
	noExtFile := writeTestDiskFile(t, tmpDir, "300/js/no_ext.js", appCode)

	deleted, _, skipped := mgr.ClearAll()
	if deleted != 2 {
		t.Fatalf("expected exactly 2 deleted files (app.js + app.js.ext), got %d", deleted)
	}

	if _, err := os.Stat(legitFallback); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be deleted", legitFallback)
	}
	for _, keep := range []string{userPhoto, userNotes, noExtFile} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected unconfirmed file %s to be preserved: %v", keep, err)
		}
	}
	if skipped < 3 {
		t.Fatalf("expected skipped >= 3, got %d", skipped)
	}
}

func TestClearAll_NamespaceAndDomainSafety(t *testing.T) {
	tmpDir := t.TempDir()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	validJS := []byte("console.log('domain_safety');")
	validIco := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}

	cases := []struct {
		name        string
		relPath     string
		content     []byte
		meta        string
		shouldClean bool
	}{
		// Canonical GBF paths (valid & legacy empty NS)
		{"Canonical_ValidNS", "assets/300/js/canonical_valid.js", validJS, `{"v":1,"ns":"gbf","ct":"application/javascript"}`, true},
		{"Canonical_EmptyNS", "assets/300/js/canonical_empty.js", validJS, `{"v":1,"ct":"application/javascript"}`, true},

		// Version fallback paths (strictly require "gbf" NS)
		{"Fallback_ValidNS", "300/js/fallback_valid.js", validJS, `{"v":1,"ns":"gbf","ct":"application/javascript"}`, true},
		{"Fallback_EmptyNS", "300/js/fallback_empty.js", validJS, `{"v":1,"ct":"application/javascript"}`, false},

		// Host paths: GBF NS & direct hostname NS accepted
		{"Hosts_GBF_NS", "hosts/sub.example.com/assets/js/host_gbf.js", validJS, `{"v":1,"ns":"gbf","ct":"application/javascript"}`, true},
		{"Hosts_Hostname_NS", "hosts/sub.example.com/assets/js/host_direct.js", validJS, `{"v":1,"ns":"sub.example.com","ct":"application/javascript"}`, true},
		{"Hosts_MismatchNS", "hosts/sub.example.com/assets/js/host_mismatch.js", validJS, `{"v":1,"ns":"other.example.com","ct":"application/javascript"}`, false},

		// Historical official GBF hosts with empty NS (requires Verified=true)
		{"OfficialHost_Verified", "hosts/granbluefantasy.com/_app/style.css", []byte("body{}"), `{"v":1,"ct":"text/css","verified":true}`, true},
		{"OfficialHost_Unverified", "hosts/granbluefantasy.com/unverified.js", validJS, `{"v":1,"ct":"application/javascript","verified":false}`, false},
		{"UntrustedHost_EmptyNS", "hosts/untrusted-domain.com/photo.png", testValidPNG, `{"v":1,"ct":"image/png","verified":true}`, false},

		// JSSDK & Root Favicon
		{"JSSDK_Valid", "jssdk/mobage-jssdk-client.3.10.1.min.js", validJS, `{"v":1,"ct":"application/javascript","ns":"gbf"}`, true},
		{"JSSDK_Backup_Invalid", "jssdk_backup/app.js", validJS, `{"v":1,"ct":"application/javascript","ns":"gbf"}`, false},
		{"Favicon_Valid", "favicon.ico", validIco, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`, true},
		{"Favicon_InvalidName", "my_favicon.ico", validIco, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`, false},
	}

	for _, tc := range cases {
		fullPath := writeTestDiskFile(t, tmpDir, tc.relPath, tc.content)
		if tc.meta != "" {
			_ = os.WriteFile(fullPath+".ext", []byte(tc.meta), 0644)
		}
	}

	deleted, _, _ := mgr.ClearAll()
	var expectedDeleted int
	for _, tc := range cases {
		if tc.shouldClean {
			expectedDeleted += 2 // data + ext
		}
	}
	if deleted != expectedDeleted {
		t.Fatalf("expected %d deleted files, got %d", expectedDeleted, deleted)
	}

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
			if tc.meta != "" {
				if _, err := os.Stat(extPath); err != nil {
					t.Errorf("[%s] expected sidecar %s to be preserved: %v", tc.name, extPath, err)
				}
			}
		}
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
