package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsConfirmedCacheFile_Tier1ValidExt(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	headers := map[string]string{
		"content-type": "image/png",
		"etag":         `"test-etag"`,
	}
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Save file with .ext metadata
	relPath := "assets/12345/img/sp/test.png"
	if !mgr.SaveWithNamespace("gbf", relPath, headers, validPng) {
		t.Fatalf("failed to save asset")
	}

	fullPath, ok := mgr.resolvePathWithNamespace("gbf", relPath)
	if !ok {
		t.Fatalf("failed to resolve path")
	}

	// Tier 1: Must confirm even WITHOUT root marker (hasRootMarker = false)
	if !IsConfirmedCacheFile(tmpDir, fullPath, false) {
		t.Errorf("expected Tier 1 asset with valid .ext to be confirmed even without root marker")
	}

	// Also confirmed when hasRootMarker = true
	if !IsConfirmedCacheFile(tmpDir, fullPath, true) {
		t.Errorf("expected Tier 1 asset to be confirmed with root marker")
	}
}

func TestIsConfirmedCacheFile_Tier2LegacyWithoutExt(t *testing.T) {
	tmpDir := t.TempDir()
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// Create legacy ACGPower cache structure directly on disk (no .ext file)
	legacyRel := filepath.Join("assets", "img", "sp", "legacy.png")
	legacyFull := filepath.Join(tmpDir, legacyRel)
	if err := os.MkdirAll(filepath.Dir(legacyFull), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(legacyFull, validPng, 0644); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Case A: Without root marker -> MUST be rejected!
	if IsConfirmedCacheFile(tmpDir, legacyFull, false) {
		t.Errorf("legacy asset without .ext must NOT be confirmed if root marker is missing")
	}

	// Case B: With root marker -> confirmed!
	if !IsConfirmedCacheFile(tmpDir, legacyFull, true) {
		t.Errorf("legacy asset without .ext must be confirmed when root marker is present")
	}

	// Case C: Fake PNG (corrupted magic bytes) with root marker -> MUST be rejected!
	fakePngFull := filepath.Join(tmpDir, "assets", "img", "sp", "fake.png")
	if err := os.WriteFile(fakePngFull, []byte("not a png file"), 0644); err != nil {
		t.Fatalf("write fake failed: %v", err)
	}
	if IsConfirmedCacheFile(tmpDir, fakePngFull, true) {
		t.Errorf("corrupted/fake image magic bytes must NOT be confirmed")
	}

	// Case D: HTML error page disguised as JS file -> MUST be rejected!
	fakeJSFull := filepath.Join(tmpDir, "assets", "js", "bad.js")
	_ = os.MkdirAll(filepath.Dir(fakeJSFull), 0755)
	if err := os.WriteFile(fakeJSFull, []byte("<html><head><title>503 Service Unavailable</title></head></html>"), 0644); err != nil {
		t.Fatalf("write fake js failed: %v", err)
	}
	if IsConfirmedCacheFile(tmpDir, fakeJSFull, true) {
		t.Errorf("HTML error content must NOT be confirmed as cache asset")
	}
}

func TestIsConfirmedCacheFile_RejectsNonCacheFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Files in cache root (e.g. doc.txt, notes.pdf, or even a .png in root)
	rootFiles := []string{"doc.txt", "notes.pdf", "data.csv", "image.png"}
	for _, name := range rootFiles {
		p := filepath.Join(tmpDir, name)
		_ = os.WriteFile(p, []byte("test content"), 0644)
		if IsConfirmedCacheFile(tmpDir, p, true) {
			t.Errorf("file in cache root %q must NOT be confirmed", name)
		}
	}

	// 2. Nested user files with non-cache extensions (including .csv)
	userDir := filepath.Join(tmpDir, "user_folder")
	_ = os.MkdirAll(userDir, 0755)
	nonCacheExts := []string{"document.docx", "data.csv", "sheet.xlsx", "archive.zip", "exec.exe"}
	for _, name := range nonCacheExts {
		p := filepath.Join(userDir, name)
		_ = os.WriteFile(p, []byte("test content"), 0644)
		if IsConfirmedCacheFile(tmpDir, p, true) {
			t.Errorf("user file with non-cache extension %q must NOT be confirmed", name)
		}
	}

	// 3. Empty files (size == 0)
	emptyFile := filepath.Join(tmpDir, "assets", "img", "sp", "empty.png")
	_ = os.MkdirAll(filepath.Dir(emptyFile), 0755)
	_ = os.WriteFile(emptyFile, []byte{}, 0644)
	if IsConfirmedCacheFile(tmpDir, emptyFile, true) {
		t.Errorf("empty file (size 0) must NOT be confirmed")
	}

	// 4. System junk files
	junkFiles := []string{".DS_Store", "Thumbs.db", "desktop.ini", "._asset.png"}
	for _, name := range junkFiles {
		p := filepath.Join(tmpDir, "assets", "img", "sp", name)
		_ = os.WriteFile(p, []byte("junk"), 0644)
		if IsConfirmedCacheFile(tmpDir, p, true) {
			t.Errorf("system junk file %q must NOT be confirmed", name)
		}
	}

	// 5. Root marker itself
	markerFile := filepath.Join(tmpDir, RootMarkerFileName)
	_ = os.WriteFile(markerFile, []byte("marker"), 0644)
	if IsConfirmedCacheFile(tmpDir, markerFile, true) {
		t.Errorf("root marker file itself must NOT be confirmed as cache asset")
	}
}

func TestRootMarkerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	if HasRootMarker(tmpDir) {
		t.Errorf("expected HasRootMarker to be false initially")
	}

	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}

	if !HasRootMarker(tmpDir) {
		t.Errorf("expected HasRootMarker to be true after EnsureRootMarker")
	}

	// Idempotent call
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("second EnsureRootMarker failed: %v", err)
	}

	// Subtest: Root marker with invalid content -> HasRootMarker returns false
	wrongMarkerPath := filepath.Join(tmpDir, RootMarkerFileName)
	if err := os.WriteFile(wrongMarkerPath, []byte("Random corrupt content"), 0644); err != nil {
		t.Fatalf("failed to overwrite marker: %v", err)
	}
	if HasRootMarker(tmpDir) {
		t.Errorf("expected HasRootMarker to be false for invalid content")
	}

	// EnsureRootMarker must refuse to overwrite invalid/existing content to protect user files
	if err := EnsureRootMarker(tmpDir); err == nil {
		t.Errorf("expected EnsureRootMarker to fail on existing unrecognized file, got nil")
	}
	content, _ := os.ReadFile(wrongMarkerPath)
	if string(content) != "Random corrupt content" {
		t.Errorf("EnsureRootMarker modified existing file content: %s", string(content))
	}
	if HasRootMarker(tmpDir) {
		t.Errorf("expected HasRootMarker to remain false when marker is invalid")
	}

	// Subtest: Root marker as directory -> HasRootMarker returns false, EnsureRootMarker returns error
	dirMarkerDir := t.TempDir()
	dirMarkerPath := filepath.Join(dirMarkerDir, RootMarkerFileName)
	if err := os.Mkdir(dirMarkerPath, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if HasRootMarker(dirMarkerDir) {
		t.Errorf("expected HasRootMarker to be false when marker is a directory")
	}
	if err := EnsureRootMarker(dirMarkerDir); err == nil {
		t.Errorf("expected EnsureRootMarker to return error when marker is a directory")
	}

	// Subtest: Root marker as symlink
	symlinkMarkerDir := t.TempDir()
	externalTarget := filepath.Join(symlinkMarkerDir, "ext_target.txt")
	_ = os.WriteFile(externalTarget, []byte(RootMarkerExpectedContent), 0644)
	symlinkPath := filepath.Join(symlinkMarkerDir, RootMarkerFileName)
	if err := os.Symlink(externalTarget, symlinkPath); err == nil {
		if HasRootMarker(symlinkMarkerDir) {
			t.Errorf("expected HasRootMarker to reject symlinks")
		}
		if err := EnsureRootMarker(symlinkMarkerDir); err == nil {
			t.Errorf("expected EnsureRootMarker to return error when marker is a symlink")
		}
	}
}

