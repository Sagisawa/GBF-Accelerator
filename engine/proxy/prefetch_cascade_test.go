package proxy

import (
	"bytes"
	"net/http"
	"path/filepath"
	"strings"
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

func TestPrefetch_WarmDiskCascade_StartJSONWarmsSpritesToRAM(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	_ = cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = true
		c.EnableRAMCache = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int32
	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	srv.assetClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			upstreamHits.Add(1)
			t.Errorf("unexpected upstream request during warm-disk cascade: %s", r.URL.String())
			return nil, http.ErrHandlerTimeout
		}),
	})
	pe := newPrefetchEngine(srv)
	defer func() {
		pe.Stop()
		if !pe.WaitTimeout(2 * time.Second) {
			t.Fatalf("prefetch workers did not exit cleanly")
		}
	}()

	const cdnHost = "prd-game-a-granbluefantasy.akamaized.net"
	manifestPath := "/assets/1795000000/js/model/manifest/boss_warm.js"
	cjsPath := "/assets/1795000000/js/cjs/boss_warm.js"
	spritePath := "/assets/img/sp/cjs/boss_warm_a.png"
	depth2SpritePath := "/assets/img/sp/cjs/boss_warm_too_deep.png"
	depth2JSPath := "/assets/1795000000/js/cjs/boss_warm_sub.js"

	manifestBody := []byte(`define([],function(){});`)
	cjsBody := []byte(`(function(lib){lib.properties={manifest:[{src:"sp/cjs/boss_warm_a.png",id:"a"}]};var s="/assets/1795000000/js/cjs/boss_warm_sub.js";})(cjs);`)
	depth2JSBody := []byte(`(function(lib){lib.properties={manifest:[{src:"sp/cjs/boss_warm_too_deep.png"}]};})(cjs);`)
	spriteBody := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0x33}, 512)...)

	for _, f := range []struct {
		p    string
		ct   string
		body []byte
	}{
		{manifestPath, "application/javascript", manifestBody},
		{cjsPath, "application/javascript", cjsBody},
		{depth2JSPath, "application/javascript", depth2JSBody},
		{spritePath, "image/png", spriteBody},
		{depth2SpritePath, "image/png", spriteBody},
	} {
		if !cacheMgr.Save(f.p, map[string]string{"content-type": f.ct}, f.body) {
			t.Fatalf("failed to seed disk cache for %s", f.p)
		}
	}

	// Simulate cold RAM with all files present on disk only.
	cacheMgr.ClearRAM()

	req, _ := http.NewRequest(http.MethodPost, "https://game.granbluefantasy.jp/rest/multiraid/start.json", nil)
	req.AddCookie(&http.Cookie{Name: "language_type", Value: "1"})
	req.Header.Set("X-VERSION", "1795000000")
	pe.RecordActiveGBFHost(cdnHost)
	startJSON := []byte(`{"boss":{"param":[{"cjs":"boss_warm"}]}}`)
	pe.MaybeEnqueueGameData(req, "game.granbluefantasy.jp", startJSON)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cacheMgr.IsInRAMWithNamespace("gbf", cjsPath) && cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	if !cacheMgr.IsInRAMWithNamespace("gbf", cjsPath) {
		t.Fatalf("expected %s to be warmed into RAM", cjsPath)
	}
	if !cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
		t.Fatalf("expected cascaded sprite %s to be warmed from disk into RAM", spritePath)
	}
	if cacheMgr.IsRAMProtected("gbf", spritePath) {
		t.Fatalf("expected warmed sprite %s to stay in SLRU Probation, not Protected", spritePath)
	}

	time.Sleep(100 * time.Millisecond)
	if cacheMgr.IsInRAMWithNamespace("gbf", depth2SpritePath) {
		t.Fatalf("expected depth > 1 sprite %s NOT to be cascaded into RAM", depth2SpritePath)
	}
	if upstreamHits.Load() != 0 {
		t.Fatalf("expected 0 upstream network requests during warm-disk cascade, got %d", upstreamHits.Load())
	}

	// Also verify already-in-RAM P1 scripts still cascade depth 0 -> 1 on a fresh uncached sprite discovery.
	ramScript := "/assets/1795000000/js/cjs/hero_in_ram.js"
	ramSprite := "/assets/img/sp/cjs/hero_in_ram_a.png"
	if !cacheMgr.Save(ramSprite, map[string]string{"content-type": "image/png"}, spriteBody) {
		t.Fatal("save ramSprite failed")
	}
	cacheMgr.ClearRAM()
	if _, ok := cacheMgr.SaveRAM(ramScript, map[string]string{"content-type": "application/javascript"},
		[]byte(`(function(lib){lib.properties={manifest:[{src:"sp/cjs/hero_in_ram_a.png"}]};})(cjs);`)); !ok {
		t.Fatal("SaveRAM hero_in_ram.js failed")
	}
	pe.dispatchDiscoveredRefsWithDepth([]discoveredRef{
		{host: cdnHost, path: ramScript, prio: 1},
	}, 0)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cacheMgr.IsInRAMWithNamespace("gbf", ramSprite) {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !cacheMgr.IsInRAMWithNamespace("gbf", ramSprite) {
		t.Fatalf("expected RAM-resident P1 script to cascade warm %s from disk", ramSprite)
	}
}

