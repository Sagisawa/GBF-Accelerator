package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerifiedNonGzipFastPath_RespectsSizeMTimeAndSetErrorHandler(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base, 16)
	defer m.Close()

	// 1. Save valid PNG (non-gzip) -> verified=true on disk and in diskMeta
	pngPath := "/assets/img/sp/verified_fast.png"
	pngBytes := makeFakePNG(2048)
	if !m.Save(pngPath, map[string]string{"content-type": "image/png", "etag": `"v1"`}, pngBytes) {
		t.Fatal("failed to save png")
	}

	m.ClearRAM()
	item, src := m.GetWithNamespace("gbf", pngPath)
	if item == nil || src != "DISK" || len(item.Data) != len(pngBytes) {
		t.Fatalf("expected verified non-gzip DISK hit, got item=%v src=%q", item, src)
	}

	// 2. Corrupt file on disk (size/mtime change) -> must fall back to IsValidCacheContent and reject
	m.ClearRAM()
	diskPath, _ := m.resolvePathWithNamespace("gbf", "assets/img/sp/verified_fast.png")
	if err := os.WriteFile(diskPath, []byte("<html>502 Bad Gateway</html>"), 0644); err != nil {
		t.Fatalf("overwrite corrupt file: %v", err)
	}
	if badItem, _ := m.GetWithNamespace("gbf", pngPath); badItem != nil {
		t.Fatalf("expected corrupted file to be rejected even after prior verification")
	}

	// 3. Tampered set-error-handler.js must still be quarantined even when .ext says verified=true
	tamperedJS := []byte(`window.onerror = function(e) { void 0; };`)
	jsClean := "assets/1795000000/js/set-error-handler.js"
	jsDiskPath, _ := m.resolvePathWithNamespace("gbf", jsClean)
	if err := os.MkdirAll(filepath.Dir(jsDiskPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(jsDiskPath, tamperedJS, 0644); err != nil {
		t.Fatalf("write tampered js: %v", err)
	}
	fi, err := os.Stat(jsDiskPath)
	if err != nil {
		t.Fatalf("stat tampered js: %v", err)
	}
	extBytes, _ := json.Marshal(diskMeta{
		Namespace:   "gbf",
		ContentType: "application/javascript",
		ETag:        `"t1"`,
		Version:     1,
		Verified:    true,
		Size:        fi.Size(),
		MTimeNano:   fi.ModTime().UnixNano(),
	})
	if err := os.WriteFile(jsDiskPath+".ext", extBytes, 0644); err != nil {
		t.Fatalf("write ext: %v", err)
	}
	if itemJS, _ := m.GetWithNamespace("gbf", "/"+jsClean); itemJS != nil {
		t.Fatalf("expected tampered set-error-handler.js to be quarantined despite verified .ext")
	}
}

func TestMetaIndex_BackfillFromLegacyDiskCache(t *testing.T) {
	base := t.TempDir()

	// Seed legacy disk files + verified .ext directly without .gbf_meta_index.v1
	files := []struct {
		rel  string
		ct   string
		etag string
		data []byte
	}{
		{"assets/1795000000/js/core.js", "application/javascript", `"js-1"`, []byte("console.log('ok');")},
		{"assets/img/sp/hero.png", "image/png", `"png-1"`, makeFakePNG(512)},
		{"assets/css/main.css", "text/css", `"css-1"`, []byte("body{margin:0}")},
	}

	for _, f := range files {
		full := filepath.Join(base, filepath.FromSlash(f.rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, f.data, 0644); err != nil {
			t.Fatalf("write asset: %v", err)
		}
		fi, err := os.Stat(full)
		if err != nil {
			t.Fatalf("stat asset: %v", err)
		}
		metaBytes, _ := json.Marshal(diskMeta{
			Namespace:   "gbf",
			ContentType: f.ct,
			ETag:        f.etag,
			AccessTime:  time.Now().Unix(),
			Version:     1,
			Verified:    true,
			Size:        fi.Size(),
			MTimeNano:   fi.ModTime().UnixNano(),
		})
		if err := os.WriteFile(full+".ext", metaBytes, 0644); err != nil {
			t.Fatalf("write ext: %v", err)
		}
	}

	m1 := NewManager(base, 16)
	m1.waitMetaIndexLoad(0)
	if m1.metaIndexLoaded.Load() {
		t.Fatal("expected metaIndexLoaded=false before backfill when no snapshot exists")
	}

	mockFG := &mockFGWaiter{isIdle: false}
	added := m1.BackfillMetaIndex(mockFG, nil)
	if added != len(files) {
		t.Fatalf("expected %d backfilled entries, got %d", len(files), added)
	}
	if !mockFG.waited {
		t.Fatal("expected BackfillMetaIndex to yield to busy foreground")
	}
	if !m1.metaIndexLoaded.Load() {
		t.Fatal("expected metaIndexLoaded=true after backfill")
	}
	m1.Close()

	// Remove .ext files to verify second Manager resolves metadata purely from persisted meta.idx
	for _, f := range files {
		full := filepath.Join(base, filepath.FromSlash(f.rel))
		_ = os.Remove(full + ".ext")
	}

	m2 := NewManager(base, 16)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)
	if !m2.metaIndexLoaded.Load() {
		t.Fatal("expected persisted meta index to load on restart")
	}
	for _, f := range files {
		item, src := m2.GetMetadataWithNamespace("gbf", "/"+f.rel)
		if item == nil || src != "DISK" || item.ETag != f.etag {
			t.Fatalf("expected metadata hit from backfilled index for %s, got item=%v src=%q", f.rel, item, src)
		}
	}
}

func TestMetaIndex_BackfillCancelsImmediately(t *testing.T) {
	base := t.TempDir()
	full := filepath.Join(base, "assets", "img", "a.png")
	_ = os.MkdirAll(filepath.Dir(full), 0755)
	_ = os.WriteFile(full, makeFakePNG(256), 0644)

	m := NewManager(base, 16)
	defer m.Close()

	cancelCh := make(chan struct{})
	close(cancelCh)
	if added := m.BackfillMetaIndex(nil, cancelCh); added != 0 {
		t.Fatalf("expected 0 entries on immediate cancel, got %d", added)
	}
}

func TestWarmSingleDiskItemToProbation(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base, 16)
	defer m.Close()

	smallPath := "/assets/1795000000/js/cjs/char_01.js"
	largePath := "/assets/1795000000/img/big.png"
	if !m.Save(smallPath, map[string]string{"content-type": "application/javascript"}, []byte("var cjs = 1;")) {
		t.Fatal("save small failed")
	}
	if !m.Save(largePath, map[string]string{"content-type": "image/png"}, makeFakePNG(64*1024)) {
		t.Fatal("save large failed")
	}

	m.ClearRAM()
	if m.IsInRAMWithNamespace("gbf", smallPath) {
		t.Fatal("expected smallPath not in RAM after ClearRAM")
	}

	if !m.WarmSingleDiskItemToProbation("gbf", smallPath, 16*1024) {
		t.Fatal("expected WarmSingleDiskItemToProbation to admit small disk asset")
	}
	if !m.IsInRAMWithNamespace("gbf", smallPath) {
		t.Fatal("expected smallPath in RAM after warm")
	}
	if m.IsRAMProtected("gbf", "assets/1795000000/js/cjs/char_01.js") {
		t.Fatal("expected warmed disk asset to reside in Probation, not Protected")
	}
	// Second warm should be a no-op returning false
	if m.WarmSingleDiskItemToProbation("gbf", smallPath, 16*1024) {
		t.Fatal("expected already-in-RAM asset to return false")
	}
	// Asset exceeding maxBytes must be skipped
	if m.WarmSingleDiskItemToProbation("gbf", largePath, 16*1024) {
		t.Fatal("expected asset exceeding maxBytes to be skipped")
	}
	if m.IsInRAMWithNamespace("gbf", largePath) {
		t.Fatal("expected skipped large asset not in RAM")
	}
}