func TestIsAcceleratorTempFile(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"assets/12345/img/sp/btn.png.tmp.1234.5678.1", true},
		{"assets/12345/img/sp/btn.png.ext.tmp.1234.5678.1", true},
		{"hosts/example.com/assets/app.js.tmp.999.1.2", true},
		// User files with .tmp. in name or non-cache directories
		{"draft.tmp.txt", false},
		{"notes.tmp.pdf", false},
		{"personal_data/report.tmp.docx", false},
		{"personal_data/photo.png.tmp.123.456", false},     // 2 numeric fields in user dir
		{"personal_data/photo.png.tmp.123.456.789", false}, // 3 numeric fields in user dir
		{"user_workspace/app.js.tmp.1.2.3", false},         // user dir
		{"assets/12345/img/sp/btn.png.tmp.123.456", false}, // only 2 numeric fields (must be 3)
		{"assets/12345/img/sp/btn.png.tmp.1.2.3.4", false}, // 4 numeric fields (must be 3)
		{"assets/12345/img/sp/btn.png.tmp.bad_suffix", false},
		{"temp.tmp.123.456.1", false},                    // root file
		{"assets/12345/img/sp/btn.csv.tmp.1.2.3", false}, // .csv is excluded
	}

	for _, tc := range cases {
		got := isAcceleratorTempFile(tc.rel)
		if got != tc.want {
			t.Errorf("isAcceleratorTempFile(%q) = %v; want %v", tc.rel, got, tc.want)
		}
	}
}

func TestIsConfirmedCachePathStructure_Tightened(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		// Legitimate cache paths
		{"assets/12345/img/sp/btn.png", true},
		{"assets/img/sp/btn.png", true},
		{"assets/12345/js/app.js", true},
		{"assets/12345/sound/bgm.mp3", true},
		{"assets/app/sample.png", true},
		{"assets/app/js/set-error-handler.js", true},
		{"assets/app.js", true},
		{"assets_en/app.js", true},
		{"assets_jp/app.js", true},
		{"img/sp/btn.png", true},
		{"sound/bgm.mp3", true},
		{"sound/voice/3040001_v.mp3", true},
		{"sound/se/se_100.mp3", true},
		{"manifest/app.js", true},
		{"hosts/example.com/assets/app.js", true},
		{"hosts/example.com/assets/12345/img/sp/btn.png", true},
		{"hosts/example.com/img/sp/btn.png", true},

		// User files in non-cache paths or invalid categories - MUST be rejected
		{"assets/personal_data/photo.png", false},
		{"assets/12345/personal_data/photo.png", false},
		{"assets_en/personal_data/photo.png", false},
		{"assets_jp/personal_data/photo.png", false},
		{"personal_data/photo.png", false},
		{"user_workspace/doc.png", false},
		{"assets/12345/btn.png", false}, // inside version dir without category
		{"12345/img/sp/btn.png", false}, // bare version number at root
		{"12345/btn.png", false},
		{"hosts/example.com/assets/personal_data/photo.png", false},
		{"hosts/example.com/personal_data/photo.png", false},
		{"hosts/example.com/12345/img/sp/btn.png", false},

		// Category internal subpath tightening (Issue 3 specific test scenarios):
		{"assets/img/personal_data/photo.png", false},
		{"assets/12345/img/personal_data/photo.png", false},
		{"img/personal_data/photo.png", false},
		{"hosts/example.com/assets/12345/img/personal_data/photo.png", false},
		{"assets/sound/personal_data/audio.mp3", false},
		{"assets/12345/sound/personal_data/audio.mp3", false},
		{"sound/personal_data/audio.mp3", false},
		{"assets/img/sp/personal_data/photo.png", false},

		// Recursive deep subpath tightening (Round 4 user test paths):
		{"assets/12345/img/sp/quest/personal_data/photo.png", false},
		{"assets/img/ui/private/photo.png", false},
		{"assets/12345/sound/voice/personal_data/audio.mp3", false},
		{"assets/js/model/user_backup/custom.js", false},
		{"assets/css/common/personal_data/theme.css", false},

		// Event user paths - MUST be rejected
		{"assets/img/sp/event/personal_data/photo.png", false},
		{"assets/img/sp/event/private/photo.png", false},
		{"assets/img/sp/event/user_backup/photo.png", false},
		{"assets/12345/img/sp/event/personal_data/photo.png", false},
		{"img/sp/event/personal_data/photo.png", false},

		// Valid deep legitimate GBF paths:
		{"assets/12345/img/sp/quest/scene/character/body/3040001000.png", true},
		{"assets/img/sp/quest/scene/character/body/3040001000.png", true},
		{"assets/img/sp/event/common/parts/btn_ok.png", true},
		{"assets/12345/js/model/manifest/npc_3040001000.js", true},
		{"assets/img/ui/icon/weapon/w_1040001.png", true},
		{"assets/css/common/index.css", true},

		// Legitimate GBF event paths:
		{"assets/img/sp/event/raid01/top.png", true},
		{"assets/img/sp/event/teamraid075/top.png", true},
		{"assets/img/sp/event/ev01/banner.png", true},
		{"assets/img/sp/event/common_campaign_2026/banner.png", true},
		{"assets/12345/img/sp/event/raid01/top.png", true},
		{"assets/12345/img/sp/event/teamraid075/top.png", true},
		{"img/sp/event/raid01/top.png", true},
		{"img/sp/event/teamraid075/top.png", true},
		{"img/sp/event/ev01/banner.png", true},
		{"img/sp/event/common/parts/btn_ok.png", true},
	}

	for _, tc := range cases {
		_, _, got := isConfirmedCachePathStructure(tc.rel)
		if got != tc.want {
			t.Errorf("isConfirmedCachePathStructure(%q) = %v; want %v", tc.rel, got, tc.want)
		}
	}
}

