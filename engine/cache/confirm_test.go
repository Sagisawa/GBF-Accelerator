package cache

import (
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
	emptyFile := filepath.Join(tmpDir, "assets", "img", "empty.png")
	_ = os.MkdirAll(filepath.Dir(emptyFile), 0755)
	_ = os.WriteFile(emptyFile, []byte{}, 0644)
	if IsConfirmedCacheFile(tmpDir, emptyFile, true) {
		t.Errorf("empty file (size 0) must NOT be confirmed")
	}

	// 4. System junk files
	junkFiles := []string{".DS_Store", "Thumbs.db", "desktop.ini", "._asset.png"}
	for _, name := range junkFiles {
		p := filepath.Join(tmpDir, "assets", "img", name)
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
}

func TestIsAcceleratorTempFile(t *testing.T) {
	cases := []struct {
		rel  string
		want bool
	}{
		{"assets/12345/img/sp/btn.png.tmp.1234.5678.1", true},
		{"assets/12345/img/sp/btn.png.ext.tmp.1234.5678.1", true},
		{"hosts/example.com/assets/app.js.tmp.999.1.2", true},
		// User files with .tmp. in name
		{"draft.tmp.txt", false},
		{"notes.tmp.pdf", false},
		{"personal_data/report.tmp.docx", false},
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
