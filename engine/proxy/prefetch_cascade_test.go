package proxy

import (
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestPrefetch_SecondaryCascade_ManifestToCJSToSprite(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	_ = cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const cdnHost = "prd-game-a-granbluefantasy.akamaized.net"
	cjsBody := []byte(`(function(lib){lib.properties={manifest:[{src:"sp/cjs/boss_001_a.png",id:"boss_001_a"}]};})(cjs);`)
	spriteBody := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89boss-sprite")
	// Add a secondary .js inside cjsBody that must NOT cascade further (depth > 1 guard)
	cjsWithExtraJS := append(cjsBody, []byte(`;var extra="/assets/1795000000/js/should_stop_depth.js";`)...)
	extraJSBody := []byte(`var deeper="/assets/1795000000/img/sp/too_deep.png";`)

	var cjsHits, spriteHits, deepImgHits atomic.Int32
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/assets/1795000000/js/cjs/boss_001.js":
			cjsHits.Add(1)
			w.Header().Set("Content-Type", "application/javascript")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cjsWithExtraJS)
		case "/assets/img/sp/cjs/boss_001_a.png":
			spriteHits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(spriteBody)
		case "/assets/1795000000/js/should_stop_depth.js":
			w.Header().Set("Content-Type", "application/javascript")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(extraJSBody)
		case "/assets/1795000000/img/sp/too_deep.png":
			deepImgHits.Add(1)
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(spriteBody)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer func() {
		pe.Stop()
		if !pe.WaitTimeout(2 * time.Second) {
			t.Fatalf("prefetch workers did not exit cleanly")
		}
	}()

	// Foreground loads model/manifest/boss_001.js -> should derive cjs/boss_001.js (depth=0)
	// and cascade once into sp/cjs/boss_001_a.png (depth=1).
	manifestPath := "/assets/1795000000/js/model/manifest/boss_001.js"
	pe.MaybeEnqueueDiscovery(cdnHost, manifestPath, []byte(`define(["cjs/boss_001"]);`))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cjsHits.Load() >= 1 && spriteHits.Load() >= 1 && cacheMgr.HasCache("/assets/img/sp/cjs/boss_001_a.png") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if cjsHits.Load() != 1 {
		t.Fatalf("expected 1 upstream fetch for derived cjs, got %d", cjsHits.Load())
	}
	if spriteHits.Load() != 1 || !cacheMgr.HasCache("/assets/img/sp/cjs/boss_001_a.png") {
		t.Fatalf("expected cascaded sprite to be prefetched and cached, hits=%d", spriteHits.Load())
	}

	// Give workers a brief moment to verify depth>1 does not cascade a 3rd hop
	time.Sleep(100 * time.Millisecond)
	if deepImgHits.Load() != 0 {
		t.Fatalf("expected depth>1 hop not to cascade to too_deep.png, got %d hits", deepImgHits.Load())
	}
}

func TestPrefetch_WaitPacerEnforcesJitterAfterFirstItem(t *testing.T) {
	pe := &PrefetchEngine{
		stopChan: make(chan struct{}),
	}
	defer close(pe.stopChan)

	// Even with empty queues (QueueLen() == 0), waitPacer() must let the first item
	// go immediately (0ms delay) while advancing nextPaceAt so rapid follow-ups are
	// spaced by at least 15ms.
	t0 := time.Now()
	if !pe.waitPacer() {
		t.Fatal("first waitPacer call returned false")
	}
	if firstDur := time.Since(t0); firstDur > 12*time.Millisecond {
		t.Fatalf("expected first waitPacer call to have ~0ms delay, got %v", firstDur)
	}

	t1 := time.Now()
	if !pe.waitPacer() {
		t.Fatal("second waitPacer call returned false")
	}
	if secondDur := time.Since(t1); secondDur < 12*time.Millisecond {
		t.Fatalf("expected second waitPacer call to enforce jittered pacing (>=15ms target), got %v", secondDur)
	}
}