func TestIsConfirmedCacheDir(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"assets", true},
		{"assets/12345", true},
		{"assets/12345/img", true},
		{"assets/12345/img/sp", true},
		{"assets/img", true},
		{"assets/img/sp", true},
		{"img", true},
		{"img/sp", true},
		{"sound", true},
		{"sound/bgm", true},
		{"sound/voice", true},
		{"hosts", true},
		{"hosts/example.com", true},
		{"hosts/example.com/assets/12345/img/sp", true},
		{"assets/12345/img/sp/quest/scene/character/body", true},
		{"assets/img/sp/event/common/parts", true},
		{"assets/12345/js/model/manifest", true},
		{"assets/img/ui/icon/weapon", true},
		{"assets/css/common", true},
		{"assets/img/sp/event/raid01", true},
		{"assets/img/sp/event/teamraid075", true},
		{"assets/img/sp/event/ev01", true},
		{"assets/img/sp/event/common_campaign_2026", true},
		{"img/sp/event/raid01", true},

		// User directories - MUST return false
		{"personal_data", false},
		{"user_workspace", false},
		{"assets/personal_data", false},
		{"assets/12345/personal_data", false},
		{"assets/img/personal_data", false},
		{"assets/12345/img/personal_data", false},
		{"img/personal_data", false},
		{"hosts/example.com/personal_data", false},
		{"hosts/example.com/assets/12345/img/personal_data", false},
		{"sound/personal_data", false},
		{"assets/12345/img/sp/quest/personal_data", false},
		{"assets/img/ui/private", false},
		{"assets/12345/sound/voice/personal_data", false},
		{"assets/js/model/user_backup", false},
		{"assets/css/common/personal_data", false},
		{"assets/img/sp/event/personal_data", false},
		{"assets/img/sp/event/private", false},
		{"assets/img/sp/event/user_backup", false},
		{"img/sp/event/personal_data", false},
	}

	for _, tc := range cases {
		got := isConfirmedCacheDir(tc.dir)
		if got != tc.want {
			t.Errorf("isConfirmedCacheDir(%q) = %v; want %v", tc.dir, got, tc.want)
		}
	}
}

