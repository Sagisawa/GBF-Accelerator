package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestAsyncCacheSave(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_async_cache_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	item, ok := mgr.SaveRAM("assets/test/async_hero.png", map[string]string{
		"content-type": "image/png",
		"etag":         "\"async-etag-1\"",
	}, validPng)
	if !ok || item == nil {
		t.Fatal("SaveRAM returned failure")
	}

	// Immediate RAM hit
	ramItem, src := mgr.Get("assets/test/async_hero.png")
	if ramItem == nil || src != "RAM" {
		t.Fatalf("expected immediate RAM hit, got %v, src %s", ramItem, src)
	}

	// Wait for background persistWorker to flush to disk
	diskFile := filepath.Join(tempDir, "assets", "test", "async_hero.png")
	extFile := diskFile + ".ext"

	persisted := false
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		if fi, err := os.Stat(diskFile); err == nil && fi.Size() > 0 {
			if fe, err2 := os.Stat(extFile); err2 == nil && fe.Size() > 0 {
				persisted = true
				break
			}
		}
	}
	if !persisted {
		t.Fatal("expected background worker to persist file and .ext to disk within 500ms")
	}

	// Verify disk load after RAM cleared
	mgr.ClearRAM()
	diskItem, srcDisk := mgr.Get("assets/test/async_hero.png")
	if diskItem == nil || srcDisk != "DISK" {
		t.Fatalf("expected DISK hit after clearing RAM, got %v, src %s", diskItem, srcDisk)
	}
}

func TestHostNamespaceIsolation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_ns_cache_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	gbfData := []byte("\x89PNG\r\n\x1a\ngbf_official_data")
	otherData := []byte("\x89PNG\r\n\x1a\nother_host_data")

	// Save to gbf namespace (mirrors a, a1..a5)
	mgr.SaveWithNamespace("gbf", "assets/common/img.png", map[string]string{"content-type": "image/png"}, gbfData)

	// Save to isolated custom host
	savedOther := mgr.SaveWithNamespace("other.example.com", "assets/common/img.png", map[string]string{"content-type": "image/png"}, otherData)
	if !savedOther {
		t.Fatalf("SaveWithNamespace failed for other.example.com")
	}

	// Verify isolation in GetWithNamespace
	itemGBF, _ := mgr.GetWithNamespace("gbf", "assets/common/img.png")
	if itemGBF == nil || string(itemGBF.Data) != string(gbfData) {
		t.Errorf("expected GBF data, got %v", itemGBF)
	}

	itemOther, _ := mgr.GetWithNamespace("other.example.com", "assets/common/img.png")
	if itemOther == nil || string(itemOther.Data) != string(otherData) {
		t.Errorf("expected Other data, got %v", itemOther)
	}

	// Verify disk paths: GBF in base root, other in hosts/other.example.com/
	gbfDisk := filepath.Join(tempDir, "assets", "common", "img.png")
	otherDisk := filepath.Join(tempDir, "hosts", "other.example.com", "assets", "common", "img.png")

	if _, err := os.Stat(gbfDisk); err != nil {
		t.Errorf("GBF file must be in root cache dir: %v", err)
	}
	if _, err := os.Stat(otherDisk); err != nil {
		t.Errorf("Other file must be in hosts/ subdir: %v", err)
	}
}

func TestAsyncDrainOnClose(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_drain_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)

	pngData := []byte("\x89PNG\r\n\x1a\ndrain_test_data")
	mgr.SaveRAMWithNamespace("gbf", "assets/drain.png", map[string]string{"content-type": "image/png"}, pngData)

	// Close immediately: Close() must wait for background persistQueue to drain
	mgr.Close()

	diskFile := filepath.Join(tempDir, "assets", "drain.png")
	data, err := os.ReadFile(diskFile)
	if err != nil {
		t.Fatalf("file must be written to disk before Close() returns: %v", err)
	}
	if string(data) != string(pngData) {
		t.Fatalf("expected %q, got %q", string(pngData), string(data))
	}
}

func TestWarmup(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	pngData := []byte("\x89PNG\r\n\x1a\nwarmup_test")
	// Save 5 files to disk
	for i := 1; i <= 5; i++ {
		path := fmt.Sprintf("assets/item%d.png", i)
		mgr.Save(path, map[string]string{"content-type": "image/png"}, pngData)
	}

	// Also save a file under hosts/
	mgr.SaveWithNamespace("example.com", "assets/host_item.png", map[string]string{"content-type": "image/png"}, pngData)

	// Create a quarantined file on disk: Warmup must skip this!
	quarantinedPath := filepath.Join(tempDir, "assets", "bad.js.quarantine.12345")
	if err := os.WriteFile(quarantinedPath, []byte("tampered content"), 0644); err != nil {
		t.Fatalf("failed to create quarantined file: %v", err)
	}

	// Clear RAM cache so it's empty
	mgr.ClearRAM()
	items, _ := mgr.Stats()
	if items != 0 {
		t.Fatalf("expected 0 items in RAM after ClearRAM, got %d", items)
	}

	// Warmup max 3 items
	loaded := mgr.Warmup(3)
	if loaded != 3 {
		t.Fatalf("expected 3 items loaded during Warmup(3), got %d", loaded)
	}

	items, _ = mgr.Stats()
	if items != 3 {
		t.Fatalf("expected 3 items in RAM cache after Warmup(3), got %d", items)
	}

	// Warmup all items: must load exactly 6 valid items (5 gbf + 1 host) and NOT the quarantined file
	loadedAll := mgr.Warmup(100)
	if loadedAll != 6 {
		t.Fatalf("expected exactly 6 items loaded during Warmup(100), got %d", loadedAll)
	}
}

