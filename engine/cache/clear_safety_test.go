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

	if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/btn.png", testPNGHeaders, testValidPNG) {
		t.Fatal("failed to save valid cache asset")
	}

	userFiles := []string{
		"doc.txt", "notes.pdf", "personal_data/report.docx", "draft.tmp.txt", "personal_data/notes.tmp.pdf",
		"user_config.ext", "personal_data/photo.png.tmp.123.456", "personal_data/photo.png.tmp.123.456.789",
		"personal_data/photo.png.ext", "my_backup/sound/audio.mp3", "assets/document.docx",
		"2026/photos/event.png", ".DS_Store", "Thumbs.db", "desktop.ini",
	}
	for _, rel := range userFiles {
		writeTestDiskFile(t, tmpDir, rel, testValidPNG)
	}

	emptyUserDir := filepath.Join(tmpDir, "user_workspace", "empty_sub")
	_ = os.MkdirAll(emptyUserDir, 0755)
	writeTestDiskFile(t, tmpDir, "assets/12345/img/sp/stale.png.tmp.9999.123456789.1", []byte("stale temp"))

	deleted, freed, skipped := mgr.ClearAll()
	assertPathStatus(t, tmpDir, []string{"assets/12345/img/sp/btn.png", "assets/12345/img/sp/btn.png.ext", "assets/12345/img/sp/stale.png.tmp.9999.123456789.1"}, false)
	if deleted < 3 || freed <= 0 || skipped < 11 {
		t.Errorf("unexpected counts: deleted=%d, freed=%d, skipped=%d", deleted, freed, skipped)
	}

	assertPathStatus(t, tmpDir, userFiles, true)
	if _, err := os.Stat(filepath.Join(tmpDir, "assets", "12345", "img", "sp")); !os.IsNotExist(err) {
		t.Error("expected empty cache directory pruned")
	}
	assertPathStatus(t, tmpDir, []string{"personal_data", "my_backup", "2026", "user_workspace/empty_sub"}, true)
}

