package patcher

import (
	"archive/zip"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAXML_OversizedManifest(t *testing.T) {
	// Exceeds MaxManifestSize
	fakeData := make([]byte, MaxManifestSize+1)
	_, err := ParseManifest(fakeData)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size limit") {
		t.Fatalf("expected size limit error, got: %v", err)
	}
}

func TestParseAXML_TruncatedHeader(t *testing.T) {
	shortData := []byte{0x03, 0x00, 0x08, 0x00}
	_, err := ParseManifest(shortData)
	if err == nil || !strings.Contains(err.Error(), "data too short") {
		t.Fatalf("expected data too short error, got: %v", err)
	}
}

func TestParseAXML_ExorbitantStringCount(t *testing.T) {
	// Header: RES_XML_TYPE (0x00080003), size = 64
	buf := make([]byte, 64)
	binary.LittleEndian.PutUint32(buf[0:], 0x00080003) // type RES_XML_TYPE
	binary.LittleEndian.PutUint32(buf[4:], 64)         // chunkSize

	// Next chunk: RES_STRING_POOL_TYPE (0x001C0001)
	binary.LittleEndian.PutUint32(buf[8:], 0x001C0001) // type RES_STRING_POOL_TYPE
	binary.LittleEndian.PutUint32(buf[12:], 40)        // chunkSize
	binary.LittleEndian.PutUint32(buf[16:], 200000)    // stringCount > MaxStringPoolCount (100000)

	_, err := ParseManifest(buf)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum limit") {
		t.Fatalf("expected string count exceeds limit error, got: %v", err)
	}
}

