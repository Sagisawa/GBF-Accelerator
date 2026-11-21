package cache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetaIndex_SaveLoadAndTieredWarmup(t *testing.T) {
	base := t.TempDir()
	m1 := NewManager(base, 32)

	jsData := []byte("console.log('core-script');")
	cssData := []byte("body { color: #000; }")
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89sprite")

	if !m1.Save("/assets/1795000000/js/core.js", map[string]string{"content-type": "application/javascript", "etag": `"js1"`}, jsData) {
		t.Fatal("failed to save core.js")
	}
	if !m1.Save("/assets/1795000000/css/app.css", map[string]string{"content-type": "text/css", "etag": `"css1"`}, cssData) {
		t.Fatal("failed to save app.css")
	}
	// Save png images with gbf namespace AFTER js/css so their timestamps are >= js/css,
	// proving Tiered Warmup still prioritizes core .js/.css over newer .png images.
	if !m1.Save("/assets/1795000000/img/bg.png", map[string]string{"content-type": "image/png", "etag": `"png1"`}, pngData) {
		t.Fatal("failed to save bg.png")
	}
	if !m1.Save("/assets/1795000000/img/bg2.png", map[string]string{"content-type": "image/png", "etag": `"png2"`}, pngData) {
		t.Fatal("failed to save bg2.png")
	}

	m1.Close()

	indexPath := filepath.Join(base, MetaIndexFileName)
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected meta index file at %s: %v", indexPath, err)
	}

	// New manager should load meta index asynchronously and serve metadata without reading .ext
	m2 := NewManager(base, 32)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)

	if !m2.metaIndexLoaded.Load() {
		t.Fatal("expected metaIndexLoaded to be true after loading snapshot")
	}

	// Remove .ext files to prove disk metadata hits come from the binary index (while physical data remains intact)
	pJS, _ := m2.ResolvePathWithNamespace("gbf", "/assets/1795000000/js/core.js")
	_ = os.Remove(pJS + ".ext")

	metaItem, src := m2.GetMetadataWithNamespace("gbf", "/assets/1795000000/js/core.js")
	if metaItem == nil || src != "DISK" || metaItem.ETag != `"js1"` {
		t.Fatalf("expected disk metadata hit from meta index without .ext, got item=%v src=%q", metaItem, src)
	}

	// Tiered Warmup(2) should prioritize Tier-3 core .js/.css over Tier-1 bg.png
	m2.ClearRAM()
	loaded := m2.Warmup(2)
	if loaded != 2 {
		t.Fatalf("expected Warmup(2) to load 2 items, got %d", loaded)
	}
	if m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/js/core.js") == nil {
		t.Fatal("expected core.js to be prioritized by Tiered Warmup")
	}
	if m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/css/app.css") == nil {
		t.Fatal("expected app.css to be prioritized over .png items during Warmup(2)")
	}
	if m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/img/bg.png") != nil || m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/img/bg2.png") != nil {
		t.Fatal("expected bg.png / bg2.png (Tier 1) NOT to be loaded before core .js/.css during Warmup(2)")
	}
}

func TestMetaIndex_CorruptFallbackAndClearAllRemoval(t *testing.T) {
	base := t.TempDir()
	indexPath := filepath.Join(base, MetaIndexFileName)
	if err := os.WriteFile(indexPath, []byte("CORRUPT_GARBAGE_DATA_12345"), 0644); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	m := NewManager(base, 16)
	defer m.Close()
	m.waitMetaIndexLoad(0)

	if m.metaIndexLoaded.Load() {
		t.Fatal("expected corrupt meta index to be rejected")
	}

	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89clean")
	if !m.Save("/assets/1795000000/img/a.png", map[string]string{"content-type": "image/png"}, pngData) {
		t.Fatal("save failed")
	}
	if err := m.SaveMetaIndex(); err != nil {
		t.Fatalf("SaveMetaIndex failed: %v", err)
	}
	if _, _, _, err := m.ClearAllWithError(); err != nil {
		t.Fatalf("ClearAllWithError failed: %v", err)
	}
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("expected ClearAllWithError to remove %s", MetaIndexFileName)
	}
}

func TestMetaIndex_OrphanTempCleanedAndConcurrentFlushClose(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base, 16)

	orphanTmp := filepath.Join(base, MetaIndexFileName+".tmp.99999.123456.1")
	if err := os.WriteFile(orphanTmp, []byte("stale-tmp"), 0644); err != nil {
		t.Fatalf("write orphanTmp: %v", err)
	}
	activeTmp := filepath.Join(base, MetaIndexFileName+".tmp.99999.123456.2")
	if err := os.WriteFile(activeTmp, []byte("active-tmp"), 0644); err != nil {
		t.Fatalf("write activeTmp: %v", err)
	}
	m.persistMu.RLock()
	tokOK := m.registerTempFileTokenLocked(activeTmp, opToken{gen: m.generation})
	m.persistMu.RUnlock()
	if !tokOK {
		t.Fatal("failed to register active meta index temp token")
	}

	res := m.AuditAndRepairWithProgress(nil, nil)
	if removed, _ := res["temp_files_removed"].(int); removed != 1 {
		t.Fatalf("expected 1 orphaned meta index temp file removed, got %v (full=%+v)", res["temp_files_removed"], res)
	}
	if _, err := os.Stat(orphanTmp); !os.IsNotExist(err) {
		t.Fatalf("expected orphanTmp %s to be deleted by audit", orphanTmp)
	}
	if _, err := os.Stat(activeTmp); err != nil {
		t.Fatalf("expected activeTmp %s to remain protected while registered: %v", activeTmp, err)
	}
	m.DiscardDiskTempFile(activeTmp)

	// Simulate in-flight periodic flush holding metaIndexSaveMu while a subsequent dirty write
	// arrives, verifying Close() waits for the flush and commits the latest dirty write.
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89close")
	if !m.Save("/assets/1795000000/img/final_item.png", map[string]string{"content-type": "image/png", "etag": `"final1"`}, pngData) {
		t.Fatal("save final_item.png failed")
	}

	m.metaIndexSaveMu.Lock()
	m.metaIndexDirty.Store(true)
	closeDone := make(chan struct{})
	go func() {
		m.Close()
		close(closeDone)
	}()

	// Close() must block until the in-flight periodic save releases metaIndexSaveMu.
	select {
	case <-closeDone:
		m.metaIndexSaveMu.Unlock()
		t.Fatal("Close() returned before in-flight metaIndexSaveMu lock was released")
	default:
	}
	m.metaIndexSaveMu.Unlock()
	<-closeDone

	m2 := NewManager(base, 16)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)
	metaItem, src := m2.GetMetadataWithNamespace("gbf", "/assets/1795000000/img/final_item.png")
	if metaItem == nil || src != "DISK" || metaItem.ETag != `"final1"` {
		t.Fatalf("expected meta index flushed cleanly on Close(), got item=%v src=%q", metaItem, src)
	}
}