func TestPruneStaleVersionsHosts(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	pngData := []byte("\x89PNG\r\n\x1a\nprune_test")

	// Create 5 versions in base assets
	for v := 101; v <= 105; v++ {
		p := fmt.Sprintf("assets/%d/img.png", v)
		mgr.Save(p, map[string]string{"content-type": "image/png"}, pngData)
	}

	// Create 5 versions in hosts/prd-game-a.akamaized.net/assets
	for v := 201; v <= 205; v++ {
		p := fmt.Sprintf("assets/%d/img.png", v)
		mgr.SaveWithNamespace("prd-game-a.akamaized.net", p, map[string]string{"content-type": "image/png"}, pngData)
	}

	// Prune keeping 2 versions
	deletedDirs, deletedFiles, _ := mgr.PruneStaleVersions(2)
	// Base should prune 3 versions (101, 102, 103), hosts should prune 3 versions (201, 202, 203)
	if deletedDirs != 6 {
		t.Errorf("expected 6 deleted dirs (3 in base + 3 in hosts), got %d", deletedDirs)
	}
	// Each version contains 1 asset file + 1 .ext metadata file = 2 files per version * 6 versions = 12 files
	if deletedFiles != 12 {
		t.Errorf("expected 12 deleted files (6 asset + 6 .ext files), got %d", deletedFiles)
	}

	// Verify latest versions still exist: 104, 105 in base, 204, 205 in hosts
	if _, err := os.Stat(filepath.Join(tempDir, "assets", "105", "img.png")); err != nil {
		t.Error("expected version 105 to be kept in base")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "hosts", "prd-game-a.akamaized.net", "assets", "205", "img.png")); err != nil {
		t.Error("expected version 205 to be kept in hosts")
	}
	// Verify stale versions deleted
	if _, err := os.Stat(filepath.Join(tempDir, "assets", "101", "img.png")); !os.IsNotExist(err) {
		t.Error("expected version 101 to be deleted from base")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "hosts", "prd-game-a.akamaized.net", "assets", "201", "img.png")); !os.IsNotExist(err) {
		t.Error("expected version 201 to be deleted from hosts")
	}
}

func TestAuditAndSlimProgress(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	// 1. Setup sample files: 1 good png, 1 0-byte file, 1 corrupt file
	goodPng := []byte("\x89PNG\r\n\x1a\nprogress_test")
	mgr.Save("assets/test/good.png", map[string]string{"content-type": "image/png"}, goodPng)
	_ = os.WriteFile(filepath.Join(tempDir, "assets", "test", "zero.png"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "assets", "test", "corrupt.png"), []byte("504 Gateway Timeout"), 0644)

	var auditCallbacks []AuditProgress
	res := mgr.AuditAndRepairWithProgress(func(p AuditProgress) {
		auditCallbacks = append(auditCallbacks, p)
	}, nil)

	if res["corrupted"].(int) != 2 {
		t.Errorf("expected 2 corrupted files detected, got %v", res["corrupted"])
	}
	if len(auditCallbacks) == 0 || !auditCallbacks[len(auditCallbacks)-1].Done {
		t.Errorf("expected final audit callback with Done=true")
	}

	// Test cancellation in audit
	cancelCh := make(chan struct{})
	close(cancelCh) // already cancelled
	resCancel := mgr.AuditAndRepairWithProgress(nil, cancelCh)
	if resCancel == nil {
		t.Fatalf("expected non-nil result on cancelled audit")
	}

	// 2. Test slim progress
	for v := 1; v <= 5; v++ {
		mgr.Save(fmt.Sprintf("assets/%d/test.png", v), map[string]string{"content-type": "image/png"}, goodPng)
	}

	var slimCallbacks []SlimProgress
	delDirs, delFiles, _ := mgr.PruneStaleVersionsWithProgress(2, func(p SlimProgress) {
		slimCallbacks = append(slimCallbacks, p)
	}, nil)

	if delDirs != 3 {
		t.Errorf("expected 3 deleted dirs, got %d", delDirs)
	}
	if delFiles != 6 { // 3 png + 3 .ext
		t.Errorf("expected 6 deleted files, got %d", delFiles)
	}
	if len(slimCallbacks) == 0 || !slimCallbacks[len(slimCallbacks)-1].Done {
		t.Errorf("expected final slim callback with Done=true")
	}
}


