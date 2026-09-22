package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConfigManager(t *testing.T) {
	tempFile, err := os.CreateTemp("", "gbf_config_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp config: %v", err)
	}
	_ = tempFile.Close()
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

func TestConfigManager_CommitConcurrency(t *testing.T) {
	tempFile, err := os.CreateTemp("", "gbf_commit_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	_ = tempFile.Close()
	defer os.Remove(tempFile.Name())

	mgr := NewManager(tempFile.Name())

	const numGoroutines = 20
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			candidate := mgr.Candidate()
			candidate.RAMCacheMaxMB = 100 + idx
			if err := mgr.Commit(candidate); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent Commit error: %v", err)
	}

	// Verify consistent state
	finalCfg := mgr.Get()
	if finalCfg.RAMCacheMaxMB < 100 || finalCfg.RAMCacheMaxMB >= 100+numGoroutines {
		t.Errorf("unexpected final RAMCacheMaxMB: %d", finalCfg.RAMCacheMaxMB)
	}

	// Verify disk file is valid JSON and matches in-memory
	loadedMgr := NewManager(tempFile.Name())
	if loadedMgr.Get().RAMCacheMaxMB != finalCfg.RAMCacheMaxMB {
		t.Errorf("disk config (%d) does not match in-memory (%d)",
			loadedMgr.Get().RAMCacheMaxMB, finalCfg.RAMCacheMaxMB)
	}
}

func TestConfigManager_CommitCallbackNoDeadlock(t *testing.T) {
	tempFile, err := os.CreateTemp("", "gbf_callback_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	_ = tempFile.Close()
	defer os.Remove(tempFile.Name())

	mgr := NewManager(tempFile.Name())

	var reentrantRan atomic.Bool
	done := make(chan struct{})

	mgr.OnUpdate(func(c *Config) {
		if !reentrantRan.Swap(true) {
			// Callback invokes Commit re-entrantly. If commitMu was still held during callback invocation,
			// this would cause an immediate re-entrant deadlock.
			cand := mgr.Candidate()
			cand.ListenPort = 9999
			_ = mgr.Commit(cand)
			close(done)
		}
	})

	cand := mgr.Candidate()
	cand.ListenPort = 8888
	if err := mgr.Commit(cand); err != nil {
		t.Fatalf("initial Commit failed: %v", err)
	}

	select {
	case <-done:
		// Completed without deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock detected: callback could not acquire commit lock")
	}

	if mgr.Get().ListenPort != 9999 {
		t.Errorf("expected ListenPort 9999 after re-entrant commit, got %d", mgr.Get().ListenPort)
	}
}



func TestConfigManager_PartialConfigPreservesDefaults(t *testing.T) {
	tempFile, err := os.CreateTemp("", "gbf_partial_config_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	if err := os.WriteFile(tempFile.Name(), []byte(`{"listen_port":9001}`), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tempFile.Name())
	cfg := mgr.Get()
	defaults := DefaultConfig()

	if cfg.ListenPort != 9001 {
		t.Fatalf("expected persisted ListenPort=9001, got %d", cfg.ListenPort)
	}
	if cfg.CleanZombies != defaults.CleanZombies ||
		cfg.EnableRAMCache != defaults.EnableRAMCache ||
		cfg.EnablePrefetch != defaults.EnablePrefetch ||
		cfg.VerifyUpstreamTLS != defaults.VerifyUpstreamTLS ||
		cfg.EnableAPITelemetry != defaults.EnableAPITelemetry {
		t.Fatalf("partial config reset defaults: %+v", cfg)
	}
}


func TestUpstreamFailoverDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.EnableUpstreamFailover {
		t.Fatal("upstream failover must be disabled by default")
	}
	if cfg.BackupUpstreamProxy != "" {
		t.Fatalf("backup upstream default %q", cfg.BackupUpstreamProxy)
	}
	if cfg.UpstreamFailoverThresholdMS != 2000 {
		t.Fatalf("threshold default %d", cfg.UpstreamFailoverThresholdMS)
	}
	if cfg.UpstreamFailoverConsecutiveFailures != 3 {
		t.Fatalf("consecutive default %d", cfg.UpstreamFailoverConsecutiveFailures)
	}
	if cfg.UpstreamFailoverCooldownSeconds != 60 {
		t.Fatalf("cooldown default %d", cfg.UpstreamFailoverCooldownSeconds)
	}
	if !cfg.UpstreamFailoverAutoRecover {
		t.Fatal("auto recovery should default to true")
	}
}

func TestUpstreamFailoverConfigPersistsAndNormalizes(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "config.json")
	mgr := NewManager(path)
	mgr.Update(func(c *Config) {
		c.BackupUpstreamProxy = "socks5://127.0.0.1:10808"
		c.EnableUpstreamFailover = true
		c.UpstreamFailoverThresholdMS = 999999
		c.UpstreamFailoverConsecutiveFailures = 99
		c.UpstreamFailoverCooldownSeconds = 0
		c.UpstreamFailoverAutoRecover = false
	})
	got := mgr.Get()
	if got.UpstreamFailoverThresholdMS != 60000 || got.UpstreamFailoverConsecutiveFailures != 10 || got.UpstreamFailoverCooldownSeconds != 60 {
		t.Fatalf("normalized config: %+v", got)
	}
	loaded := NewManager(path).Get()
	if loaded.BackupUpstreamProxy != got.BackupUpstreamProxy ||
		loaded.EnableUpstreamFailover != got.EnableUpstreamFailover ||
		loaded.UpstreamFailoverThresholdMS != got.UpstreamFailoverThresholdMS ||
		loaded.UpstreamFailoverConsecutiveFailures != got.UpstreamFailoverConsecutiveFailures ||
		loaded.UpstreamFailoverCooldownSeconds != got.UpstreamFailoverCooldownSeconds ||
		loaded.UpstreamFailoverAutoRecover != got.UpstreamFailoverAutoRecover {
		t.Fatalf("persisted mismatch: got=%+v loaded=%+v", got, loaded)
	}
}
