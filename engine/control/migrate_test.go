package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestStandardCacheDir_Helpers(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("skipping test: UserHomeDir not available")
	}

	if runtime.GOOS == "darwin" {
		stdDir := filepath.Join(home, "Library", "Application Support", "GBF-Accelerator", "cache")
		if !isStandardCacheDir(stdDir) {
			t.Errorf("expected isStandardCacheDir(%q) to be true", stdDir)
		}
		if isStandardCacheDir(filepath.Join(home, "Downloads", "some-cache")) {
			t.Errorf("expected isStandardCacheDir(Downloads) to be false")
		}
		if getStandardCacheDir() != stdDir {
			t.Errorf("getStandardCacheDir() = %q; expected %q", getStandardCacheDir(), stdDir)
		}
	} else {
		// Non-darwin platforms should always return true for isStandardCacheDir
		if !isStandardCacheDir(filepath.Join("some", "random", "dir")) {
			t.Errorf("expected isStandardCacheDir to return true on non-darwin")
		}
		if getStandardCacheDir() != "" {
			t.Errorf("expected getStandardCacheDir to return empty string on non-darwin")
		}
	}
}

func TestMigrateStandardCache_Handler(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	sourceCacheDir := filepath.Join(tempDir, "custom_cache")
	if err := os.MkdirAll(sourceCacheDir, 0755); err != nil {
		t.Fatalf("failed to create sourceCacheDir: %v", err)
	}

	// Create dummy cache file
	dummyFile := filepath.Join(sourceCacheDir, "test_asset.png")
	if err := os.WriteFile(dummyFile, []byte("dummy-data"), 0644); err != nil {
		t.Fatalf("failed to write dummy cache file: %v", err)
	}

	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.CacheDir = sourceCacheDir
	})

	cacheMgr := cache.NewManager(sourceCacheDir, 16)
	stats := telemetry.GlobalStats

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	req := httptest.NewRequest(http.MethodPost, "/api/cache/migrate-standard", nil)
	req.Host = "127.0.0.1:8125"
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()

	ctrl.handleRoute(w, req)
	resp := w.Result()
	defer resp.Body.Close()

	if runtime.GOOS != "darwin" {
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected HTTP 400 on non-darwin, got %d", resp.StatusCode)
		}
		return
	}

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		t.Fatalf("expected HTTP 200, got %d (body: %v)", resp.StatusCode, errResp)
	}

	var res map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res["ok"] != true {
		t.Fatalf("expected ok=true, got %v", res["ok"])
	}

	newDir, _ := res["new_dir"].(string)
	if newDir == "" {
		t.Fatalf("expected non-empty new_dir")
	}

	// Verify status endpoint reflects standard dir
	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusReq.Host = "127.0.0.1:8125"
	statusReq.RemoteAddr = "127.0.0.1:54321"
	statusW := httptest.NewRecorder()
	ctrl.handleRoute(statusW, statusReq)

	var statusResp map[string]interface{}
	if err := json.NewDecoder(statusW.Body).Decode(&statusResp); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
	cacheObj, ok := statusResp["cache"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing cache object in status")
	}
	if cacheObj["is_standard_dir"] != true {
		t.Errorf("expected is_standard_dir=true after migration, got %v", cacheObj["is_standard_dir"])
	}
}