func TestExtractAssetRefs_SIMDWindowBoundaryEquivalence(t *testing.T) {
	pe := &PrefetchEngine{}
	const cdnHost = "prd-game-a-granbluefantasy.akamaized.net"

	// Place slash-containing non-asset expressions right before boundary offsets (512, 1024),
	// plus max-prefix "assets_en/" (10 bytes from opening quote to '/') and ".woff2" / ".css" right across 512B.
	pad500 := strings.Repeat("x", 495)
	jsBody := []byte(`var a=(1/2);` + pad500 +
		`"assets_en/1795000000/font/boundary_check.woff2";` +
		`var b="not/an/asset";` +
		strings.Repeat(" ", 490) +
		`(https://` + cdnHost + `/assets/1795000000/img/sp/unit_01.webp);` +
		`{css:"css/common/main.css",src:"sp/cjs/effect_02.png"}`)

	refs := pe.extractAssetRefs("/assets_en/1795000000/js/main.js", jsBody, cdnHost)
	got := make(map[string]bool, len(refs))
	for _, r := range refs {
		got[r[0]+":"+r[1]] = true
	}

	wants := []string{
		cdnHost + ":/assets_en/1795000000/font/boundary_check.woff2",
		cdnHost + ":/assets/1795000000/img/sp/unit_01.webp",
		cdnHost + ":/css/common/main.css",
		cdnHost + ":/assets_en/img/sp/cjs/effect_02.png",
	}
	for _, w := range wants {
		if !got[w] {
			t.Fatalf("missing expected ref %q; got=%+v", w, refs)
		}
	}
	if len(refs) != len(wants) {
		t.Fatalf("expected %d refs, got %d: %+v", len(wants), len(refs), refs)
	}
}

func TestPrefetch_WarmDiskCascade_Depth1PreemptDoesNotDropDepth0Cascade(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	_ = cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = true
		c.EnableRAMCache = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	srv.assetClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			t.Errorf("unexpected upstream request during preemption cascade test: %s", r.URL.String())
			return nil, http.ErrHandlerTimeout
		}),
	})
	pe := newPrefetchEngine(srv)
	defer func() {
		pe.Stop()
		if !pe.WaitTimeout(2 * time.Second) {
			t.Fatalf("prefetch workers did not exit cleanly")
		}
	}()

	const cdnHost = "prd-game-a-granbluefantasy.akamaized.net"
	cjsPath := "/assets/1795000000/js/cjs/preempt_boss.js"
	spritePath := "/assets/img/sp/cjs/preempt_boss_a.png"

	cjsBody := []byte(`(function(lib){lib.properties={manifest:[{src:"sp/cjs/preempt_boss_a.png",id:"a"}]};})(cjs);`)
	spriteBody := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0x44}, 512)...)

	if !cacheMgr.Save(cjsPath, map[string]string{"content-type": "application/javascript"}, cjsBody) {
		t.Fatalf("failed to seed disk cache for %s", cjsPath)
	}
	if !cacheMgr.Save(spritePath, map[string]string{"content-type": "image/png"}, spriteBody) {
		t.Fatalf("failed to seed disk cache for %s", spritePath)
	}
	cacheMgr.ClearRAM()

	// Simulate depth=1 warm (e.g. manifest -> twin CJS) admitting cjsPath into RAM first.
	pe.diskWarmCh <- prefetchItem{prio: 1, host: cdnHost, path: cjsPath, depth: 1}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cacheMgr.IsInRAMWithNamespace("gbf", cjsPath) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !cacheMgr.IsInRAMWithNamespace("gbf", cjsPath) {
		t.Fatalf("expected %s to be warmed into RAM by depth=1 task", cjsPath)
	}

	// Now process the depth=0 warm task for the same already-RAM-resident script.
	pe.diskWarmCh <- prefetchItem{prio: 1, host: cdnHost, path: cjsPath, depth: 0}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
		t.Fatalf("expected %s to be warmed from disk into RAM even when depth=1 preempted depth=0", spritePath)
	}
	if cacheMgr.IsRAMProtected("gbf", spritePath) {
		t.Fatalf("expected warmed sprite %s to stay in SLRU Probation, not Protected", spritePath)
	}
}

