package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigManager(t *testing.T) {
	tempFile, err := os.CreateTemp("", "gbf_config_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp config: %v", err)
	}
	defer os.Remove(tempFile.Name())

	cfgJSON := `{
		"listen_port": 9000,
		"allow_lan": true,
		"upstream_proxy": "http://127.0.0.1:7890",
		"direct_mode": false,
		"ram_cache_max_mb": 512
	}`
	_ = os.WriteFile(tempFile.Name(), []byte(cfgJSON), 0644)

	mgr := NewManager(tempFile.Name())
	cfg := mgr.Get()

	if cfg.ListenPort != 9000 {
		t.Errorf("expected ListenPort 9000, got %d", cfg.ListenPort)
	}
	if !cfg.AllowLAN {
		t.Error("expected AllowLAN to be true")
	}
	if mgr.GetEffectiveListenHost() != "0.0.0.0" {
		t.Errorf("expected 0.0.0.0 for allow_lan=true, got %s", mgr.GetEffectiveListenHost())
	}
	if mgr.GetEffectiveUpstreamProxy() != "http://127.0.0.1:7890" {
		t.Errorf("expected upstream proxy http://127.0.0.1:7890, got %s", mgr.GetEffectiveUpstreamProxy())
	}

	// Test update and callback
	var callbackCalled bool
	mgr.OnUpdate(func(c *Config) {
		callbackCalled = true
	})

	mgr.Update(func(c *Config) {
		c.DirectMode = true
	})

	if !callbackCalled {
		t.Error("expected update callback to be invoked")
	}
	if mgr.GetEffectiveUpstreamProxy() != "" {
		t.Error("expected empty upstream proxy under direct_mode=true")
	}

	// Test NormalizeCacheDir
	norm := NormalizeCacheDir("")
	if norm != filepath.Join("cache", "gbf", "https") {
		t.Errorf("expected default cache dir, got %s", norm)
	}

	tempRoot := t.TempDir()
	acgpPath := filepath.Join(tempRoot, "cache", "gbf", "https", "assets")
	if err := os.MkdirAll(acgpPath, 0755); err != nil {
		t.Fatalf("failed to create dummy acgp structure: %v", err)
	}

	// Given ACGPower root folder
	resRoot := NormalizeCacheDir(tempRoot)
	expectedRoot := filepath.Join(tempRoot, "cache", "gbf", "https")
	if resRoot != expectedRoot {
		t.Errorf("NormalizeCacheDir(%q) = %q; expected %q", tempRoot, resRoot, expectedRoot)
	}

	// Given cache folder directly
	resCache := NormalizeCacheDir(filepath.Join(tempRoot, "cache"))
	if resCache != expectedRoot {
		t.Errorf("NormalizeCacheDir(cache) = %q; expected %q", resCache, expectedRoot)
	}

	// Given assets folder directly
	resAssets := NormalizeCacheDir(acgpPath)
	if resAssets != expectedRoot {
		t.Errorf("NormalizeCacheDir(assets) = %q; expected %q", resAssets, expectedRoot)
	}
}

