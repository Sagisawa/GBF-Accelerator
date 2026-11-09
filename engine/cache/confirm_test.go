package cache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestIsConfirmedCacheFile_RejectionsAndBasics(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	// 1. Tier 1 asset with valid .ext is confirmed with or without root marker
	relPath := "assets/12345/img/sp/test.png"
	if !mgr.SaveWithNamespace("gbf", relPath, testPNGHeaders, testValidPNG) {
		t.Fatal("failed to save asset")
	}
	fullPath, ok := mgr.resolvePathWithNamespace("gbf", relPath)
	if !ok || !IsConfirmedCacheFile(tmpDir, fullPath, false) || !IsConfirmedCacheFile(tmpDir, fullPath, true) {
		t.Errorf("Tier 1 asset with valid .ext must be confirmed with or without root marker")
	}

	// 2. Comprehensive negative matrix: legacy without .ext, user files, junk, corrupted .ext, empty .ext
	for _, tc := range []struct {
		rel string
		ext []byte
	}{
		{rel: "assets/img/sp/legacy.png"}, {rel: "assets/img/sp/doc.docx"}, {rel: "doc.txt"}, {rel: "notes.pdf"}, {rel: "data.csv"}, {rel: "image.png"},
		{rel: "user_folder/document.docx"}, {rel: "user_folder/data.csv"}, {rel: "user_folder/sheet.xlsx"}, {rel: "user_folder/archive.zip"}, {rel: "user_folder/exec.exe"},
		{rel: "assets/img/sp/empty.png"}, {rel: "assets/img/sp/.DS_Store"}, {rel: "assets/img/sp/Thumbs.db"}, {rel: "assets/img/sp/desktop.ini"}, {rel: "assets/img/sp/._asset.png"}, {rel: RootMarkerFileName},
		{"personal_data/photo.png", []byte(`{"v":1,"size":100,"mtime":123,"at":123,"ct":"image/png","verified":true,"ns":"gbf"}`)}, {"assets/12345/img/sp/broken_ext.png", []byte("not valid json")}, {"assets/12345/img/sp/empty_ext.png", []byte{}},
	} {
		data := testValidPNG
		if tc.rel == "assets/img/sp/empty.png" {
			data = nil
		}
		p := writeTestAsset(t, tmpDir, tc.rel, data, tc.ext)
		if IsConfirmedCacheFile(tmpDir, p, true) || IsConfirmedCacheFile(tmpDir, p, false) {
			t.Errorf("file %q must NOT be confirmed", tc.rel)
		}
	}

	// 3. Symlink pointing outside cache root rejected
	extDir := t.TempDir()
	extPng := writeTestDiskFile(t, extDir, "secret.png", testValidPNG)
	symlinkPath := filepath.Join(tmpDir, "assets", "12345", "img", "sp", "sym.png")
	if err := os.Symlink(extPng, symlinkPath); err != nil {
		t.Skipf("skipping symlink test due to OS permission: %v", err)
	} else if IsConfirmedCacheFile(tmpDir, symlinkPath, true) {
		t.Errorf("symlink pointing outside cache root must be rejected")
	}
}

func TestRootMarkerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	if HasRootMarker(tmpDir) || EnsureRootMarker(tmpDir) != nil || !HasRootMarker(tmpDir) || EnsureRootMarker(tmpDir) != nil {
		t.Fatal("EnsureRootMarker lifecycle failed")
	}
	_ = os.WriteFile(filepath.Join(tmpDir, RootMarkerFileName), bytes.Repeat([]byte("A"), 257), 0644)
	if HasRootMarker(tmpDir) {
		t.Error("oversized marker must return false")
	}
	for _, kind := range []string{"corrupt", "dir", "symlink"} {
		d := t.TempDir()
		writeTestInvalidMarker(t, d, kind)
		if HasRootMarker(d) || EnsureRootMarker(d) == nil {
			t.Errorf("invalid marker %q must return false and fail EnsureRootMarker", kind)
		}
	}
}