func TestSanitizeVersionName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.0.0", "1.0.0"},
		{"v2.4.0-release", "v2.4.0-release"},
		{"../../evil/path", "evilpath"},
		{"1.0 / test", "1.0__test"},
		{"unknown", "1.0"},
		{"   ", "1.0"},
		{"..", "1.0"},
		{"---", "1.0"},
	}

	for _, tc := range tests {
		got := sanitizeVersionName(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeVersionName(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestSanitizeSplitName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"config.arm64_v8a", "config_arm64_v8a"},
		{"split_main", "split_main"},
		{"../../evil", "evil"},
		{"foo/bar\\baz", "foobarbaz"},
		{"", ""},
		{"...", ""},
	}

	for _, tc := range tests {
		got := sanitizeSplitName(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeSplitName(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestIsPathContained(t *testing.T) {
	base := filepath.Join("var", "tmp", "app")

	// Valid children
	if !isPathContained(base, filepath.Join(base, "child.apk")) {
		t.Errorf("child.apk must be contained")
	}
	if !isPathContained(base, filepath.Join(base, "sub", "child.apk")) {
		t.Errorf("sub/child.apk must be contained")
	}
	absBase, _ := filepath.Abs(base)
	absChild, _ := filepath.Abs(filepath.Join(base, "child.apk"))
	if !isPathContained(base, absChild) {
		t.Errorf("absolute child must be contained in relative base")
	}
	if !isPathContained(absBase, filepath.Join(base, "child.apk")) {
		t.Errorf("relative child must be contained in absolute base")
	}
	if !isPathContained(absBase, absChild) {
		t.Errorf("absolute child must be contained in absolute base")
	}

	// Escape attempts
	if isPathContained(base, filepath.Join(base, "..", "escape.apk")) {
		t.Errorf("parent escape must not be contained")
	}
	if isPathContained(base, filepath.Join(base, "..", "..", "root.apk")) {
		t.Errorf("root escape must not be contained")
	}
	if isPathContained(base, base) {
		t.Errorf("base path itself must not be considered strictly contained inside itself")
	}
}

func TestParseAXML_TruncatedStringPool(t *testing.T) {
	// Case 1: StringPool chunk smaller than 28-byte header
	buf1 := make([]byte, 24)
	binary.LittleEndian.PutUint32(buf1[0:], 0x00080003) // type RES_XML_TYPE
	binary.LittleEndian.PutUint32(buf1[4:], 24)         // chunkSize
	binary.LittleEndian.PutUint32(buf1[8:], 0x001C0001) // type RES_STRING_POOL_TYPE
	binary.LittleEndian.PutUint32(buf1[12:], 16)        // chunkSize < 28
	_, err := ParseManifest(buf1)
	if err == nil || !strings.Contains(err.Error(), "invalid StringPool header length") {
		t.Fatalf("expected header length error, got: %v", err)
	}

	// Case 2: StringPool claims 50 strings, but chunk data is only 40 bytes (need 28 + 50*4 = 228)
	buf2 := make([]byte, 48)
	binary.LittleEndian.PutUint32(buf2[0:], 0x00080003)
	binary.LittleEndian.PutUint32(buf2[4:], 48)
	binary.LittleEndian.PutUint32(buf2[8:], 0x001C0001)
	binary.LittleEndian.PutUint32(buf2[12:], 40)
	binary.LittleEndian.PutUint32(buf2[16:], 50) // stringCount = 50
	_, err = ParseManifest(buf2)
	if err == nil || !strings.Contains(err.Error(), "exceeds available data length") {
		t.Fatalf("expected exceeds available data length error, got: %v", err)
	}

	// Case 3: stringsStart overlaps offset table (stringsStart < 28 + stringCount*4)
	buf3 := make([]byte, 64)
	binary.LittleEndian.PutUint32(buf3[0:], 0x00080003)
	binary.LittleEndian.PutUint32(buf3[4:], 64)
	binary.LittleEndian.PutUint32(buf3[8:], 0x001C0001)
	binary.LittleEndian.PutUint32(buf3[12:], 56)
	binary.LittleEndian.PutUint32(buf3[16:], 2)  // stringCount = 2 (offsets occupy 28..36)
	binary.LittleEndian.PutUint32(buf3[28:], 28) // stringsStart = 28 (overlaps offset table)
	_, err = ParseManifest(buf3)
	if err == nil || !strings.Contains(err.Error(), "overlaps strings start offset") {
		t.Fatalf("expected overlaps error, got: %v", err)
	}

	// Case 4: stringsStart exceeds chunk data
	buf4 := make([]byte, 64)
	binary.LittleEndian.PutUint32(buf4[0:], 0x00080003)
	binary.LittleEndian.PutUint32(buf4[4:], 64)
	binary.LittleEndian.PutUint32(buf4[8:], 0x001C0001)
	binary.LittleEndian.PutUint32(buf4[12:], 56)
	binary.LittleEndian.PutUint32(buf4[16:], 1)   // stringCount = 1
	binary.LittleEndian.PutUint32(buf4[28:], 500) // stringsStart = 500 > len(data)
	_, err = ParseManifest(buf4)
	if err == nil || !strings.Contains(err.Error(), "exceeds data length") {
		t.Fatalf("expected exceeds data length error, got: %v", err)
	}
}

func TestLegitimatePackageAndVersionNames(t *testing.T) {
	legitPackages := []string{
		"jp.mbga.a12016007.lite",
		"com.dena.skyleap",
		"com.cygames.granbluefantasy",
		"org.chromium.chrome",
		"com.example.app_123",
	}
	for _, pkg := range legitPackages {
		if !IsValidPackageName(pkg) {
			t.Errorf("legitimate package name %q was incorrectly rejected", pkg)
		}
	}

	maliciousPackages := []string{
		"../../evil",
		"../app",
		"com/dena/skyleap",
		"com\\dena\\skyleap",
		"com.dena.skyleap;rm -rf",
		"C:\\Windows\\System32",
		"com..dena",
		"",
	}
	for _, pkg := range maliciousPackages {
		if IsValidPackageName(pkg) {
			t.Errorf("malicious package name %q should be rejected", pkg)
		}
	}

	legitVersions := []struct {
		input    string
		expected string
	}{
		{"1.60.0", "1.60.0"},
		{"v2.4.0", "v2.4.0"},
		{"1.0.0-beta.1", "1.0.0-beta.1"},
		{"3.2.1_hotfix", "3.2.1_hotfix"},
		{"2024.10.01", "2024.10.01"},
	}
	for _, tc := range legitVersions {
		got := sanitizeVersionName(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeVersionName(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestOversizedManifestZipEntry(t *testing.T) {
	tempDir := t.TempDir()
	apkPath := filepath.Join(tempDir, "oversized.apk")

	f, err := os.Create(apkPath)
	if err != nil {
		t.Fatalf("failed to create test zip: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatalf("failed to create zip entry: %v", err)
	}

	// Write 11 MB of zeros into the entry
	chunk := make([]byte, 1024*1024)
	for i := 0; i < 11; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("failed to write oversized entry chunk: %v", err)
		}
	}
	_ = zw.Close()
	_ = f.Close()

	// 1. parseApkManifestInfo must fail cleanly
	_, err = parseApkManifestInfo(apkPath)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size limit") {
		t.Fatalf("parseApkManifestInfo: expected size limit error, got: %v", err)
	}

	// 2. ValidateModuleApk must fail cleanly
	err = ValidateModuleApk(apkPath)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size limit") {
		t.Fatalf("ValidateModuleApk: expected size limit error, got: %v", err)
	}
}
