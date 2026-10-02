package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// Helper to create a test ProxyServer with in-memory cache and telemetry
func createTestProxyForGameData(t *testing.T, enablePrefetch bool) (*ProxyServer, *PrefetchEngine, func()) {
	t.Helper()
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = enablePrefetch
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	stats := telemetry.NewStats()
	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	pe := srv.prefetch
	cleanup := func() {
		if pe != nil {
			pe.Stop()
		}
		cacheMgr.Close()
	}
	return srv, pe, cleanup
}

// 1. start.json 正常解析
func TestGameDataPrefetch_MultiRaidStart_Normal(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"player": {
			"param": [
				{"cjs": "nsp_3040058000_01"}
			]
		},
		"boss": {
			"param": [
				{"cjs": "raid_boss_common_01"}
			]
		},
		"background_image_object": [
			"/sp/raid/bg/common_014.jpg"
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "1790900582",
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 5 {
		t.Fatalf("expected 5 refs (2 player + 2 boss + 1 bg), got %d: %+v", len(refs), refs)
	}

	expectedPaths := []struct {
		path string
		prio int
	}{
		{"/assets/1790900582/js/model/manifest/nsp_3040058000_01.js", 1},
		{"/assets/1790900582/js/cjs/nsp_3040058000_01.js", 1},
		{"/assets/1790900582/js/model/manifest/raid_boss_common_01.js", 1},
		{"/assets/1790900582/js/cjs/raid_boss_common_01.js", 1},
		{"/assets/img/sp/raid/bg/common_014.jpg", 2},
	}

	for i, exp := range expectedPaths {
		if refs[i].path != exp.path {
			t.Errorf("ref[%d] path mismatch: expected %q, got %q", i, exp.path, refs[i].path)
		}
		if refs[i].prio != exp.prio {
			t.Errorf("ref[%d] prio mismatch: expected %d, got %d", i, exp.prio, refs[i].prio)
		}
	}
}

// 2. player + boss 多个 cjs 正确生成 manifest/CJS URL
func TestGameDataPrefetch_MultiRaidStart_MultipleCJS(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"player": {
			"param": [
				{"cjs": "p1"},
				{"cjs": "p2"},
				{"cjs": "p3"},
				{"cjs": "p4"}
			]
		},
		"boss": {
			"param": [
				{"cjs": "b1"},
				{"cjs": "p1"}
			]
		}
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "2000000001",
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	// Unique CJS count: p1, p2, p3, p4, b1 -> 5 unique CJS -> 10 refs total
	if len(refs) != 10 {
		t.Fatalf("expected 10 refs (5 unique CJS * 2), got %d: %+v", len(refs), refs)
	}

	for _, r := range refs {
		if r.prio != 1 {
			t.Errorf("expected prio 1 for CJS ref, got %d for %s", r.prio, r.path)
		}
		if !strings.HasPrefix(r.path, "/assets/2000000001/js/") {
			t.Errorf("expected versioned js path, got %s", r.path)
		}
	}
}

// 3. background_image_object 正确生成背景 URL
func TestGameDataPrefetch_MultiRaidStart_BackgroundImageObject(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// Test array of backgrounds: only first must be taken into P2
	payloadArray := `{
		"player": {"param": []},
		"background_image_object": [
			"/sp/raid/bg/first.jpg",
			"/sp/raid/bg/second.jpg",
			"/sp/raid/bg/third.png"
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payloadArray),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "100",
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 1 {
		t.Fatalf("expected exactly 1 background ref, got %d", len(refs))
	}
	if refs[0].path != "/assets/img/sp/raid/bg/first.jpg" {
		t.Errorf("expected first background, got %s", refs[0].path)
	}
	if refs[0].prio != 2 {
		t.Errorf("expected prio 2 for background, got %d", refs[0].prio)
	}

	// Test single string background
	payloadString := `{
		"background_image_object": "sp/raid/bg/single.png"
	}`
	cand.data = []byte(payloadString)
	cand.langPrefix = "/assets_en"
	refsStr := pe.extractGameDataRefs(cand)
	if len(refsStr) != 1 || refsStr[0].path != "/assets_en/img/sp/raid/bg/single.png" {
		t.Fatalf("expected /assets_en/img/sp/raid/bg/single.png, got: %+v", refsStr)
	}
}

// 4. X-VERSION 正确参与 URL 构造
func TestGameDataPrefetch_MultiRaidStart_XVersion(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"player": {"param": [{"cjs": "test_actor"}]}
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "99887766",
		langPrefix: "/assets_en",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	expectedManifest := "/assets_en/99887766/js/model/manifest/test_actor.js"
	expectedCJS := "/assets_en/99887766/js/cjs/test_actor.js"
	if refs[0].path != expectedManifest {
		t.Errorf("expected manifest %q, got %q", expectedManifest, refs[0].path)
	}
	if refs[1].path != expectedCJS {
		t.Errorf("expected cjs %q, got %q", expectedCJS, refs[1].path)
	}
}

// 5. 缺失 X-VERSION 时不会错误生成 URL
func TestGameDataPrefetch_MultiRaidStart_MissingXVersion(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"player": {"param": [{"cjs": "test_actor"}]},
		"boss": {"param": [{"cjs": "test_boss"}]},
		"background_image_object": ["/sp/raid/bg/common_014.jpg"]
	}`

	// 5.1 Empty X-VERSION
	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "",
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	// Must NOT generate CJS URLs, only background is safe
	for _, r := range refs {
		if strings.Contains(r.path, "/js/") || strings.Contains(r.path, "manifest") || strings.Contains(r.path, "test_") {
			t.Errorf("unexpected CJS ref when X-VERSION is missing: %s", r.path)
		}
	}
	if len(refs) != 1 || refs[0].path != "/assets/img/sp/raid/bg/common_014.jpg" {
		t.Errorf("expected only background ref when X-VERSION missing, got: %+v", refs)
	}

	// 5.2 Non-numeric X-VERSION
	cand.xVersion = "invalid_version_chars"
	refsInvalid := pe.extractGameDataRefs(cand)
	for _, r := range refsInvalid {
		if strings.Contains(r.path, "/js/") {
			t.Errorf("unexpected CJS ref when X-VERSION is non-numeric: %s", r.path)
		}
	}
}

// 6. cleared_quest_scenario 能正确解析前 5 幕
func TestGameDataPrefetch_ClearedQuest_First5Scenes(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"scene_list": [
			{"bg1": "/sp/quest/scene/bg/bg1.jpg"},
			{"bg1": "/sp/quest/scene/bg/bg2.jpg"},
			{"bg1": "/sp/quest/scene/bg/bg3.jpg"},
			{"bg1": "/sp/quest/scene/bg/bg4.jpg"},
			{"bg1": "/sp/quest/scene/bg/bg5.jpg"},
			{"bg1": "/sp/quest/scene/bg/bg6.jpg"},
			{"bg1": "/sp/quest/scene/bg/bg7.jpg"}
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/quest/cleared_quest_scenario/10001/1",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodGet,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 5 {
		t.Fatalf("expected exactly 5 refs, got %d: %+v", len(refs), refs)
	}

	for i := 1; i <= 5; i++ {
		expected := fmt.Sprintf("/assets/img/sp/quest/scene/bg/bg%d.jpg", i)
		if refs[i-1].path != expected {
			t.Errorf("expected %q at %d, got %q", expected, i-1, refs[i-1].path)
		}
		if refs[i-1].prio != 2 {
			t.Errorf("expected prio 2, got %d", refs[i-1].prio)
		}
	}
}

// 7. 前 5 幕资源正确去重
func TestGameDataPrefetch_ClearedQuest_Deduplication(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"scene_list": [
			{"bg1": "/sp/quest/scene/bg/common_bg.jpg", "charcter1_big_image": "/sp/quest/scene/character/body/char1.png"},
			{"bg1": "/sp/quest/scene/bg/common_bg.jpg", "charcter1_big_image": "/sp/quest/scene/character/body/char1.png"},
			{"bg1": "/sp/quest/scene/bg/common_bg.jpg", "charcter1_big_image": "/sp/quest/scene/character/body/char2.png"}
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/quest/cleared_quest_scenario/10001/1",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodGet,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	// Expected unique: common_bg.jpg, char1.png, char2.png -> 3 refs total
	if len(refs) != 3 {
		t.Fatalf("expected 3 deduplicated refs, got %d: %+v", len(refs), refs)
	}

	foundMap := make(map[string]bool)
	for _, r := range refs {
		foundMap[r.path] = true
	}
	expected := []string{
		"/assets/img/sp/quest/scene/bg/common_bg.jpg",
		"/assets/img/sp/quest/scene/character/body/char1.png",
		"/assets/img/sp/quest/scene/character/body/char2.png",
	}
	for _, exp := range expected {
		if !foundMap[exp] {
			t.Errorf("missing expected deduplicated path: %s", exp)
		}
	}
}

// 8. 最多只进入前 5 个视觉资源
func TestGameDataPrefetch_ClearedQuest_Max5VisualLimit(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// Scene 1 has 3 images, Scene 2 has 3 images -> Total 6 unique images across first 2 scenes
	payload := `{
		"scene_list": [
			{
				"bg1": "/sp/quest/scene/bg/b1.jpg",
				"charcter1_big_image": "/sp/quest/scene/character/c1.png",
				"charcter2_big_image": "/sp/quest/scene/character/c2.png"
			},
			{
				"bg1": "/sp/quest/scene/bg/b2.jpg",
				"charcter1_big_image": "/sp/quest/scene/character/c3.png",
				"charcter2_big_image": "/sp/quest/scene/character/c4.png"
			}
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/quest/cleared_quest_scenario/10001/1",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodGet,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) > 5 {
		t.Fatalf("exceeded 5 visual assets limit: got %d", len(refs))
	}
	if len(refs) != 5 {
		t.Fatalf("expected capped exactly at 5, got %d", len(refs))
	}
}

// 9. 音频永远不会进入 Game-Data Prefetch
func TestGameDataPrefetch_NoAudio(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// Both endpoints containing audio fields
	scenarioPayload := `{
		"scene_list": [
			{
				"bg1": "/sp/quest/scene/bg/bg1.jpg",
				"bgm": "/sound/bgm/battle.mp3",
				"bgm1": "/sound/bgm/theme.wav",
				"sound": "/sound/se/sword.ogg",
				"voice": "/sound/voice/attack.m4a",
				"charcter1_big_image": "/sound/fake_char.mp3",
				"charcter2_big_image": "/sp/quest/scene/character/char1.png"
			}
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/quest/cleared_quest_scenario/10001/1",
		data:       []byte(scenarioPayload),
		isGameData: true,
		reqMethod:  http.MethodGet,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	for _, r := range refs {
		p := strings.ToLower(r.path)
		if strings.HasSuffix(p, ".mp3") || strings.HasSuffix(p, ".wav") || strings.HasSuffix(p, ".ogg") || strings.HasSuffix(p, ".m4a") {
			t.Fatalf("CRITICAL: Audio asset leaked into Game-Data Prefetch: %s", r.path)
		}
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 visual assets, got %d: %+v", len(refs), refs)
	}

	// Also check background_image_object with audio
	startPayload := `{
		"background_image_object": ["/sound/theme.mp3"]
	}`
	candRaid := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(startPayload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		langPrefix: "/assets",
	}
	raidRefs := pe.extractGameDataRefs(candRaid)
	if len(raidRefs) != 0 {
		t.Fatalf("expected 0 refs for audio background, got: %+v", raidRefs)
	}
}

// 10. 异常 JSON 不 panic
func TestGameDataPrefetch_MalformedJSON_NoPanic(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	malformedCases := [][]byte{
		nil,
		[]byte(""),
		[]byte("{"),
		[]byte("not a json"),
		[]byte(`{"player": "not_an_object"}`),
		[]byte(`{"player": {"param": "not_an_array"}}`),
		[]byte(`{"player": {"param": [{"cjs": null}]}}`),
		[]byte(`{"player": {"param": [{"cjs": 12345}]}}`),
		[]byte(`{"player": {"param": [{"cjs": "../../../evil"}]}}`),
		[]byte(`{"scene_list": null}`),
		[]byte(`{"scene_list": "string"}`),
		[]byte(`{"scene_list": [null, 123, "text", {"bg1": null}, {"charcter1_big_image": 123}]}`),
		[]byte(`{"background_image_object": null}`),
		[]byte(`{"background_image_object": false}`),
		[]byte(`{"background_image_object": [{"path": 12345}]}`),
	}

	for i, c := range malformedCases {
		candRaid := prefetchCandidate{
			host:       "prd-game-a-granbluefantasy.akamaized.net",
			path:       "/rest/multiraid/start.json",
			data:       c,
			isGameData: true,
			reqMethod:  http.MethodPost,
			xVersion:   "123",
			langPrefix: "/assets",
		}
		// Must not panic
		_ = pe.extractGameDataRefs(candRaid)

		candQuest := prefetchCandidate{
			host:       "prd-game-a-granbluefantasy.akamaized.net",
			path:       "/rest/quest/cleared_quest_scenario/1/1",
			data:       c,
			isGameData: true,
			reqMethod:  http.MethodGet,
			langPrefix: "/assets",
		}
		// Must not panic
		_ = pe.extractGameDataRefs(candQuest)

		if t.Failed() {
			t.Fatalf("panicked or failed on case %d", i)
		}
	}
}

// 11. Prefetch 关闭时 Game-Data Discovery 完全不工作
func TestGameDataPrefetch_Disabled(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, false) // EnablePrefetch = false
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, "https://game.granbluefantasy.jp/rest/multiraid/start.json", nil)
	req.Header.Set("X-VERSION", "1790900582")
	body := []byte(`{"player": {"param": [{"cjs": "test_actor"}]}}`)

	pe.MaybeEnqueueGameData(req, "game.granbluefantasy.jp", body)

	if len(pe.discoveryCh) != 0 {
		t.Errorf("expected discoveryCh to remain empty when prefetch disabled, got len %d", len(pe.discoveryCh))
	}
}

// 12. 与现有 Prefetch 的 URL 去重正常
func TestGameDataPrefetch_DeduplicationWithExistingPrefetch(t *testing.T) {
	srv, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	host := "prd-game-a-granbluefantasy.akamaized.net"
	manifestPath := "/assets/1790900582/js/model/manifest/test_dedup.js"

	// 1. Simulate existing cache has this asset
	ns, _ := NormalizeAssetNamespace(host)
	srv.cacheMgr.SaveRAMValidatedWithNamespace(ns, manifestPath, map[string]string{"content-type": "application/javascript"}, []byte("console.log(1);"))

	// Stop workers so tasks remain in queue for direct inspection
	pe.Stop()
	time.Sleep(20 * time.Millisecond)

	// 2. Dispatch Game-Data discovery refs
	refs := []discoveredRef{
		{host: host, path: manifestPath, prio: 1},
		{host: host, path: "/assets/1790900582/js/cjs/test_dedup.js", prio: 1},
	}
	pe.dispatchDiscoveredRefs(refs)

	// Manifest is already cached, so only cjs should enter queue
	select {
	case item := <-pe.prio1Queue:
		if item.path != "/assets/1790900582/js/cjs/test_dedup.js" {
			t.Errorf("expected only uncached cjs item in queue, got %s", item.path)
		}
	default:
		t.Fatalf("expected queued cjs item, but prio1Queue was empty")
	}

	// prio1Queue must be empty now (no duplicate manifest task)
	if len(pe.prio1Queue) != 0 {
		t.Errorf("expected prio1Queue to be empty after taking cjs item, got len %d", len(pe.prio1Queue))
	}
}

// 13. 队列满时不会阻塞 API
func TestGameDataPrefetch_QueueFull_NonBlocking(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// Fill discoveryCh to capacity (128)
	dummy := prefetchCandidate{host: "dummy", path: "dummy"}
	for i := 0; i < cap(pe.discoveryCh); i++ {
		pe.discoveryCh <- dummy
	}

	req, _ := http.NewRequest(http.MethodPost, "https://game.granbluefantasy.jp/rest/multiraid/start.json", nil)
	req.Header.Set("X-VERSION", "1790900582")
	body := []byte(`{"player": {"param": [{"cjs": "test_actor"}]}}`)

	start := time.Now()
	// Must return immediately without blocking
	pe.MaybeEnqueueGameData(req, "game.granbluefantasy.jp", body)
	elapsed := time.Since(start)

	if elapsed > 10*time.Millisecond {
		t.Errorf("MaybeEnqueueGameData blocked on full queue for %v", elapsed)
	}
}

// 14. 动态 API 原有响应内容没有变化
func TestGameDataPrefetch_DynamicAPI_Transparency(t *testing.T) {
	srv, _, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	upstreamBody := `{"player":{"param":[{"cjs":"actor_one"}]},"success":true}`
	upstreamServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Custom-Game-Header", "game-val")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstreamBody))
	}))
	defer upstreamServer.Close()

	targetHost := "game.granbluefantasy.jp"
	tr := upstreamServer.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", upstreamServer.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true
	setTestAPIClient(srv, &http.Client{Transport: tr})

	// Send POST /rest/multiraid/start.json
	req, err := http.NewRequest(http.MethodPost, "https://"+targetHost+"/rest/multiraid/start.json", strings.NewReader(`{"param":"post_body"}`))
	if err != nil {
		t.Fatalf("failed to create req: %v", err)
	}
	req.Header.Set("X-VERSION", "1790900582")

	var responseBuf bytes.Buffer
	ok := srv.handleDynamicAPI(&responseBuf, req, targetHost)
	if !ok {
		t.Fatalf("handleDynamicAPI returned keepAlive false")
	}

	respStr := responseBuf.String()

	// 1. Status Code must be 200 OK
	if !strings.Contains(respStr, "HTTP/1.1 200 OK") {
		t.Errorf("expected 200 OK in response, got:\n%s", respStr)
	}

	// 2. Response body must match upstream byte-for-byte
	if !strings.Contains(respStr, upstreamBody) {
		t.Errorf("response body does not match upstream:\n%s", respStr)
	}

	// 3. Upstream custom header must be preserved
	if !strings.Contains(respStr, "X-Custom-Game-Header: game-val") {
		t.Errorf("upstream custom header missing in response:\n%s", respStr)
	}

	// 4. Zero custom proxy headers allowed
	forbiddenHeaders := []string{"X-Proxy", "X-Cache", "X-Acceleration"}
	for _, fh := range forbiddenHeaders {
		if strings.Contains(respStr, fh) {
			t.Errorf("zero header pollution violated: found %s in response", fh)
		}
	}
}

