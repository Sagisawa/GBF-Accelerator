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