func TestIsConfirmedCacheFile_StrictExtAndPath(t *testing.T) {
	tmpDir := t.TempDir()
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

	// 1. User file on non-cache path with valid paired .ext JSON: MUST BE REJECTED!
	userDir := filepath.Join(tmpDir, "assets", "personal_data")
	_ = os.MkdirAll(userDir, 0755)
	userPhoto := filepath.Join(userDir, "photo.png")
	_ = os.WriteFile(userPhoto, validPng, 0644)
	fi, _ := os.Stat(userPhoto)
	userExt := userPhoto + ".ext"
	_ = os.WriteFile(userExt, []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano())), 0644)

	if IsConfirmedCacheFile(tmpDir, userPhoto, true) {
		t.Errorf("IsConfirmedCacheFile must reject user file on non-cache path even with valid .ext")
	}

	// 2. Legitimate cache path with corrupted / empty .ext: MUST NOT fall through to Tier 2!
	cacheDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	_ = os.MkdirAll(cacheDir, 0755)
	cacheFile := filepath.Join(cacheDir, "broken_ext.png")
	_ = os.WriteFile(cacheFile, validPng, 0644)
	brokenExt := cacheFile + ".ext"
	_ = os.WriteFile(brokenExt, []byte("not valid json at all"), 0644)

	// Even with root marker, because broken .ext exists, it must NOT fall through to Tier 2
	if IsConfirmedCacheFile(tmpDir, cacheFile, true) {
		t.Errorf("IsConfirmedCacheFile must NOT fall through to Tier 2 when companion .ext is corrupted")
	}

	// 3. Legitimate cache path with 0-byte .ext: MUST NOT fall through to Tier 2!
	emptyExtFile := filepath.Join(cacheDir, "empty_ext.png")
	_ = os.WriteFile(emptyExtFile, validPng, 0644)
	emptyExt := emptyExtFile + ".ext"
	_ = os.WriteFile(emptyExt, []byte{}, 0644)

	if IsConfirmedCacheFile(tmpDir, emptyExtFile, true) {
		t.Errorf("IsConfirmedCacheFile must NOT fall through to Tier 2 when companion .ext is 0-byte empty")
	}
}

func TestIsConfirmedCacheFile_RejectsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	extDir := t.TempDir()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")
	externalPng := filepath.Join(extDir, "secret.png")
	if err := os.WriteFile(externalPng, validPng, 0644); err != nil {
		t.Fatalf("failed to write external png: %v", err)
	}

	// Create symlink pointing outside baseDir to externalPng
	cacheSubDir := filepath.Join(tmpDir, "assets", "12345", "img", "sp")
	if err := os.MkdirAll(cacheSubDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	symlinkPath := filepath.Join(cacheSubDir, "btn.png")
	if err := os.Symlink(externalPng, symlinkPath); err != nil {
		t.Skipf("skipping symlink test due to OS permission: %v", err)
	}

	_ = EnsureRootMarker(tmpDir)

	// IsConfirmedCacheFile MUST reject symlinks even if pointing to a valid PNG
	if IsConfirmedCacheFile(tmpDir, symlinkPath, true) {
		t.Errorf("IsConfirmedCacheFile must reject symbolic link pointing outside cache root")
	}
}

func TestAutoWriteRootMarkerLifecycle(t *testing.T) {
	// Case 1: Empty dir -> allowed
	emptyDir := t.TempDir()
	can, err := CanAutoWriteRootMarker(emptyDir, false, false)
	if err != nil || !can {
		t.Fatalf("expected clean dir to allow auto root marker, got can=%v, err=%v", can, err)
	}
	wrote, err := TryAutoWriteRootMarker(emptyDir, false, false)
	if err != nil || !wrote || !HasRootMarker(emptyDir) {
		t.Fatalf("expected TryAutoWriteRootMarker to succeed on empty dir")
	}

	// Case 2: Dir with user files -> forbidden unless isDefaultDir or allFilesConfirmed
	userDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(userDir, "user_doc.txt"), []byte("hello"), 0644)
	can, _ = CanAutoWriteRootMarker(userDir, false, false)
	if can {
		t.Errorf("expected dir with user files NOT to allow auto root marker")
	}
	wrote, _ = TryAutoWriteRootMarker(userDir, false, false)
	if wrote || HasRootMarker(userDir) {
		t.Errorf("expected TryAutoWriteRootMarker to fail on dir with user files")
	}

	// If isDefaultDir is true -> allowed
	can, _ = CanAutoWriteRootMarker(userDir, true, false)
	if !can {
		t.Errorf("expected isDefaultDir=true to allow auto root marker")
	}

	// If allFilesConfirmed is true -> allowed
	can, _ = CanAutoWriteRootMarker(userDir, false, true)
	if !can {
		t.Errorf("expected allFilesConfirmed=true to allow auto root marker")
	}
}