// 15. Additional Edge Case: Cookie language_type determines assets vs assets_en & Gzip decompression
func TestGameDataPrefetch_LanguageCookieSelection(t *testing.T) {
	srv, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// Japanese request (language_type=1)
	reqJP, _ := http.NewRequest(http.MethodPost, "https://game.granbluefantasy.jp/rest/multiraid/start.json", nil)
	reqJP.AddCookie(&http.Cookie{Name: "language_type", Value: "1"})
	if p := pe.determineLangPrefix(reqJP); p != "/assets" {
		t.Errorf("expected /assets for language_type=1, got %s", p)
	}

	// English request (language_type=2)
	reqEN, _ := http.NewRequest(http.MethodPost, "https://game.granbluefantasy.jp/rest/multiraid/start.json", nil)
	reqEN.AddCookie(&http.Cookie{Name: "language_type", Value: "2"})
	if p := pe.determineLangPrefix(reqEN); p != "/assets_en" {
		t.Errorf("expected /assets_en for language_type=2, got %s", p)
	}

	// Pause fetch worker using foreground activity counter to ensure deterministic queue inspection
	srv.stats.AddActiveFG(1)
	defer srv.stats.AddActiveFG(-1)

	// Gzipped response decompress in discoveryWorker
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	_, _ = gw.Write([]byte(`{"player":{"param":[{"cjs":"gzip_cjs"}]}}`))
	_ = gw.Close()

	reqJP.Header.Set("X-VERSION", "1790900582")
	pe.MaybeEnqueueGameData(reqJP, "game.granbluefantasy.jp", gzBuf.Bytes())

	// Verify discovery strictly succeeded without fake default fallback
	select {
	case item := <-pe.prio1Queue:
		if !strings.Contains(item.path, "gzip_cjs") {
			t.Errorf("expected gzip_cjs in queue, got %s", item.path)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for gzip_cjs in prio1Queue")
	}
}

// 16. Unknown language skips Game-Data Prefetch rather than guessing /assets
func TestGameDataPrefetch_UnknownLanguage_Skipped(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// Unrecorded language: activeLangPrefix is empty
	if prefix := pe.GetActiveLangPrefix(); prefix != "" {
		t.Fatalf("expected empty activeLangPrefix on fresh engine, got %q", prefix)
	}

	reqNoCookie, _ := http.NewRequest(http.MethodPost, "https://game.granbluefantasy.jp/rest/multiraid/start.json", nil)
	reqNoCookie.Header.Set("X-VERSION", "1790900582")
	body := []byte(`{"player":{"param":[{"cjs":"test_unknown_lang"}]}}`)

	pe.MaybeEnqueueGameData(reqNoCookie, "game.granbluefantasy.jp", body)

	// Must be dropped immediately due to unknown language
	if len(pe.discoveryCh) != 0 {
		t.Errorf("expected discoveryCh to remain empty when language unknown, got len %d", len(pe.discoveryCh))
	}

	// Direct extraction with empty langPrefix must return nil
	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       body,
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "1790900582",
		langPrefix: "",
	}
	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs when langPrefix is empty, got %d: %+v", len(refs), refs)
	}
}