func TestIsAcceleratorTempFile(t *testing.T) {
	for _, tc := range []struct {
		rel  string
		want bool
	}{
		{"assets/12345/img/sp/btn.png.tmp.1234.5678.1", true}, {"assets/12345/img/sp/btn.png.ext.tmp.1234.5678.1", true},
		{"hosts/example.com/assets/app.js.tmp.999.1.2", true}, {"draft.tmp.txt", false}, {"notes.tmp.pdf", false},
		{"personal_data/report.tmp.docx", false}, {"personal_data/photo.png.tmp.123.456", false},
		{"personal_data/photo.png.tmp.123.456.789", false}, {"user_workspace/app.js.tmp.1.2.3", false},
		{"assets/12345/img/sp/btn.png.tmp.123.456", false}, {"assets/12345/img/sp/btn.png.tmp.1.2.3.4", false},
		{"assets/12345/img/sp/btn.png.tmp.bad_suffix", false}, {"temp.tmp.123.456.1", false}, {"assets/12345/img/sp/btn.csv.tmp.1.2.3", false},
	} {
		if got := IsAcceleratorTempFile(tc.rel); got != tc.want {
			t.Errorf("IsAcceleratorTempFile(%q) = %v; want %v", tc.rel, got, tc.want)
		}
	}
}

func TestIsConfirmedCacheDir(t *testing.T) {
	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{"assets", true}, {"assets/12345", true}, {"assets/12345/img", true}, {"assets/12345/img/sp", true}, {"assets/img", true}, {"assets/common", true},
		{"assets_en", true}, {"assets_jp", true}, {"img", true}, {"img/sp", true}, {"sound", true}, {"hosts", true}, {"hosts/example.com", true},
		{"hosts/example.com/assets", true}, {"300", true}, {"300/js", true}, {"personal_data", false}, {"user_workspace", false}, {"my_backup", false}, {"2026/photos", false},
	} {
		if got := IsConfirmedCacheDirectory(tc.dir); got != tc.want {
			t.Errorf("IsConfirmedCacheDirectory(%q) = %v; want %v", tc.dir, got, tc.want)
		}
	}
}

