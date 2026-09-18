package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_cache_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)

	// 1. Path traversal security checks
	traversals := []string{
		"../escape.png",
		"assets/../../secret.txt",
		"C:\\Windows\\win.ini",
		"D:\\data\\file.png",
		"\\\\server\\share\\data.png",
	}
	for _, p := range traversals {
		if _, ok := mgr.resolvePath(p); ok {
			t.Errorf("expected path %q to be rejected, but resolvePath returned true", p)
		}
	}

	// 2. Save valid PNG asset
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	saved := mgr.Save("assets/test/hero.png", map[string]string{
		"content-type": "image/png",
		"etag":         "\"test-etag-1\"",
	}, validPng)
	if !saved {
		t.Fatal("failed to save valid asset")
	}

	// Verify disk files exist: <file> and <file>.ext
	diskFile := filepath.Join(tempDir, "assets", "test", "hero.png")
	extFile := diskFile + ".ext"
	if fi, err := os.Stat(diskFile); err != nil || fi.Size() == 0 {
		t.Fatalf("asset disk file %s not found or empty", diskFile)
	}
	if fi, err := os.Stat(extFile); err != nil || fi.Size() == 0 {
		t.Fatalf("asset .ext file %s not found or empty", extFile)
	}

	// 3. Retrieve from RAM
	item, src := mgr.Get("assets/test/hero.png")
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM hit, got item=%v, src=%s", item, src)
	}

	// 4. Clear RAM and retrieve from Disk
	mgr.ClearRAM()
	itemDisk, srcDisk := mgr.Get("assets/test/hero.png")
	if itemDisk == nil || srcDisk != "DISK" {
		t.Fatalf("expected DISK hit, got item=%v, src=%s", itemDisk, srcDisk)
	}

	// 5. Plaintext with deceptive Content-Encoding: gzip in metadata must be stripped
	plainText := []byte("console.log('plaintext');")
	mgr.Save("assets/test/app.js", map[string]string{
		"content-type":     "application/javascript",
		"content-encoding": "gzip", // deceptive!
	}, plainText)

	// Manually corrupt .ext to have ce: gzip to simulate legacy ACGP or upstream bug
	appExtPath := filepath.Join(tempDir, "assets", "test", "app.js.ext")
	corruptMeta := map[string]interface{}{
		"ct": "application/javascript",
		"ce": "gzip",
	}
	corruptBytes, _ := json.Marshal(corruptMeta)
	_ = os.WriteFile(appExtPath, corruptBytes, 0644)

	mgr.ClearRAM()
	itemJs, _ := mgr.Get("assets/test/app.js")
	if itemJs == nil {
		t.Fatal("expected app.js to be retrieved")
	}
	if itemJs.ContentEncoding == "gzip" {
		t.Error("plain text without 0x1f 0x8b magic bytes must NEVER have Content-Encoding: gzip (causes ERR_CONTENT_DECODING_FAILED)")
	}

	// 6. Genuine gzip data retains Content-Encoding: gzip
	genuineGzip := gzipBytes([]byte("console.log('compressed');"))
	mgr.Save("assets/test/compressed.js", map[string]string{
		"content-type":     "application/javascript",
		"content-encoding": "gzip",
	}, genuineGzip)

	mgr.ClearRAM()
	itemGz, _ := mgr.Get("assets/test/compressed.js")
	if itemGz == nil {
		t.Fatal("expected compressed.js to be retrieved")
	}
	if itemGz.ContentEncoding != "gzip" {
		t.Errorf("genuine gzip data must retain Content-Encoding: gzip, got %q", itemGz.ContentEncoding)
	}

	// 7. Audit and repair cleans corrupted files
	corruptFile := filepath.Join(tempDir, "assets", "corrupt.png")
	_ = os.WriteFile(corruptFile, []byte("garbage_not_png"), 0644)
	_ = os.WriteFile(corruptFile+".ext", []byte("{}"), 0644)

	auditRes := mgr.AuditAndRepair()
	if auditRes["corrupted"].(int) < 1 {
		t.Errorf("expected at least 1 corrupted file cleaned, got %v", auditRes)
	}
	if _, err := os.Stat(corruptFile); !os.IsNotExist(err) {
		t.Error("corrupted file should have been deleted by audit")
	}
}

func TestLegacyTamperedJSQuarantine(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_quarantine_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)

	// 1. Verify quarantine triggers on tampered file
	tamperedFile := filepath.Join(tempDir, "set-error-handler.js")
	tamperedContent := []byte("window.onerror=function(t,a){void 0}; console.log('error handler');")
	if err := os.WriteFile(tamperedFile, tamperedContent, 0644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(tamperedFile+".ext", []byte("{}"), 0644)

	isQuarantined := mgr.CheckAndQuarantineTamperedJS(tamperedFile)
	if !isQuarantined {
		t.Fatal("Must quarantine tampered set-error-handler.js")
	}

	matches, _ := filepath.Glob(filepath.Join(tempDir, "set-error-handler.js.quarantine.*"))
	if len(matches) != 1 {
		t.Fatalf("Must find exactly one .quarantine backup file, found %d", len(matches))
	}
	if _, err := os.Stat(tamperedFile); !os.IsNotExist(err) {
		t.Fatal("Original tampered file must be renamed out of place")
	}

	// 2. Verify negative case: legitimate modern minified JS using `void 0` is NOT quarantined
	legitFile := filepath.Join(tempDir, "legit-set-error-handler.js")
	legitContent := []byte("if(foo === void 0) { console.error('not set'); } window.onerror=function(e){ alert(e); window.location.reload(); }")
	_ = os.WriteFile(legitFile, legitContent, 0644)

	if mgr.CheckAndQuarantineTamperedJS(legitFile) {
		t.Fatal("Must NOT quarantine legitimate JS using void 0")
	}

	// 3. Verify negative case: original official script with alert/reload is NOT quarantined
	origContent := []byte("window.onerror=function(t,a){ t && alert(t); a && window.location.reload(); };")
	origFile := filepath.Join(tempDir, "official-set-error-handler.js")
	_ = os.WriteFile(origFile, origContent, 0644)

	if mgr.CheckAndQuarantineTamperedJS(origFile) {
		t.Fatal("Must NOT quarantine original official script")
	}
}

func TestHasCacheAndAutoRepair(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_hascache_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)

	// HasCache returns false for non-existent
	if mgr.HasCache("assets/test/missing.png") {
		t.Error("expected false for missing asset")
	}

	// Save valid asset
	mgr.Save("assets/test/sample.png", map[string]string{
		"content-type": "image/png",
	}, []byte("\x89PNG\r\n\x1a\nheader"))

	if !mgr.HasCache("assets/test/sample.png") {
		t.Error("expected true for cached asset")
	}

	// Corrupt content on disk is deleted on Get()
	corruptDisk := filepath.Join(tempDir, "assets", "test", "bad.png")
	_ = os.MkdirAll(filepath.Dir(corruptDisk), 0755)
	_ = os.WriteFile(corruptDisk, []byte("not_png_bytes"), 0644)
	_ = os.WriteFile(corruptDisk+".ext", []byte("{}"), 0644)

	mgr.ClearRAM()
	item, _ := mgr.Get("assets/test/bad.png")
	if item != nil {
		t.Error("corrupt content should return nil")
	}
	if _, err := os.Stat(corruptDisk); !os.IsNotExist(err) {
		t.Error("corrupt file should be unlinked on Get auto-repair")
	}
}