// 17. Loose parsing: player.param containing non-object/corrupted items does NOT abort parsing
func TestGameDataPrefetch_MultiRaidStart_LooseParamTypes(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"player": {
			"param": [
				null,
				12345,
				"invalid_string_item",
				{"cjs": null},
				{"cjs": 999},
				{"cjs": "valid_player_cjs"}
			]
		},
		"boss": {
			"param": [
				{"cjs": "valid_boss_cjs"}
			]
		},
		"background_image_object": ["/sp/raid/bg/common_014.jpg"]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		xVersion:   "1790900582",
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	// Expected: 2 for valid_player_cjs + 2 for valid_boss_cjs + 1 for bg = 5 refs
	if len(refs) != 5 {
		t.Fatalf("expected 5 refs from loose parsing, got %d: %+v", len(refs), refs)
	}

	foundPlayer := false
	foundBoss := false
	foundBG := false
	for _, r := range refs {
		if strings.Contains(r.path, "valid_player_cjs") {
			foundPlayer = true
		}
		if strings.Contains(r.path, "valid_boss_cjs") {
			foundBoss = true
		}
		if strings.Contains(r.path, "common_014.jpg") {
			foundBG = true
		}
	}
	if !foundPlayer || !foundBoss || !foundBG {
		t.Errorf("missing expected extracted assets: player=%v boss=%v bg=%v", foundPlayer, foundBoss, foundBG)
	}
}

