package cache

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPersistGenerationRejectsStaleWriteAfterCacheSwitch(t *testing.T) {
    oldBase := t.TempDir()
    newBase := t.TempDir()
    mgr := NewManager(oldBase, 16)
    defer mgr.Close()

    oldGen := mgr.generation
    oldPath := filepath.Join(oldBase, "assets", "stale.png")
    data := []byte("\x89PNG\r\n\x1a\nregression")
    headers := map[string]string{"content-type": "image/png"}

    mgr.SetCacheBase(newBase)
    if mgr.saveToDisk(oldPath, headers, data, oldGen) {
        t.Fatal("stale persistence task must be rejected after cache base changes")
    }
    if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
        t.Fatalf("stale cache file was recreated after cache switch: %v", err)
    }
}

func TestRAMCacheAndAutoRepairRuntimeToggles(t *testing.T) {
    base := t.TempDir()
    mgr := NewManager(base, 16)
    defer mgr.Close()

    data := []byte("\x89PNG\r\n\x1a\nram-toggle")
    if _, ok := mgr.SaveRAMWithNamespace("gbf", "assets/toggle.png", map[string]string{"content-type": "image/png"}, data); !ok {
        t.Fatal("failed to seed RAM cache")
    }
    if !mgr.ramCache.Contains("assets/toggle.png") {
        t.Fatal("expected asset to be present in RAM cache")
    }

    mgr.SetRAMEnabled(false)
    if mgr.ramCache.Contains("assets/toggle.png") {
        t.Fatal("disabling RAM cache must clear existing RAM entries")
    }

    jsPath := filepath.Join(base, "js", "set-error-handler.js")
    if err := os.MkdirAll(filepath.Dir(jsPath), 0755); err != nil {
        t.Fatal(err)
    }
    js := []byte("window.onerror=function(t,a){void 0}; console.log('error handler');")
    if err := os.WriteFile(jsPath, js, 0644); err != nil {
        t.Fatal(err)
    }

    mgr.SetAutoRepair(false)
    item, src := mgr.Get("js/set-error-handler.js")
    if item == nil || src != "DISK" {
        t.Fatalf("auto-repair disabled should allow disk read, got item=%v src=%s", item, src)
    }
    if _, err := os.Stat(jsPath); err != nil {
        t.Fatalf("auto-repair disabled must not quarantine file: %v", err)
    }
}

func TestAutoRepairDisabledPreservesInvalidCacheFile(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base, 16)
	defer m.Close()
	m.SetAutoRepair(false)

	path := filepath.Join(base, "assets", "123", "broken.png")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a png"), 0644); err != nil {
		t.Fatal(err)
	}

	if item, _ := m.Get("/assets/123/broken.png"); item != nil {
		t.Fatal("invalid cache content must not be served")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("invalid cache file was removed while auto repair disabled: %v", err)
	}
}

func TestAutoRepairEnabled_LegitimateErrorHandlerLoadedSuccessfully(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(base, 16)
	defer mgr.Close()

	mgr.SetAutoRepair(true)

	jsDir := filepath.Join(base, "js")
	if err := os.MkdirAll(jsDir, 0755); err != nil {
		t.Fatal(err)
	}
	jsPath := filepath.Join(jsDir, "set-error-handler.js")
	origContent := []byte("window.onerror=function(t,a){ t && alert(t); a && window.location.reload(); };")
	if err := os.WriteFile(jsPath, origContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsPath+".ext", []byte(`{"ContentType":"application/javascript","v":1}`), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Direct loadAndValidateDiskItem must succeed with valid open handle
	item, err := mgr.loadAndValidateDiskItem("gbf", "js/set-error-handler.js", jsPath)
	if err != nil {
		t.Fatalf("loadAndValidateDiskItem failed on legitimate set-error-handler.js: %v", err)
	}
	if item == nil || !bytes.Equal(item.Data, origContent) {
		t.Fatalf("loadAndValidateDiskItem returned invalid item: %v", item)
	}

	// 2. High-level Get() must succeed and return DISK source
	getItem, src := mgr.Get("js/set-error-handler.js")
	if getItem == nil || (src != "DISK" && src != "RAM") {
		t.Fatalf("Get() failed on legitimate set-error-handler.js: item=%v src=%s", getItem, src)
	}
	if !bytes.Equal(getItem.Data, origContent) {
		t.Fatalf("Get() returned wrong content: got %s, want %s", string(getItem.Data), string(origContent))
	}

	// 3. Confirm file was NOT quarantined
	if _, err := os.Stat(jsPath); err != nil {
		t.Fatalf("legitimate file was unexpectedly removed or quarantined: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(jsDir, "set-error-handler.js.quarantine.*"))
	if len(matches) != 0 {
		t.Fatalf("found unexpected quarantine files: %v", matches)
	}
}

func TestAutoRepairEnabled_TamperedErrorHandlerQuarantinedViaGet(t *testing.T) {
	base := t.TempDir()
	mgr := NewManager(base, 16)
	defer mgr.Close()

	mgr.SetAutoRepair(true)

	jsDir := filepath.Join(base, "js")
	if err := os.MkdirAll(jsDir, 0755); err != nil {
		t.Fatal(err)
	}
	jsPath := filepath.Join(jsDir, "set-error-handler.js")
	tamperedContent := []byte("window.onerror=function(t,a){void 0}; console.log('error handler');")
	if err := os.WriteFile(jsPath, tamperedContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsPath+".ext", []byte(`{"ContentType":"application/javascript","v":1}`), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Direct loadAndValidateDiskItem must detect tampered script and quarantine it
	item, err := mgr.loadAndValidateDiskItem("gbf", "js/set-error-handler.js", jsPath)
	if err == nil {
		t.Fatal("expected error on tampered set-error-handler.js, got nil")
	}
	if item != nil {
		t.Fatalf("expected nil item on tampered set-error-handler.js, got %v", item)
	}

	// 2. High-level Get() must return nil because file is quarantined
	getItem, src := mgr.Get("js/set-error-handler.js")
	if getItem != nil {
		t.Fatalf("expected Get() to return nil for quarantined file, got %v (src=%s)", getItem, src)
	}

	// 3. Confirm file was quarantined
	if _, err := os.Stat(jsPath); !os.IsNotExist(err) {
		t.Fatal("original tampered file should have been moved away")
	}
	matches, _ := filepath.Glob(filepath.Join(jsDir, "set-error-handler.js.quarantine.*"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 quarantine file, found %d", len(matches))
	}
}

