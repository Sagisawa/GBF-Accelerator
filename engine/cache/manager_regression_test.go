package cache

import (
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