func TestTier1NamespaceConfirmationMatrix(t *testing.T) {
	tmpDir := t.TempDir()
	_ = EnsureRootMarker(tmpDir)
	cacheRoot, err := os.OpenRoot(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cacheRoot.Close()

	payload := []byte("console.log('ns_matrix');")
	tests := []struct {
		name, relPath, metaNS string
		verified, wantTier1   bool
	}{
		{"1_CanonicalGBF_ValidNS", "assets/300/js/canonical_valid.js", "gbf", true, true}, {"2_CanonicalGBF_EmptyNS", "assets/300/js/canonical_empty.js", "", true, true}, {"3_Fallback_ValidNS", "300/js/fallback_valid.js", "gbf", true, true},
		{"4_Fallback_EmptyNS", "300/js/fallback_empty.js", "", true, false}, {"5_Hosts_GBF_NS", "hosts/sub.example.com/assets/js/host_gbf.js", "gbf", true, true}, {"6_Hosts_Hostname_NS", "hosts/sub.example.com/assets/js/host_direct.js", "sub.example.com", true, true},
		{"7_Hosts_EmptyNS_Untrusted", "hosts/untrusted-cdn.com/assets/js/host_empty.js", "", true, false}, {"8_Hosts_MismatchNS", "hosts/sub.example.com/assets/js/host_mismatch.js", "other.example.com", true, false},
		{"9_Hosts_Official_EmptyNS_Verified", "hosts/game.granbluefantasy.jp/assets/app.js", "", true, true}, {"10_Hosts_Official_EmptyNS_Unverified", "hosts/game.granbluefantasy.jp/assets/bad.js", "", false, false}, {"11_Hosts_Official_MismatchNS", "hosts/game.granbluefantasy.jp/assets/other.js", "other_game", true, false},
	}

	for _, tc := range tests {
		extJSON := fmt.Sprintf(`{"v":1,"ct":"application/javascript","verified":%v,"ns":%q}`, tc.verified, tc.metaNS)
		fullPath := writeTestAsset(t, tmpDir, tc.relPath, payload, []byte(extJSON))
		fi, _ := os.Stat(fullPath)
		if got := IsConfirmedCacheFileWithInfo(tmpDir, fullPath, fi, true); got != tc.wantTier1 {
			t.Errorf("[%s] IsConfirmedCacheFileWithInfo(%s) = %v, want %v", tc.name, tc.relPath, got, tc.wantTier1)
		}
		if got := isConfirmedCacheFileInRoot(cacheRoot, tc.relPath, fi, true); got != tc.wantTier1 {
			t.Errorf("[%s] isConfirmedCacheFileInRoot(%s) = %v, want %v", tc.name, tc.relPath, got, tc.wantTier1)
		}
	}
}

func TestTier1VersionFallbackAndCategories(t *testing.T) {
	root := t.TempDir()
	_ = EnsureRootMarker(root)
	pngMagic, jsCode, icoData := []byte("\x89PNG\r\n\x1a\nphotos"), []byte("console.log('test');"), []byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10}
	gbfJS := []byte(`{"v":1,"ns":"gbf","ct":"application/javascript","verified":true}`)
	gbfPNG := []byte(`{"v":1,"ct":"image/png","verified":true,"ns":"gbf"}`)
	gbfICO := []byte(`{"v":1,"ns":"gbf","ct":"image/x-icon"}`)

	for _, tc := range []struct {
		name, rel string
		data, ext []byte
		want      bool
	}{
		{"fallback_bad_cat", "2026/photos/photo.png", jsCode, gbfPNG, false}, {"fallback_personal", "300/personal/app.js", jsCode, nil, false}, {"fallback_docx", "300/js/app.docx", jsCode, nil, false},
		{"fallback_bare_no_ext", "300/js/app.js", jsCode, nil, false}, {"fallback_valid_quest", "300/img/sp/quest/scene/ch1.png", pngMagic, gbfPNG, true}, {"jssdk_valid", "jssdk/mobage-jssdk-client.3.10.1.min.js", jsCode, gbfJS, true},
		{"jssdk_no_ext", "jssdk/mobage-jssdk-xdframe.2.3.min.js", jsCode, nil, false}, {"jssdk_docx", "jssdk/notes.docx", []byte("doc"), nil, false}, {"jssdk_backup", "jssdk_backup/app.js", jsCode, nil, false},
		{"favicon_bare", "favicon.ico", icoData, nil, false}, {"favicon_valid_gbf", "favicon.ico", icoData, gbfICO, true}, {"favicon_legacy_empty", "favicon.ico", icoData, []byte(`{"v":1,"ct":"image/x-icon"}`), true},
		{"favicon_mismatch_ns", "favicon.ico", icoData, []byte(`{"v":1,"ns":"other_game","ct":"image/x-icon"}`), false}, {"favicon_bad_prefix", "my_favicon.ico", icoData, gbfICO, false}, {"favicon_bad_bak", "favicon.ico.bak", icoData, gbfICO, false},
		{"favicon_png_ext", "favicon.png", icoData, gbfICO, false}, {"root_notes_txt", "notes.txt", icoData, gbfICO, false}, {"root_image_png", "root_image.png", icoData, gbfICO, false},
		{"perm_mismatched_ns", "300/js/perm_ns.js", jsCode, []byte(`{"v":1,"ns":"unrelated.com","ct":"application/javascript","verified":true}`), false},
		{"perm_size_mismatch", "300/js/perm_sz.js", jsCode, []byte(`{"v":1,"ns":"gbf","ct":"application/javascript","verified":true,"size":9999}`), true},
		{"perm_mtime_mismatch", "300/js/perm_mt.js", jsCode, []byte(`{"v":1,"ns":"gbf","ct":"application/javascript","verified":true,"mtime":9999}`), true},
		{"perm_empty_ns", "300/js/perm_empty.js", jsCode, []byte(`{"v":1,"ns":"","ct":"application/javascript","verified":true}`), false},
		{"perm_valid_matching", "300/js/perm_valid.js", jsCode, gbfJS, true},
		{"19_digits_max_int64", "9223372036854775807/js/app.js", jsCode, gbfJS, true},
		{"20_digits_overflow", "12345678901234567890/js/app.js", jsCode, gbfJS, false},
	} {
		p := writeTestAsset(t, root, tc.rel, tc.data, tc.ext)
		if got := IsConfirmedCacheFile(root, p, true); got != tc.want {
			t.Errorf("[%s] IsConfirmedCacheFile(%s) = %v, want %v", tc.name, tc.rel, got, tc.want)
		}
	}
}
