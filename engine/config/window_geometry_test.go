package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowGeometryDefaultsAndRoundTrip(t *testing.T) {
	defaults := DefaultConfig()
	if defaults.WindowWidth != 880 || defaults.WindowHeight != 640 || defaults.WindowMaximized {
		t.Fatalf("unexpected window defaults: %+v", defaults)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen_port":9000}`), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	mgr := NewManager(path)
	loaded := mgr.Get()
	if loaded.WindowWidth != 880 || loaded.WindowHeight != 640 || loaded.WindowMaximized {
		t.Fatalf("legacy config did not retain window defaults: %+v", loaded)
	}

	loaded.WindowWidth = 1200
	loaded.WindowHeight = 800
	loaded.WindowMaximized = true
	if err := mgr.Commit(loaded); err != nil {
		t.Fatalf("failed to commit window geometry: %v", err)
	}

	reloaded := NewManager(path).Get()
	if reloaded.WindowWidth != 1200 || reloaded.WindowHeight != 800 || !reloaded.WindowMaximized {
		t.Fatalf("window geometry did not round-trip: %+v", reloaded)
	}
}
