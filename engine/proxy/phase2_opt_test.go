package proxy

import (
	"bytes"
	"net/http"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestIsCleanFastStaticRequest_SecurityEquivalence(t *testing.T) {
	cleanCases := []struct {
		reqURI, urlPath, host string
	}{
		{"/assets/1795000000/js/core.js", "/assets/1795000000/js/core.js", "prd-game-a1-granbluefantasy.akamaized.net"},
		{"/assets/img/sp/assets/npc/m/3040001000_01.jpg?t=12:34:56", "/assets/img/sp/assets/npc/m/3040001000_01.jpg", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets_en/1795000000/js/model/manifest/nsp_123.js", "/assets_en/1795000000/js/model/manifest/nsp_123.js", "prd-game-a2-granbluefantasy.akamaized.net"},
	}
	for _, tc := range cleanCases {
		if !isCleanFastStaticRequest(tc.reqURI, tc.urlPath, tc.host) {
			t.Errorf("expected clean fast path for (%q, %q, %q)", tc.reqURI, tc.urlPath, tc.host)
		}
	}

	mustFallback := []struct {
		reqURI, urlPath, host string
	}{
		{"/assets/../etc/passwd", "/assets/../etc/passwd", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets/%2e%2e/secret.js", "/assets/%2e%2e/secret.js", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets/Windows/system.ini", "/assets/Windows/system.ini", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets/windows/system.ini", "/assets/windows/system.ini", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets/etc/passwd", "/assets/etc/passwd", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets/c:/secret.png", "/assets/c:/secret.png", "prd-game-a-granbluefantasy.akamaized.net"},
		{"\\windows\\win.ini", "\\windows\\win.ini", "prd-game-a-granbluefantasy.akamaized.net"},
		{"//evil.com/assets/a.js", "/assets/a.js", "prd-game-a-granbluefantasy.akamaized.net"},
		{"http://prd-game-a-granbluefantasy.akamaized.net/assets/a.js", "/assets/a.js", "prd-game-a-granbluefantasy.akamaized.net"},
		{"/assets/a.js", "/assets/a.js", "prd-game-a-granbluefantasy.akamaized.net:443"},
		{"/assets/UpperCase.png", "/assets/UpperCase.png", "prd-game-a-granbluefantasy.akamaized.net"},
	}
	for _, tc := range mustFallback {
		if isCleanFastStaticRequest(tc.reqURI, tc.urlPath, tc.host) {
			t.Errorf("expected slow-path fallback for (%q, %q, %q)", tc.reqURI, tc.urlPath, tc.host)
		}
	}
}

func TestCacheHit_TriggersCascadeDiskToRAMWarmupZeroNetwork(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = true
		c.EnableRAMCache = true
	})
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("cert.NewManager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer func() {
		srv.Stop()
		srv.WaitPrefetchWorkers(2 * time.Second)
	}()

	var upstreamHits atomic.Int32
	srv.assetClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			upstreamHits.Add(1)
			t.Errorf("unexpected upstream network request during Disk->RAM warmup: %s", r.URL.String())
			return nil, http.ErrHandlerTimeout
		}),
	})

	manifestPath := "/assets/1795000000/js/model/manifest/hero_01.js"
	twinCJSPath := "/assets/1795000000/js/cjs/hero_01.js"
	spritePath := "/assets/img/sp/assets/npc/hero_01.png"

	manifestBody := []byte(`define([], function(){ return { manifest: [{ src: "sp/assets/npc/hero_01.png" }] }; });`)
	cjsBody := []byte(`(function(lib){ lib.hero_01 = function(){}; })(window);`)
	pngBody := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte{0x42}, 1024)...)

	if !cacheMgr.Save(manifestPath, map[string]string{"content-type": "application/javascript"}, manifestBody) {
		t.Fatal("save manifest failed")
	}
	if !cacheMgr.Save(twinCJSPath, map[string]string{"content-type": "application/javascript"}, cjsBody) {
		t.Fatal("save twin cjs failed")
	}
	if !cacheMgr.Save(spritePath, map[string]string{"content-type": "image/png"}, pngBody) {
		t.Fatal("save sprite failed")
	}

	// Simulate cold RAM with all files on disk
	cacheMgr.ClearRAM()
	if cacheMgr.IsInRAMWithNamespace("gbf", twinCJSPath) || cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
		t.Fatal("expected RAM to be empty before foreground manifest request")
	}

	req := &http.Request{
		Method:     http.MethodGet,
		Host:       "prd-game-a-granbluefantasy.akamaized.net",
		RequestURI: manifestPath,
		URL:        &url.URL{Path: manifestPath},
		Header:     http.Header{},
	}
	var out bytes.Buffer
	if !srv.handleStaticAsset(&out, req, "prd-game-a-granbluefantasy.akamaized.net") {
		t.Fatal("expected keepAlive=true on cached manifest hit")
	}
	if !bytes.Contains(out.Bytes(), manifestBody) {
		t.Fatal("expected manifest body in foreground response")
	}

	// Wait for background Disk->RAM warmup workers to load twin CJS & sprite into RAM Probation
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cacheMgr.IsInRAMWithNamespace("gbf", twinCJSPath) && cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}

	if !cacheMgr.IsInRAMWithNamespace("gbf", twinCJSPath) {
		t.Fatal("expected twin CJS to be preloaded from disk into RAM")
	}
	if !cacheMgr.IsInRAMWithNamespace("gbf", spritePath) {
		t.Fatal("expected referenced sprite PNG to be preloaded from disk into RAM")
	}
	if cacheMgr.IsRAMProtected("gbf", twinCJSPath) || cacheMgr.IsRAMProtected("gbf", spritePath) {
		t.Fatal("expected Disk->RAM warmed assets to stay in SLRU Probation, not Protected")
	}
	if upstreamHits.Load() != 0 {
		t.Fatalf("expected 0 upstream network requests, got %d", upstreamHits.Load())
	}
}

func TestGameDataPrefetch_SinglePlayerRaidStart(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	if !isGameDataAPI(http.MethodPost, "/rest/raid/start.json") {
		t.Fatal("expected POST /rest/raid/start.json to be recognized as Game-Data API")
	}
	if isGameDataAPI(http.MethodGet, "/rest/raid/start.json") {
		t.Fatal("expected GET /rest/raid/start.json NOT to be recognized as Game-Data API")
	}

	payload := `{
		"player": {"param": [{"cjs": "solo_hero_01"}]},
		"boss": {"param": [{"cjs": "solo_boss_01"}]},
		"background_image_object": ["/sp/raid/bg/solo_bg.jpg"]
	}`
	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/raid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "1795000002",
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 5 {
		t.Fatalf("expected 5 refs for /rest/raid/start.json, got %d: %+v", len(refs), refs)
	}
	if refs[0].path != "/assets/1795000002/js/model/manifest/solo_hero_01.js" || refs[0].prio != 1 {
		t.Fatalf("unexpected ref[0]: %+v", refs[0])
	}
	if refs[4].path != "/assets/img/sp/raid/bg/solo_bg.jpg" || refs[4].prio != 2 {
		t.Fatalf("unexpected ref[4]: %+v", refs[4])
	}
}