func TestTier1VersionFallbackNegativeAndPositiveValidation(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRootMarker(root); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}

	writeDiskFile := func(relPath string, data []byte) string {
		full := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(full, data, 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		return full
	}

	writeExt := func(full string, meta diskMeta) {
		if err := writeDiskExtFile(full, meta); err != nil {
			t.Fatalf("writeDiskExtFile failed: %v", err)
		}
	}

	pngMagic := []byte("\x89PNG\r\n\x1a\nphotos")
	jsCode := []byte("console.log('test');")

	// 1. Negative: 2026/photos/photo.png MUST NOT be recognized under any tier
	p2026 := writeDiskFile("2026/photos/photo.png", pngMagic)
	fi2026, _ := os.Stat(p2026)
	if IsConfirmedCacheFile(root, p2026, true) {
		t.Fatalf("2026/photos/photo.png must NOT be recognized without .ext")
	}
	writeExt(p2026, diskMeta{
		ContentType: "image/png",
		Version:     1,
		Verified:    true,
		Size:        int64(len(pngMagic)),
		MTimeNano:   fi2026.ModTime().UnixNano(),
		Namespace:   "gbf",
	})
	if IsConfirmedCacheFile(root, p2026, true) {
		t.Fatalf("2026/photos/photo.png must NOT be recognized even with valid .ext")
	}
	if isTier1VersionFallbackPath("2026/photos/photo.png") {
		t.Fatalf("2026/photos/photo.png must return false for isTier1VersionFallbackPath")
	}
	if isConfirmedCacheDir("2026/photos") {
		t.Fatalf("2026/photos must return false for isConfirmedCacheDir")
	}

	// 2. Negative: 300/js/app.js without .ext MUST NOT be recognized (Tier-2 rejected)
	p300 := writeDiskFile("300/js/app.js", jsCode)
	fi300, _ := os.Stat(p300)
	if IsConfirmedCacheFile(root, p300, true) {
		t.Fatalf("300/js/app.js without .ext must NOT be recognized through Tier-2")
	}
	cacheRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cacheRoot.Close()
	if isUnverifiedLegacyCacheCandidateInRoot(cacheRoot, "300/js/app.js", fi300, true) {
		t.Fatalf("300/js/app.js without .ext must not be treated as unverified legacy candidate")
	}

	// 3. Negative: 300/js/app.js with mismatched .ext MUST NOT be recognized
	// 3a. Namespace mismatch
	writeExt(p300, diskMeta{
		ContentType: "application/javascript",
		Version:     1,
		Verified:    true,
		Size:        int64(len(jsCode)),
		MTimeNano:   fi300.ModTime().UnixNano(),
		Namespace:   "unrelated.host.com",
	})
	if IsConfirmedCacheFile(root, p300, true) {
		t.Fatalf("300/js/app.js with namespace mismatch must NOT be recognized")
	}

	// 3b. Size mismatch
	writeExt(p300, diskMeta{
		ContentType: "application/javascript",
		Version:     1,
		Verified:    true,
		Size:        int64(len(jsCode)) + 999,
		MTimeNano:   fi300.ModTime().UnixNano(),
		Namespace:   "gbf",
	})
	if IsConfirmedCacheFile(root, p300, true) {
		t.Fatalf("300/js/app.js with size mismatch must NOT be recognized")
	}

	// 3c. MTime mismatch
	writeExt(p300, diskMeta{
		ContentType: "application/javascript",
		Version:     1,
		Verified:    true,
		Size:        int64(len(jsCode)),
		MTimeNano:   fi300.ModTime().UnixNano() + 1000000,
		Namespace:   "gbf",
	})
	if IsConfirmedCacheFile(root, p300, true) {
		t.Fatalf("300/js/app.js with mtime mismatch must NOT be recognized")
	}

	// 3d. Empty Namespace: 300/js/app.js with empty namespace MUST NOT be recognized under Tier-1
	writeExt(p300, diskMeta{
		ContentType: "application/javascript",
		Version:     1,
		Verified:    true,
		Size:        int64(len(jsCode)),
		MTimeNano:   fi300.ModTime().UnixNano(),
		Namespace:   "",
	})
	if IsConfirmedCacheFile(root, p300, true) {
		t.Fatalf("300/js/app.js with empty namespace must NOT be recognized")
	}
	if isConfirmedCacheFileInRoot(cacheRoot, "300/js/app.js", fi300, true) {
		t.Fatalf("300/js/app.js with empty namespace must NOT be recognized by isConfirmedCacheFileInRoot")
	}

	// 4. Positive: 300/js/app.js with matching .ext MUST be recognized under Tier-1
	writeExt(p300, diskMeta{
		ContentType: "application/javascript",
		Version:     1,
		Verified:    true,
		Size:        int64(len(jsCode)),
		MTimeNano:   fi300.ModTime().UnixNano(),
		Namespace:   "gbf",
	})
	if !IsConfirmedCacheFile(root, p300, true) {
		t.Fatalf("300/js/app.js with valid matching .ext MUST be recognized under Tier-1")
	}
	if !isConfirmedCacheFileInRoot(cacheRoot, "300/js/app.js", fi300, true) {
		t.Fatalf("300/js/app.js with valid matching .ext MUST be recognized by isConfirmedCacheFileInRoot")
	}

	// 5. Positive: 300/img/sp/quest/scene/ch1.png with matching .ext MUST be recognized
	pQuest := writeDiskFile("300/img/sp/quest/scene/ch1.png", pngMagic)
	fiQuest, _ := os.Stat(pQuest)
	writeExt(pQuest, diskMeta{
		ContentType: "image/png",
		Version:     1,
		Verified:    true,
		Size:        int64(len(pngMagic)),
		MTimeNano:   fiQuest.ModTime().UnixNano(),
		Namespace:   "gbf",
	})
	if !IsConfirmedCacheFile(root, pQuest, true) {
		t.Fatalf("300/img/sp/quest/scene/ch1.png with valid matching .ext MUST be recognized")
	}
}