func TestClearAll_SafetyInvariants(t *testing.T) {
	t.Run("RefusesUnsafeDirectory", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			t.Skip("UserHomeDir not available")
		}
		mgr := NewManager(home, 64)
		defer mgr.Close()
		if del, freed, skip := mgr.ClearAll(); del != 0 || freed != 0 || skip != 0 {
			t.Fatalf("expected (0, 0, 0), got (%d, %d, %d)", del, freed, skip)
		}
	})

	t.Run("FilesWithoutExtPreservedAndCleanCache", func(t *testing.T) {
		tmpDir := t.TempDir()
		legacy := []string{"assets/12345/img/sp/quest.png", "assets/img/sp/btn.png", "assets/common/img.png"}
		for _, rel := range legacy { writeTestDiskFile(t, tmpDir, rel, testValidPNG) }
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		for _, marker := range []bool{false, true} {
			if marker { _ = mgr.EnsureRootMarker() }
			if del, _, skip := mgr.ClearAll(); del != 0 || skip < len(legacy) {
				t.Fatalf("marker=%v: expected del=0, skip>=%d; got (%d, %d)", marker, len(legacy), del, skip)
			}
		}
		assertPathStatus(t, tmpDir, legacy, true)
		_ = mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/clean.png", testPNGHeaders, testValidPNG)
		if del, freed, skip := mgr.ClearAll(); del != 2 || freed <= 0 || skip != len(legacy) {
			t.Fatalf("clean cache: expected del=2, freed>0, skip=%d; got (%d, %d, %d)", len(legacy), del, freed, skip)
		}
	})

	t.Run("RejectsSymlinksAndReplacedFiles", func(t *testing.T) {
		tmpDir, extDir := t.TempDir(), t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		extPng := writeTestDiskFile(t, extDir, "secret.png", testValidPNG)
		subDir := filepath.Join(tmpDir, "assets/12345/img/sp")
		_ = os.MkdirAll(subDir, 0755)
		for _, name := range []string{"sym.png", "fake.png.tmp.1234.5678.1", "sym.png.ext"} {
			if err := os.Symlink(extPng, filepath.Join(subDir, name)); err != nil {
				t.Skipf("skipping symlink: %v", err)
			}
		}
		_ = EnsureRootMarker(tmpDir)
		if del, _, skip := mgr.ClearAll(); del != 0 || skip < 3 { t.Fatalf("expected del=0, skip>=3; got (%d, %d)", del, skip) }
		if content, err := os.ReadFile(extPng); err != nil || len(content) != len(testValidPNG) { t.Fatal("external file corrupted") }

		if !mgr.SaveWithNamespace("gbf", "assets/12345/img/sp/replace_me.png", testPNGHeaders, testValidPNG) { t.Fatal("failed to save asset") }
		targetPath, _ := mgr.resolvePathWithNamespace("gbf", "assets/12345/img/sp/replace_me.png")
		mgr.SetBeforeClearAllRemoveHookForTest(func(rel string) {
			if strings.HasSuffix(filepath.ToSlash(rel), "replace_me.png") {
				_ = os.Remove(targetPath); _ = os.WriteFile(targetPath, []byte("attacker replaced content"), 0644)
			}
		})
		defer mgr.SetBeforeClearAllRemoveHookForTest(nil)
		if del, _, skip := mgr.ClearAll(); del != 0 || skip < 1 { t.Fatalf("expected del=0, skip>=1; got (%d, %d)", del, skip) }
		if c, _ := os.ReadFile(targetPath); string(c) != "attacker replaced content" { t.Fatal("replaced file was modified") }
	})

	t.Run("MismatchedExtAndOrphansPreserved", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()
		_ = EnsureRootMarker(tmpDir)

		baseMeta := `{"v":1,"size":100,"mtime":123,"at":123,"ct":"image/png","verified":true,"ns":"gbf"}`
		cases := []struct {
			rel, meta string
			expectDel bool
		}{
			{"assets/12345/img/sp/sz.png", strings.Replace(baseMeta, "100", "99999", 1), true}, {"assets/12345/img/sp/mt.png", strings.Replace(baseMeta, "123", "99999", 1), true},
			{"assets/12345/img/sp/brk.png", "broken non-json", false}, {"assets/12345/img/sp/empty.png", "", false},
			{"assets/12345/img/sp/v2.png", strings.Replace(baseMeta, `"v":1`, `"v":2`, 1), false}, {"assets/12345/img/sp/ns.png", strings.Replace(baseMeta, `"ns":"gbf"`, `"ns":"other"`, 1), false},
		}
		for _, tc := range cases { writeTestAsset(t, tmpDir, tc.rel, testValidPNG, []byte(tc.meta)) }
		orphans := []string{
			"assets/12345/img/sp/no_verified.png.ext", "assets/12345/img/sp/no_mtime.png.ext",
			"assets/12345/img/sp/orphan_v2.png.ext", "personal_data/orphan_photo.png.ext", "assets/12345/img/sp/complete_orphan.png.ext",
		}
		for _, rel := range orphans { writeTestDiskFile(t, tmpDir, rel, []byte(baseMeta)) }
		if del, _, skip := mgr.ClearAll(); del != 4 || skip < len(orphans) {
			t.Fatalf("expected del=4, skip>=%d; got (%d, %d)", len(orphans), del, skip)
		}
		for _, tc := range cases { assertPathStatus(t, tmpDir, []string{tc.rel, tc.rel + ".ext"}, !tc.expectDel) }
		assertPathStatus(t, tmpDir, orphans, true)
	})

	t.Run("RootMarkerValidationRejectsInvalid", func(t *testing.T) {
		for _, kind := range []string{"corrupt", "empty", "dir", "symlink"} {
			tmpDir := t.TempDir()
			mgr := NewManager(tmpDir, 64)
			defer mgr.Close()
			writeTestInvalidMarker(t, tmpDir, kind)
			writeTestDiskFile(t, tmpDir, "assets/img/sp/legacy.png", testValidPNG)
			if mgr.HasRootMarker() || func() bool { del, _, skip := mgr.ClearAll(); return del != 0 || skip != 1 }() {
				t.Fatalf("[%s] root marker validation failed", kind)
			}
			assertPathStatus(t, tmpDir, []string{"assets/img/sp/legacy.png"}, true)
		}
	})

	t.Run("QuarantineFilesAndUserPreserved", func(t *testing.T) {
		tmpDir := t.TempDir()
		_ = EnsureRootMarker(tmpDir)
		mgr := NewManager(tmpDir, 64)
		defer mgr.Close()

		quar := []string{"assets/12345/js/tampered.js.quarantine.1730000000", "assets/12345/js/tampered.js.ext.quarantine.1730000000", "assets/img/sp/bad.png.quarantine.1730000001"}
		for _, rel := range quar { writeTestDiskFile(t, tmpDir, rel, []byte("quarantine")) }
		user := []string{"x.quarantine.docx", "personal_data/notes.quarantine.123", "assets/12345/user.docx.quarantine.123"}
		for _, rel := range user { writeTestDiskFile(t, tmpDir, rel, []byte("user docx")) }
		if del, _, skip := mgr.ClearAll(); del != 3 || skip < 3 { t.Fatalf("expected del=3, skip>=3; got (%d, %d)", del, skip) }
		assertPathStatus(t, tmpDir, quar, false)
		assertPathStatus(t, tmpDir, user, true)
	})
}