// 18. Loose parsing: scene_list containing non-object items and PHP numeric map
func TestGameDataPrefetch_ClearedQuest_LooseSceneList(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	// 18.1 Slice with corrupted items
	payloadSlice := `{
		"scene_list": [
			123,
			"corrupt",
			null,
			{"bg1": "/sp/quest/scene/bg/valid_1.jpg"},
			{"charcter1_big_image": "/sp/quest/scene/character/valid_2.png"}
		]
	}`

	candSlice := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/quest/cleared_quest_scenario/10001/1",
		data:       []byte(payloadSlice),
		isGameData: true,
		reqMethod:  http.MethodGet,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(candSlice)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs from loose slice, got %d: %+v", len(refs), refs)
	}

	// 18.2 PHP-style numeric object map {"0": {...}, "1": {...}}
	payloadMap := `{
		"scene_list": {
			"0": {"bg1": "/sp/quest/scene/bg/php_bg.jpg"},
			"1": {"charcter1_big_image": "/sp/quest/scene/character/php_char.png"}
		}
	}`

	candMap := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/quest/cleared_quest_scenario/10001/1",
		data:       []byte(payloadMap),
		isGameData: true,
		reqMethod:  http.MethodGet,
		langPrefix: "/assets_en",
	}

	refsMap := pe.extractGameDataRefs(candMap)
	if len(refsMap) != 2 {
		t.Fatalf("expected 2 refs from PHP map, got %d: %+v", len(refsMap), refsMap)
	}
	if refsMap[0].path != "/assets_en/img/sp/quest/scene/bg/php_bg.jpg" {
		t.Errorf("unexpected path: %s", refsMap[0].path)
	}
}