// TestTier1NamespaceConfirmationMatrix 明确覆盖 8 种 Namespace 场景的 Tier-1 认证判定：
// 1. 普通 GBF 路径 + 有效 GBF Namespace -> 识别
// 2. 普通 GBF 路径 + 空 Namespace -> 识别 (兼容旧版历史元数据)
// 3. 版本回退路径 + 有效 GBF Namespace -> 识别
// 4. 版本回退路径 + 空 Namespace -> 拒绝
// 5. hosts 路径 + GBF Namespace -> 识别 (Form A 入口)
// 6. hosts 路径 + hostname Namespace -> 识别 (Form B 入口)
// 7. hosts 路径 + 空 Namespace -> 拒绝
// 8. hosts 路径 + 不匹配的 hostname Namespace -> 拒绝
func TestTier1NamespaceConfirmationMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatal(err)
	}
	cacheRoot, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cacheRoot.Close()

	payload := []byte("console.log('ns_matrix');")

	tests := []struct {
		name      string
		relPath   string
		metaNS    string
		wantTier1 bool
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fullPath := filepath.Join(tmpDir, filepath.FromSlash(tc.relPath))
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fullPath, payload, 0644); err != nil {
				t.Fatal(err)
			}
			fi, err := os.Stat(fullPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeDiskExtFile(fullPath, diskMeta{
				ContentType: "application/javascript",
				Version:     1,
				Verified:    true,
				Size:        fi.Size(),
				MTimeNano:   fi.ModTime().UnixNano(),
				Namespace:   tc.metaNS,
			}); err != nil {
				t.Fatal(err)
			}

			gotWithInfo := IsConfirmedCacheFileWithInfo(tmpDir, fullPath, fi, true)
			if gotWithInfo != tc.wantTier1 {
				t.Errorf("IsConfirmedCacheFileWithInfo(%s) = %v, want %v", tc.relPath, gotWithInfo, tc.wantTier1)
			}
			gotInRoot := isConfirmedCacheFileInRoot(cacheRoot, tc.relPath, fi, true)
			if gotInRoot != tc.wantTier1 {
				t.Errorf("isConfirmedCacheFileInRoot(%s) = %v, want %v", tc.relPath, gotInRoot, tc.wantTier1)
			}
		})
	}
}