func TestClearAll_NamespaceAndDomainSafety(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	validJS, validIco := []byte("console.log('domain_safety');"), []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}
	jsMeta, emptyMeta, icoMeta := `{"v":1,"ns":"gbf","ct":"application/javascript"}`, `{"v":1,"ct":"application/javascript"}`, `{"v":1,"ct":"image/x-icon","ns":"gbf"}`

	cases := []struct {
		relPath, meta string
		content       []byte
		shouldClean   bool
	}{
		{"assets/300/js/canonical_valid.js", jsMeta, validJS, true}, {"assets/300/js/canonical_empty.js", emptyMeta, validJS, true},
		{"300/js/fallback_valid.js", jsMeta, validJS, true}, {"300/js/fallback_empty.js", emptyMeta, validJS, false},
		{"300/js/app.js", `{"v":1,"ct":"application/javascript","ns":"gbf","verified":true}`, validJS, true}, {"2026/photos/family.png", "", testValidPNG, false},
		{"300/personal/notes.txt", "", []byte("private"), false}, {"300/js/no_ext.js", "", validJS, false},
		{"hosts/sub.example.com/assets/js/host_gbf.js", jsMeta, validJS, true}, {"hosts/sub.example.com/assets/js/host_direct.js", `{"v":1,"ns":"sub.example.com","ct":"application/javascript"}`, validJS, true},
		{"hosts/sub.example.com/assets/js/host_mismatch.js", `{"v":1,"ns":"other.example.com","ct":"application/javascript"}`, validJS, false}, {"hosts/granbluefantasy.com/_app/style.css", `{"v":1,"ct":"text/css","verified":true}`, []byte("body{}"), true},
		{"hosts/granbluefantasy.com/unverified.js", `{"v":1,"ct":"application/javascript","verified":false}`, validJS, false}, {"hosts/untrusted-domain.com/photo.png", `{"v":1,"ct":"image/png","verified":true}`, testValidPNG, false},
		{"jssdk/mobage-jssdk-client.3.10.1.min.js", jsMeta, validJS, true}, {"jssdk_backup/app.js", jsMeta, validJS, false},
		{"favicon.ico", icoMeta, validIco, true}, {"my_favicon.ico", icoMeta, validIco, false},
	}

	expectedDeleted := 0
	for _, tc := range cases {
		var extBytes []byte
		if tc.meta != "" { extBytes = []byte(tc.meta) }
		writeTestAsset(t, tmpDir, tc.relPath, tc.content, extBytes)
		if tc.shouldClean { expectedDeleted += 2 }
	}

	if deleted, _, _ := mgr.ClearAll(); deleted != expectedDeleted {
		t.Fatalf("expected %d deleted files, got %d", expectedDeleted, deleted)
	}

	for _, tc := range cases {
		pList := []string{tc.relPath}
		if tc.meta != "" { pList = append(pList, tc.relPath+".ext") }
		assertPathStatus(t, tmpDir, pList, !tc.shouldClean)
	}
}