// 19. Background image: null or empty at index 0 scans to first valid image
func TestGameDataPrefetch_BackgroundImageObject_NullFirst(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"background_image_object": [
			null,
			"",
			12345,
			"/sp/raid/bg/second_is_valid.jpg",
			"/sp/raid/bg/third_must_not_be_used.jpg"
		]
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 1 {
		t.Fatalf("expected exactly 1 background ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].path != "/assets/img/sp/raid/bg/second_is_valid.jpg" {
		t.Errorf("expected second_is_valid.jpg, got %s", refs[0].path)
	}
}

// 20. Background image: PHP associative array with numeric key "0"
func TestGameDataPrefetch_BackgroundImageObject_NumericMap(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	payload := `{
		"background_image_object": {
			"0": "/sp/raid/bg/php_raid_bg.jpg"
		}
	}`

	cand := prefetchCandidate{
		host:       "prd-game-a-granbluefantasy.akamaized.net",
		path:       "/rest/multiraid/start.json",
		data:       []byte(payload),
		isGameData: true,
		reqMethod:  http.MethodPost,
		langPrefix: "/assets",
	}

	refs := pe.extractGameDataRefs(cand)
	if len(refs) != 1 {
		t.Fatalf("expected exactly 1 background ref, got %d: %+v", len(refs), refs)
	}
	if refs[0].path != "/assets/img/sp/raid/bg/php_raid_bg.jpg" {
		t.Errorf("expected php_raid_bg.jpg, got %s", refs[0].path)
	}
}

// 21. normalizeCDNImagePath edge cases
func TestGameDataPrefetch_NormalizeCDNImagePath_EdgeCases(t *testing.T) {
	tests := []struct {
		raw        string
		langPrefix string
		expected   string
	}{
		{"assets/img/sp/bg.png", "/assets", "/assets/img/sp/bg.png"},
		{"assets_en/img/sp/bg.png", "/assets_en", "/assets_en/img/sp/bg.png"},
		{"/assets/img/sp/bg.png", "/assets", "/assets/img/sp/bg.png"},
		{"/sp/raid/bg.jpg?version=123", "/assets", "/assets/img/sp/raid/bg.jpg"},
		{"sp/raid/bg.jpg", "/assets_en", "/assets_en/img/sp/raid/bg.jpg"},
		{"/sp/raid/bg.jpg", "", ""},          // Unknown language prefix must return empty
		{"/sp/raid/bg.jpg", "/unknown", ""}, // Invalid language prefix must return empty
	}

	for _, tc := range tests {
		actual := normalizeCDNImagePath(tc.raw, tc.langPrefix)
		if actual != tc.expected {
			t.Errorf("normalizeCDNImagePath(%q, %q) = %q, expected %q", tc.raw, tc.langPrefix, actual, tc.expected)
		}
	}
}

// 22. Trailing slash on cleared_quest_scenario is handled cleanly
func TestGameDataPrefetch_TrailingSlash(t *testing.T) {
	if !isClearedQuestScenarioPath("/rest/quest/cleared_quest_scenario/10001/1/") {
		t.Errorf("expected isClearedQuestScenarioPath to accept trailing slash")
	}
	if !isClearedQuestScenarioPath("/rest/quest/cleared_quest_scenario/10001/1") {
		t.Errorf("expected isClearedQuestScenarioPath to accept path without trailing slash")
	}
	if isClearedQuestScenarioPath("/rest/quest/cleared_quest_scenario/10001/1/extra") {
		t.Errorf("expected isClearedQuestScenarioPath to reject extra path segments")
	}
	if isClearedQuestScenarioPath("/rest/quest/cleared_quest_scenario/10001") {
		t.Errorf("expected isClearedQuestScenarioPath to reject missing second segment")
	}
}

// 23. Non-GBF domain requests are completely ignored
func TestGameDataPrefetch_NonGBFDomain_Skipped(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	req, _ := http.NewRequest(http.MethodPost, "https://api.github.com/rest/multiraid/start.json", nil)
	req.Header.Set("X-VERSION", "1790900582")
	body := []byte(`{"player":{"param":[{"cjs":"actor"}]}}`)

	pe.MaybeEnqueueGameData(req, "api.github.com", body)

	if len(pe.discoveryCh) != 0 {
		t.Errorf("expected discoveryCh to remain empty for non-GBF domain, got len %d", len(pe.discoveryCh))
	}
}

// 24. Nil req.URL does not panic
func TestGameDataPrefetch_NilURL_NoPanic(t *testing.T) {
	_, pe, cleanup := createTestProxyForGameData(t, true)
	defer cleanup()

	req := &http.Request{Method: http.MethodPost} // req.URL is nil
	body := []byte(`{"player":{"param":[{"cjs":"actor"}]}}`)

	// Must not panic
	pe.MaybeEnqueueGameData(req, "game.granbluefantasy.jp", body)
}
