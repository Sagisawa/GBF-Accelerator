package cache

import (
	"encoding/json"
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

	// Save file with .ext metadata
	relPath := "assets/12345/img/sp/test.png"
	if !mgr.SaveWithNamespace("gbf", relPath, headers, testValidPNG) {
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

func TestIsConfirmedCacheFile_WithoutExtRejected(t *testing.T) {
	tmpDir := t.TempDir()

	legacyFull := writeTestDiskFile(t, tmpDir, "assets/img/sp/legacy.png", testValidPNG)

	// Without .ext -> MUST be rejected both with and without root marker
	if IsConfirmedCacheFile(tmpDir, legacyFull, false) {
		t.Errorf("legacy asset without .ext must NOT be confirmed without marker")
	}
	if IsConfirmedCacheFile(tmpDir, legacyFull, true) {
		t.Errorf("legacy asset without .ext must NOT be confirmed even with root marker")
	}

	// Non-asset extension with root marker -> MUST be rejected!
	userDocFull := writeTestDiskFile(t, tmpDir, "assets/img/sp/doc.docx", []byte("user doc"))
	if IsConfirmedCacheFile(tmpDir, userDocFull, true) {
		t.Errorf("file with non-asset extension must NOT be confirmed")
	}
}


func TestIsConfirmedCacheFile_RejectsNonCacheFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Files in cache root (e.g. doc.txt, notes.pdf, or even a .png in root)
	for _, name := range []string{"doc.txt", "notes.pdf", "data.csv", "image.png"} {
		p := writeTestDiskFile(t, tmpDir, name, []byte("test content"))
		if IsConfirmedCacheFile(tmpDir, p, true) {
			t.Errorf("file in cache root %q must NOT be confirmed", name)
		}
	}

	// 2. Nested user files with non-cache extensions (including .csv)
	for _, name := range []string{"document.docx", "data.csv", "sheet.xlsx", "archive.zip", "exec.exe"} {
		p := writeTestDiskFile(t, tmpDir, filepath.Join("user_folder", name), []byte("test content"))
		if IsConfirmedCacheFile(tmpDir, p, true) {
			t.Errorf("user file with non-cache extension %q must NOT be confirmed", name)
		}
	}

	// 3. Empty files (size == 0)
	emptyFile := writeTestDiskFile(t, tmpDir, "assets/img/sp/empty.png", nil)
	if IsConfirmedCacheFile(tmpDir, emptyFile, true) {
		t.Errorf("empty file (size 0) must NOT be confirmed")
	}

	// 4. System junk files
	for _, name := range []string{".DS_Store", "Thumbs.db", "desktop.ini", "._asset.png"} {
		p := writeTestDiskFile(t, tmpDir, filepath.Join("assets/img/sp", name), []byte("junk"))
		if IsConfirmedCacheFile(tmpDir, p, true) {
			t.Errorf("system junk file %q must NOT be confirmed", name)
		}
	}

	// 5. Root marker itself
	markerFile := writeTestDiskFile(t, tmpDir, RootMarkerFileName, []byte("marker"))
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
		{"assets/common", true},
		{"assets_en", true},
		{"assets_jp", true},
		{"img", true},
		{"img/sp", true},
		{"sound", true},
		{"hosts", true},
		{"hosts/example.com", true},
		{"hosts/example.com/assets", true},
		{"300", true},
		{"300/js", true},

		// User directories - MUST return false
		{"personal_data", false},
		{"user_workspace", false},
		{"my_backup", false},
		{"2026/photos", false}, // version dir with invalid category
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

	// 1. User file on non-cache path with valid paired .ext JSON: MUST BE REJECTED!
	userPhoto := writeTestDiskFile(t, tmpDir, "personal_data/photo.png", testValidPNG)
	fi, _ := os.Stat(userPhoto)
	userExt := userPhoto + ".ext"
	_ = os.WriteFile(userExt, []byte(fmt.Sprintf(`{"v":1,"size":%d,"mtime":%d,"at":12345,"ct":"image/png","verified":true,"ns":"gbf"}`, fi.Size(), fi.ModTime().UnixNano())), 0644)

	if IsConfirmedCacheFile(tmpDir, userPhoto, true) {
		t.Errorf("IsConfirmedCacheFile must reject user file on non-cache path even with valid .ext")
	}

	// 2. Legitimate cache path with corrupted / empty .ext: MUST NOT fall through to Tier 2!
	cacheFile := writeTestDiskFile(t, tmpDir, "assets/12345/img/sp/broken_ext.png", testValidPNG)
	brokenExt := cacheFile + ".ext"
	_ = os.WriteFile(brokenExt, []byte("not valid json at all"), 0644)

	// Even with root marker, because broken .ext exists, it must NOT fall through to Tier 2
	if IsConfirmedCacheFile(tmpDir, cacheFile, true) {
		t.Errorf("IsConfirmedCacheFile must NOT fall through to Tier 2 when companion .ext is corrupted")
	}

	// 3. Legitimate cache path with 0-byte .ext: MUST NOT fall through to Tier 2!
	emptyExtFile := writeTestDiskFile(t, tmpDir, "assets/12345/img/sp/empty_ext.png", testValidPNG)
	emptyExt := emptyExtFile + ".ext"
	_ = os.WriteFile(emptyExt, []byte{}, 0644)

	if IsConfirmedCacheFile(tmpDir, emptyExtFile, true) {
		t.Errorf("IsConfirmedCacheFile must NOT fall through to Tier 2 when companion .ext is 0-byte empty")
	}
}

func TestIsConfirmedCacheFile_RejectsSymlinks(t *testing.T) {
	tmpDir := t.TempDir()
	extDir := t.TempDir()

	externalPng := writeTestDiskFile(t, extDir, "secret.png", testValidPNG)

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

func TestTier1VersionFallbackNegativeAndPositiveValidation(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRootMarker(root); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}

	writeExt := func(full string, meta diskMeta) {
		_ = writeDiskExtFile(full, meta)
	}

	pngMagic := []byte("\x89PNG\r\n\x1a\nphotos")
	jsCode := []byte("console.log('test');")

	// 1. Negative: invalid category or extension rejected under all tiers
	for _, invalidRel := range []string{
		"2026/photos/photo.png",
		"300/personal/app.js",
		"300/js/app.docx",
	} {
		p := writeTestDiskFile(t, root, invalidRel, jsCode)
		if IsConfirmedCacheFile(root, p, true) {
			t.Fatalf("%s must return false for version fallback checks", invalidRel)
		}
	}
	if isConfirmedCacheDir("2026/photos") || isConfirmedCacheDir("300/personal") {
		t.Fatalf("invalid categories must not be confirmed directories")
	}
	p2026 := filepath.Join(root, "2026", "photos", "photo.png")
	fi2026, _ := os.Stat(p2026)
	writeExt(p2026, diskMeta{ContentType: "image/png", Version: 1, Verified: true, Size: int64(len(jsCode)), MTimeNano: fi2026.ModTime().UnixNano(), Namespace: "gbf"})
	if IsConfirmedCacheFile(root, p2026, true) {
		t.Fatalf("2026/photos/photo.png must NOT be recognized even with valid .ext")
	}

	// 2. Bare version assets without .ext: strictly unconfirmed
	p300 := writeTestDiskFile(t, root, "300/js/app.js", jsCode)
	pQuestLegacy := writeTestDiskFile(t, root, "300/img/sp/quest/scene/ch1.png", pngMagic)
	for _, p := range []string{p300, pQuestLegacy} {
		if IsConfirmedCacheFile(root, p, true) || IsConfirmedCacheFile(root, p, false) {
			t.Fatalf("%s without .ext must be rejected", p)
		}
	}
	fi300, _ := os.Stat(p300)

	// 3. 300/js/app.js with .ext permutations
	extCases := []struct {
		name string
		meta diskMeta
		want bool
	}{
		{"mismatched NS", diskMeta{ContentType: "application/javascript", Version: 1, Verified: true, Size: int64(len(jsCode)), MTimeNano: fi300.ModTime().UnixNano(), Namespace: "unrelated.com"}, false},
		{"size mismatch", diskMeta{ContentType: "application/javascript", Version: 1, Verified: true, Size: int64(len(jsCode)) + 999, MTimeNano: fi300.ModTime().UnixNano(), Namespace: "gbf"}, true},
		{"mtime mismatch", diskMeta{ContentType: "application/javascript", Version: 1, Verified: true, Size: int64(len(jsCode)), MTimeNano: fi300.ModTime().UnixNano() + 1000000, Namespace: "gbf"}, true},
		{"empty NS", diskMeta{ContentType: "application/javascript", Version: 1, Verified: true, Size: int64(len(jsCode)), MTimeNano: fi300.ModTime().UnixNano(), Namespace: ""}, false},
		{"valid matching", diskMeta{ContentType: "application/javascript", Version: 1, Verified: true, Size: int64(len(jsCode)), MTimeNano: fi300.ModTime().UnixNano(), Namespace: "gbf"}, true},
	}
	for _, tc := range extCases {
		writeExt(p300, tc.meta)
		if got := IsConfirmedCacheFile(root, p300, true); got != tc.want {
			t.Errorf("[%s] IsConfirmedCacheFile = %v, want %v", tc.name, got, tc.want)
		}
	}

	// 4. Positive image in version fallback
	pQuest := writeTestDiskFile(t, root, "300/img/sp/quest/scene/ch1.png", pngMagic)
	fiQuest, _ := os.Stat(pQuest)
	writeExt(pQuest, diskMeta{ContentType: "image/png", Version: 1, Verified: true, Size: int64(len(pngMagic)), MTimeNano: fiQuest.ModTime().UnixNano(), Namespace: "gbf"})
	if !IsConfirmedCacheFile(root, pQuest, true) {
		t.Fatalf("300/img/sp/quest/scene/ch1.png with matching .ext MUST be recognized")
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

// TestHistoricalCompatibility_GBFHostsEmptyNS verifies that only strictly confirmed
// GBF official domains are permitted to match Tier-1 with an empty namespace (meta.Namespace == "")
// and ONLY when meta.Verified == true.
func TestHistoricalCompatibility_GBFHostsEmptyNS(t *testing.T) {
	officialHosts := []string{
		"granbluefantasy.com",
		"game.granbluefantasy.jp",
		"ava-a.sp.mbga.jp",
		"connect.mobage.jp",
		"steam.granbluefantasy.com",
	}

	for _, host := range officialHosts {
		slashRel := fmt.Sprintf("hosts/%s/asset.png", host)
		// 1. Positive: host with empty ns and Verified=true -> Allowed
		metaValid := diskMeta{
			Version:  1,
			Verified: true,
		}
		raw, _ := json.Marshal(metaValid)
		if _, ok := VerifyTier1Meta(slashRel, raw); !ok {
			t.Fatalf("expected host %s with empty ns and Verified=true to be accepted, but got rejected", host)
		}

		// 2. Negative: host with empty ns but Verified=false -> Rejected
		metaUnverified := diskMeta{
			Version:  1,
			Verified: false,
		}
		rawUnverified, _ := json.Marshal(metaUnverified)
		if _, ok := VerifyTier1Meta(slashRel, rawUnverified); ok {
			t.Fatalf("expected host %s with empty ns and Verified=false to be rejected, but got accepted", host)
		}

		// 3. Negative: host with mismatched non-empty ns -> Rejected
		metaMismatch := diskMeta{
			Version:   1,
			Namespace: "other_game",
			Verified:  true,
		}
		rawMismatch, _ := json.Marshal(metaMismatch)
		if _, ok := VerifyTier1Meta(slashRel, rawMismatch); ok {
			t.Fatalf("expected host %s with mismatched ns to be rejected, but got accepted", host)
		}
	}

	// 4. Untrusted hosts with empty ns -> Strictly Rejected even if Verified=true
	for _, host := range []string{"untrusted-cdn.com", "custom.cdn.net"} {
		slashRel := fmt.Sprintf("hosts/%s/asset.png", host)
		meta := diskMeta{
			Version:  1,
			Verified: true,
		}
		raw, _ := json.Marshal(meta)
		if _, ok := VerifyTier1Meta(slashRel, raw); ok {
			t.Fatalf("untrusted host %s with empty ns must be rejected", host)
		}
	}
}

// TestJSSDKCategorySupport verifies positive and negative attribution for the jssdk category.
func TestJSSDKCategorySupport(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	validJS := []byte("console.log('mobage-jssdk');")
	jssdkDir := filepath.Join(root, "jssdk")
	if err := os.MkdirAll(jssdkDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Positive Tier-1: jssdk/mobage-jssdk-client.3.10.1.min.js with paired .ext
	clientPath := filepath.Join(jssdkDir, "mobage-jssdk-client.3.10.1.min.js")
	if err := os.WriteFile(clientPath, validJS, 0644); err != nil {
		t.Fatal(err)
	}
	extData := []byte(`{"v":1,"ns":"gbf","ct":"application/javascript"}`)
	if err := os.WriteFile(clientPath+".ext", extData, 0644); err != nil {
		t.Fatal(err)
	}
	if !IsConfirmedCacheFile(root, clientPath, false) {
		t.Fatalf("jssdk asset with paired .ext must be confirmed under Tier-1")
	}

	// 2. jssdk file without .ext must NOT be confirmed
	xdframePath := filepath.Join(jssdkDir, "mobage-jssdk-xdframe.2.3.min.js")
	if err := os.WriteFile(xdframePath, validJS, 0644); err != nil {
		t.Fatal(err)
	}
	if IsConfirmedCacheFile(root, xdframePath, true) || IsConfirmedCacheFile(root, xdframePath, false) {
		t.Fatalf("jssdk asset without .ext must NOT be confirmed")
	}

	// 3. Negative: non-asset extension in jssdk
	docPath := filepath.Join(jssdkDir, "notes.docx")
	if err := os.WriteFile(docPath, []byte("word doc"), 0644); err != nil {
		t.Fatal(err)
	}
	if IsConfirmedCacheFile(root, docPath, true) {
		t.Fatalf("jssdk/notes.docx must be rejected due to non-asset extension")
	}

	// 4. Negative: lookalike directory (jssdk_backup)
	fakeDir := filepath.Join(root, "jssdk_backup")
	if err := os.MkdirAll(fakeDir, 0755); err != nil {
		t.Fatal(err)
	}
	fakeScript := filepath.Join(fakeDir, "app.js")
	if err := os.WriteFile(fakeScript, validJS, 0644); err != nil {
		t.Fatal(err)
	}
	if IsConfirmedCacheFile(root, fakeScript, true) {
		t.Fatalf("jssdk_backup/app.js must be rejected because jssdk_backup is not a GBF category")
	}
}

// TestRootFaviconSupport verifies that only the exact root favicon.ico with valid paired .ext
// is confirmed, while bare root favicon, other root files, and lookalikes are rejected.
func TestRootFaviconSupport(t *testing.T) {
	root := t.TempDir()
	if err := EnsureRootMarker(root); err != nil {
		t.Fatal(err)
	}

	icoData := []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}
	faviconPath := filepath.Join(root, "favicon.ico")
	if err := os.WriteFile(faviconPath, icoData, 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Without .ext: must NOT be confirmed (root favicon is Tier-1 only, no Tier-2)
	if IsConfirmedCacheFile(root, faviconPath, true) {
		t.Fatalf("root favicon.ico without .ext must NOT be confirmed through Tier-2")
	}

	// 2. With valid paired .ext (ns="gbf"): must be confirmed under Tier-1
	extData := []byte(`{"v":1,"ns":"gbf","ct":"image/x-icon"}`)
	if err := os.WriteFile(faviconPath+".ext", extData, 0644); err != nil {
		t.Fatal(err)
	}
	if !IsConfirmedCacheFile(root, faviconPath, false) {
		t.Fatalf("root favicon.ico with paired .ext must be confirmed under Tier-1")
	}

	// 3. With valid paired .ext (empty ns for legacy backward compat): must be confirmed under Tier-1
	extLegacy := []byte(`{"v":1,"ct":"image/x-icon"}`)
	if err := os.WriteFile(faviconPath+".ext", extLegacy, 0644); err != nil {
		t.Fatal(err)
	}
	if !IsConfirmedCacheFile(root, faviconPath, false) {
		t.Fatalf("root favicon.ico with legacy empty ns .ext must be confirmed under Tier-1")
	}

	// 4. With mismatched namespace (ns="other"): must be rejected
	extBadNS := []byte(`{"v":1,"ns":"other_game","ct":"image/x-icon"}`)
	if err := os.WriteFile(faviconPath+".ext", extBadNS, 0644); err != nil {
		t.Fatal(err)
	}
	if IsConfirmedCacheFile(root, faviconPath, false) {
		t.Fatalf("root favicon.ico with mismatched ns must be rejected")
	}

	// 5. Lookalikes and other root files must be strictly rejected
	lookalikes := []string{
		"my_favicon.ico",
		"favicon.ico.bak",
		"favicon.png",
		"notes.txt",
		"root_image.png",
	}
	for _, name := range lookalikes {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, icoData, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+".ext", extData, 0644); err != nil {
			t.Fatal(err)
		}
		if IsConfirmedCacheFile(root, p, true) {
			t.Fatalf("root file %s must be rejected even with fake .ext and marker", name)
		}
	}
}
