package proxy

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestProxyRoutingRules(t *testing.T) {
	// 1. isGBFDomain
	gbfHosts := []string{
		"game.granbluefantasy.jp",
		"gbf.game.mbga.jp",
		"connect.mobage.jp",
		"prd-game-a-granbluefantasy.akamaized.net",
		"localhost",
		"127.0.0.1",
	}
	for _, h := range gbfHosts {
		if !isGBFDomain(h) {
			t.Errorf("host %s must be recognized as GBF domain", h)
		}
	}

	nonGBFHosts := []string{
		"example.com",
		"google.com",
		"twitter.com",
	}
	for _, h := range nonGBFHosts {
		if isGBFDomain(h) {
			t.Errorf("host %s must NOT be recognized as GBF domain", h)
		}
	}

	// 2. isStaticTarget
	staticTargets := []struct {
		host string
		path string
	}{
		{"prd-game-a-granbluefantasy.akamaized.net", "/assets/img/hero.png"},
		{"game.granbluefantasy.jp", "/assets/1772717316/js/app.js"},
		{"game.granbluefantasy.jp", "/assets_en/css/style.css"},
		{"game.granbluefantasy.jp", "/sound/bgm.mp3"},
	}
	for _, target := range staticTargets {
		if !isStaticTarget(target.host, target.path) {
			t.Errorf("expected static target for %s%s", target.host, target.path)
		}
	}

	dynamicTargets := []struct {
		host string
		path string
	}{
		{"game.granbluefantasy.jp", "/rest/multiraid/start.json"},
		{"game.granbluefantasy.jp", "/quest/stage_list"},
		{"game.granbluefantasy.jp", "/party/deck_info"},
		{"game.granbluefantasy.jp", "/user/status"},
		{"game.granbluefantasy.jp", "/ob/r"},
	}
	for _, target := range dynamicTargets {
		if isStaticTarget(target.host, target.path) {
			t.Errorf("dynamic API %s%s must NOT be treated as static", target.host, target.path)
		}
	}

	// 3. isRetryableAPI
	retryable := []string{
		"/rest/multiraid/condition.json",
		"/rest/quest/stage_list",
		"/rest/party/deck_info",
	}
	for _, p := range retryable {
		if !isRetryableAPI(p) {
			t.Errorf("path %s must be in retryable whitelist", p)
		}
	}

	nonRetryable := []string{
		"/rest/multiraid/start.json",
		"/rest/raid/ability_result.json",
		"/rest/user/profile",
		"/ob/r",
	}
	for _, p := range nonRetryable {
		if isRetryableAPI(p) {
			t.Errorf("path %s must NOT be in retryable whitelist", p)
		}
	}

	// 4. matchETag
	if !matchETag("\"abc\"", "\"abc\"") {
		t.Error("identical ETags must match")
	}
	if !matchETag("W/\"abc\"", "\"abc\"") {
		t.Error("weak ETag must match strong ETag with same value")
	}
	if !matchETag("\"abc\"", "W/\"abc\"") {
		t.Error("strong ETag must match weak ETag with same value")
	}
	if matchETag("\"abc\"", "\"def\"") {
		t.Error("different ETags must not match")
	}
	if !matchETag("*", "\"any-tag\"") {
		t.Error("wildcard ETag must match any server ETag")
	}
	if !matchETag("\"tag1\", \"tag2\"", "\"tag2\"") {
		t.Error("comma-separated ETags containing server ETag must match")
	}
	if !matchETag("W/\"tag1\", W/\"tag2\"", "\"tag2\"") {
		t.Error("comma-separated weak ETags containing server ETag must match")
	}
	if matchETag("\"tag1\", \"tag2\"", "\"tag3\"") {
		t.Error("comma-separated ETags not containing server ETag must not match")
	}

	// 5. isHopByHop
	hopHeaders := []string{"connection", "keep-alive", "transfer-encoding", "upgrade", "te"}
	for _, h := range hopHeaders {
		if !isHopByHop(h) {
			t.Errorf("header %s must be recognized as hop-by-hop", h)
		}
	}
	if isHopByHop("content-type") || isHopByHop("etag") || isHopByHop("cookie") {
		t.Error("standard headers must not be marked as hop-by-hop")
	}

	// 6. isPassthroughHost: ws.game.granbluefantasy.jp must be passthrough and NOT MITM'd
	if !isPassthroughHost("ws.game.granbluefantasy.jp") {
		t.Error("ws.game.granbluefantasy.jp must be recognized as passthrough host")
	}
	if isGBFDomain("ws.game.granbluefantasy.jp") {
		t.Error("ws.game.granbluefantasy.jp must NOT be in MITM isGBFDomain")
	}

	// 7. isTelemetryHost: ad & tracking domains must be blocked
	telemetryHosts := []string{"smbeat.jp", "smrtbeat.com", "datadoghq.com", "rcv.a-i-ad.com"}
	for _, th := range telemetryHosts {
		if !isTelemetryHost(th) {
			t.Errorf("%s must be detected as telemetry domain", th)
		}
	}
	if isTelemetryHost("game.granbluefantasy.jp") {
		t.Error("game.granbluefantasy.jp must NOT be marked as telemetry domain")
	}

	// 8. isGBFAkamaiHost & isGBFDomain: only legitimate GBF domains are MITM'd
	if !isGBFAkamaiHost("prd-game-a-granbluefantasy.akamaized.net") {
		t.Error("prd-game-a-granbluefantasy.akamaized.net must be recognized as GBF Akamai host")
	}
	if !isGBFAkamaiHost("prd-game-a-granbluefantasy.akamaized.net:443") {
		t.Error("Akamai host with port 443 must be recognized as GBF Akamai host")
	}
	if ns, isGBF := NormalizeAssetNamespace("prd-game-a-granbluefantasy.akamaized.net:80"); !isGBF || ns != "gbf" {
		t.Errorf("NormalizeAssetNamespace with port expected ('gbf', true), got (%q, %v)", ns, isGBF)
	}
	if !isGBFAkamaiHost("prd-game-a1-granbluefantasy-steam.akamaized.net") {
		t.Error("steam Akamai host must be recognized as GBF Akamai host")
	}
	if !isGBFAkamaiHost("prd-game-a-gbf.akamaized.net") {
		t.Error("Android SkyLeap Akamai host prd-game-a-gbf.akamaized.net must be recognized as GBF Akamai host")
	}
	if !isGBFAkamaiHost("prd-game-a-gbf.akamaized.net:443") {
		t.Error("Android SkyLeap Akamai host with port 443 must be recognized as GBF Akamai host")
	}
	if !isGBFAkamaiHost("prd-game-a1-gbf.akamaized.net") {
		t.Error("Android SkyLeap Akamai shard prd-game-a1-gbf.akamaized.net must be recognized as GBF Akamai host")
	}
	if ns, isGBF := NormalizeAssetNamespace("prd-game-a-gbf.akamaized.net:443"); !isGBF || ns != "gbf" {
		t.Errorf("NormalizeAssetNamespace for Android CDN expected ('gbf', true), got (%q, %v)", ns, isGBF)
	}
	if !isGBFDomain("prd-game-a-gbf.akamaized.net") {
		t.Error("prd-game-a-gbf.akamaized.net must be recognized as GBF domain")
	}
	if !isStaticTarget("prd-game-a-gbf.akamaized.net", "/assets_en/img/sp/ui/icon.png") {
		t.Error("asset on prd-game-a-gbf.akamaized.net must be recognized as static target")
	}
	if isGBFAkamaiHost("unrelated-tenant.akamaized.net") {
		t.Error("unrelated Akamai tenants must NOT be recognized as GBF Akamai host")
	}
	if isGBFDomain("fakegranbluefantasy.jp") {
		t.Error("fakegranbluefantasy.jp must NOT be recognized as GBF domain")
	}
	if !isGBFDomain("game.granbluefantasy.jp") {
		t.Error("game.granbluefantasy.jp must be recognized as GBF domain")
	}

	// 9. isStaticTarget vs dynamic APIs
	if !isStaticTarget("game.granbluefantasy.jp", "/sound/se/se_100.ogg") {
		t.Error("/sound/se/se_100.ogg must be recognized as static")
	}
	if !isStaticTarget("game.granbluefantasy.jp", "/img/sp/ui/btn.png") {
		t.Error("/img/sp/ui/btn.png must be recognized as static")
	}
	if isStaticTarget("game.granbluefantasy.jp", "/rest/multiraid/condition.json") {
		t.Error("/rest/... must NOT be recognized as static")
	}
	if isStaticTarget("game.granbluefantasy.jp", "/present/receive.json") {
		t.Error("/present/... must NOT be recognized as static")
	}
	if isStaticTarget("game.granbluefantasy.jp", "/guild/info.json") {
		t.Error("/guild/... must NOT be recognized as static")
	}
}

func TestPrefetchExtraction(t *testing.T) {
	pe := &PrefetchEngine{}
	sampleJS := `
		var img1 = "assets/img/sp/quest/scene/character/body/3040001000.png";
		var sound1 = '/sound/se/se_100.mp3';
		var cjs = "sp/cjs/npc_3040001000.png";
	`
	refs := pe.extractAssetRefs("/assets/js/bundle.js", []byte(sampleJS), "prd-game-a-granbluefantasy.akamaized.net")
	if len(refs) < 2 {
		t.Fatalf("expected at least 2 extracted refs, got %d", len(refs))
	}

	// Twin CreateJS deduction test
	twinManifestPath := "/assets/123456/js/model/manifest/npc_3040001000.js"
	twinRefs := pe.extractAssetRefs(twinManifestPath, []byte("{}"), "prd-game-a-granbluefantasy.akamaized.net")
	foundTwin := false
	for _, r := range twinRefs {
		if r[1] == "/assets/123456/js/cjs/npc_3040001000.js" {
			foundTwin = true
			break
		}
	}
	if !foundTwin {
		t.Error("expected twin CreateJS script to be deduced from model manifest")
	}

	// English asset prefix test
	enRefs := pe.extractAssetRefs("/assets_en/123456/js/manifest.js", []byte(`var s = "sp/cjs/tex.png";`), "prd-game-a-granbluefantasy.akamaized.net")
	foundEn := false
	for _, r := range enRefs {
		if r[1] == "/assets_en/img/sp/cjs/tex.png" {
			foundEn = true
			break
		}
	}
	if !foundEn {
		t.Errorf("expected /assets_en/img prefix for english assets, got: %v", enRefs)
	}

	prioJS := getPrefetchPriority("/assets/js/bundle.js")
	if prioJS != 1 {
		t.Errorf("expected prio 1 for JS, got %d", prioJS)
	}
	prioImg := getPrefetchPriority("/assets/img/hero.png")
	if prioImg != 2 {
		t.Errorf("expected prio 2 for PNG, got %d", prioImg)
	}
	prioSound := getPrefetchPriority("/sound/bgm.mp3")
	if prioSound != 3 {
		t.Errorf("expected prio 3 for MP3, got %d", prioSound)
	}
}

func TestProxyServerLifecycleRestart(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "proxy-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = 0 // random free port allocated by OS
		c.CacheDir = tmpDir
	})

	certMgr, err := cert.NewManager(filepath.Join(tmpDir, "certs"))
	if err != nil {
		t.Fatal(err)
	}

	cacheMgr := cache.NewManager(tmpDir, 16)
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	// A stopped proxy must terminate already-accepted client connections, not
	// merely stop accepting new connections. Otherwise HTTP Keep-Alive / CONNECT
	// sessions could continue forwarding traffic after the UI says "stopped".
	if err := srv.Start(); err != nil {
		t.Fatalf("start for active-connection stop test failed: %v", err)
	}
	clientConn, err := net.Dial("tcp", srv.ListenerAddr())
	if err != nil {
		t.Fatalf("failed to connect test client: %v", err)
	}
	defer clientConn.Close()

	deadline := time.Now().Add(1 * time.Second)
	for {
		srv.connMu.Lock()
		activeCount := len(srv.activeConns)
		srv.connMu.Unlock()
		if activeCount > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test client connection was never tracked as active")
		}
		time.Sleep(5 * time.Millisecond)
	}

	srv.Stop()
	clientConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	var readBuf [1]byte
	if _, readErr := clientConn.Read(readBuf[:]); readErr == nil {
		t.Fatal("expected client connection to be closed after proxy stop")
	}

	// Cycle 1: Start -> Stop
	if err := srv.Start(); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	if !srv.IsRunning() {
		t.Fatal("expected running true")
	}
	srv.Stop()
	if srv.IsRunning() {
		t.Fatal("expected running false after stop")
	}

	// Idempotent Stop (must not panic)
	srv.Stop()

	// Cycle 2: Restart -> Stop
	if err := srv.Start(); err != nil {
		t.Fatalf("second start failed: %v", err)
	}
	if !srv.IsRunning() {
		t.Fatal("expected running true on second start")
	}
	if srv.prefetch == nil || srv.prefetch.IsStopped() {
		t.Fatal("expected prefetch engine to be running after restart")
	}
	srv.Stop()
	if !srv.prefetch.IsStopped() {
		t.Fatal("expected prefetch engine to be stopped after stop")
	}
}

func TestPrefetchInflightRelease(t *testing.T) {
	srv := &ProxyServer{
		stats: telemetry.NewStats(),
	}
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	key := "test-host/assets/test.png"
	pe.inflightMu.Lock()
	pe.inflight[key] = struct{}{}
	pe.inflightMu.Unlock()

	pe.inflightMu.Lock()
	_, ok := pe.inflight[key]
	pe.inflightMu.Unlock()
	if !ok {
		t.Fatal("expected key in inflight")
	}

	pe.removeInflight(key)

	pe.inflightMu.Lock()
	_, ok = pe.inflight[key]
	pe.inflightMu.Unlock()
	if ok {
		t.Fatal("expected key to be removed from inflight")
	}
}

func TestWindowsPathValidation(t *testing.T) {
	validPaths := []string{
		"windows_skin.png",
		"/assets/img/windows_character.png",
		"/sound/windows_bgm.mp3",
		"c:\\windows_skin.png",
		"d:\\assets\\windows_theme.css",
	}
	for _, p := range validPaths {
		if isBlockedWindowsPath(p) {
			t.Errorf("path %q should NOT be blocked as windows system path", p)
		}
	}

	blockedPaths := []string{
		"/windows/system32/calc.exe",
		"\\windows\\win.ini",
		"windows/win.ini",
		"windows",
		"/windows",
		"\\windows",
		"c:\\windows",
		"c:\\windows\\system32",
		"c:/windows/system32",
		"/foo/windows/bar",
	}
	for _, p := range blockedPaths {
		if !isBlockedWindowsPath(p) {
			t.Errorf("path %q SHOULD be blocked as windows system path", p)
		}
	}
}

type dummyConn struct {
	net.Conn
	writeBuf bytes.Buffer
}

func (d *dummyConn) Write(b []byte) (int, error) {
	return d.writeBuf.Write(b)
}

func (d *dummyConn) Close() error {
	return nil
}

func (d *dummyConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8124}
}

func (d *dummyConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}
}

func TestHandlePlainHTTP_DirectLocalAndPAC(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	// 1. Direct local request for /ca.crt
	conn1 := &dummyConn{}
	req1 := &http.Request{
		Method: http.MethodGet,
		Host:   "127.0.0.1:8124",
		URL:    &url.URL{Path: "/ca.crt"},
	}
	srv.handlePlainHTTP(conn1, req1)
	if !strings.Contains(conn1.writeBuf.String(), "200 OK") || !strings.Contains(conn1.writeBuf.String(), "application/x-x509-ca-cert") {
		t.Errorf("expected 200 OK with ca cert for direct local request, got %s", conn1.writeBuf.String())
	}

	// 2. Proxied request for http://prd-game-a-granbluefantasy.akamaized.net/assets/ca.crt (URL.IsAbs() = true)
	// Must NOT be intercepted as direct local CA cert
	cacheMgr.SaveWithNamespace("gbf", "assets/ca.crt", map[string]string{"content-type": "text/plain"}, []byte("game-asset-bytes"))
	conn2 := &dummyConn{}
	req2 := &http.Request{
		Method: http.MethodGet,
		Host:   "prd-game-a-granbluefantasy.akamaized.net",
		URL:    &url.URL{Scheme: "http", Host: "prd-game-a-granbluefantasy.akamaized.net", Path: "/assets/ca.crt"},
	}
	srv.handlePlainHTTP(conn2, req2)
	// Should NOT return application/x-x509-ca-cert
	if strings.Contains(conn2.writeBuf.String(), "application/x-x509-ca-cert") || strings.Contains(conn2.writeBuf.String(), "gbf_ca.crt") {
		t.Error("external proxied request for /assets/ca.crt must NOT return proxy root CA cert")
	}
	if !strings.Contains(conn2.writeBuf.String(), "game-asset-bytes") {
		t.Errorf("expected cached game asset, got %s", conn2.writeBuf.String())
	}

	// 3. An external host must not be able to claim internal proxy endpoints.
	// Use a loopback HTTP proxy so this regression test remains fully local.
	upstreamProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("upstream-resource"))
	}))
	defer upstreamProxy.Close()
	cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = true
		c.UpstreamProxy = upstreamProxy.URL
	})
	connExternal := &dummyConn{}
	reqExternal := &http.Request{
		Method: http.MethodGet,
		Host:   "example.com:8124",
		URL:    &url.URL{Scheme: "http", Host: "example.com:8124", Path: "/ca.crt"},
	}
	srv.handlePlainHTTP(connExternal, reqExternal)
	if strings.Contains(connExternal.writeBuf.String(), "application/x-x509-ca-cert") ||
		strings.Contains(connExternal.writeBuf.String(), "gbf_ca.crt") {
		t.Fatal("external /ca.crt must never return the proxy's root CA")
	}
	if !strings.Contains(connExternal.writeBuf.String(), "upstream-resource") {
		t.Fatalf("external /ca.crt should be forwarded, got %s", connExternal.writeBuf.String())
	}

	// 4. PAC extraction with port omitted
	cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = true
	})
	targetPAC := "127.0.0.1"
	if lan := config.GetLANIP(); lan != "" {
		targetPAC = lan
	}
	conn3 := &dummyConn{}
	req3 := &http.Request{
		Method: http.MethodGet,
		Host:   targetPAC, // Port omitted!
		URL:    &url.URL{Path: "/proxy.pac"},
	}
	srv.handlePlainHTTP(conn3, req3)
	expectedAddr := targetPAC + ":8124"
	if !strings.Contains(conn3.writeBuf.String(), expectedAddr) {
		t.Errorf("expected PAC script to contain host %s, got %s", expectedAddr, conn3.writeBuf.String())
	}

	// 5. Direct local IPv6 request for /ca.crt with Host: [::1] (port omitted)
	conn4 := &dummyConn{}
	req4 := &http.Request{
		Method: http.MethodGet,
		Host:   "[::1]",
		URL:    &url.URL{Path: "/ca.crt"},
	}
	srv.handlePlainHTTP(conn4, req4)
	if !strings.Contains(conn4.writeBuf.String(), "200 OK") || !strings.Contains(conn4.writeBuf.String(), "application/x-x509-ca-cert") {
		t.Errorf("expected 200 OK with ca cert for IPv6 [::1] direct local request, got %s", conn4.writeBuf.String())
	}
}

func TestConditionalAsset304AndCacheControl(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, _ := cert.NewManager(filepath.Join(tempDir, "certs"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	// Mock upstream HTTPS server
	upstreamHitCount := 0
	upstreamReceivedConditional := false
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHitCount++
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			upstreamReceivedConditional = true
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", "\"cdn-etag-999\"")
		w.Header().Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	srv.assetClient.Store(ts.Client())

	// Client sends conditional request (If-None-Match matching CDN ETag) on cache miss
	req, _ := http.NewRequest(http.MethodGet, "https://"+u.Host+"/assets/test_conditional.png", nil)
	req.Header.Set("If-None-Match", "\"cdn-etag-999\"")

	var buf bytes.Buffer
	srv.handleStaticAsset(&buf, req, u.Host)

	// 1. Upstream must NOT have received conditional headers
	if upstreamReceivedConditional {
		t.Error("expected upstream request to have client conditional headers stripped")
	}

	// 2. Client response must be 304 Not Modified
	respStr := buf.String()
	if !strings.HasPrefix(respStr, "HTTP/1.1 304 Not Modified") {
		t.Fatalf("expected HTTP/1.1 304 Not Modified, got: %s", respStr)
	}

	// 3. Response must include Cache-Control per RFC 7234
	if !strings.Contains(respStr, "Cache-Control:") {
		t.Errorf("expected 304 response to include Cache-Control header per RFC 7234, got: %s", respStr)
	}
	if !strings.Contains(respStr, "ETag: \"cdn-etag-999\"") {
		t.Errorf("expected 304 response to include ETag, got: %s", respStr)
	}

	// 4. Client sends standard unconditional GET: must return 200 OK with ETag and Last-Modified
	req2, _ := http.NewRequest(http.MethodGet, "https://"+u.Host+"/assets/test_conditional.png", nil)
	var buf2 bytes.Buffer
	srv.handleStaticAsset(&buf2, req2, u.Host)
	resp2Str := buf2.String()
	if !strings.HasPrefix(resp2Str, "HTTP/1.1 200 OK") {
		t.Fatalf("expected HTTP/1.1 200 OK, got: %s", resp2Str)
	}
	if !strings.Contains(resp2Str, "Last-Modified: Wed, 21 Oct 2026 07:28:00 GMT") {
		t.Errorf("expected 200 OK response to include Last-Modified, got: %s", resp2Str)
	}
}

func TestHandleStaticAsset_SingleFlightResilience(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, _ := cert.NewManager(filepath.Join(tempDir, "certs"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond) // Ensure flight is in progress
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	srv.assetClient.Store(ts.Client())

	var wg sync.WaitGroup
	wg.Add(2)

	// Client 1: Context cancels early after 10ms
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel1()
	req1, _ := http.NewRequestWithContext(ctx1, http.MethodGet, "https://"+u.Host+"/assets/shared_flight.png", nil)
	var buf1 bytes.Buffer

	// Client 2: Context remains active
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://"+u.Host+"/assets/shared_flight.png", nil)
	var buf2 bytes.Buffer

	go func() {
		defer wg.Done()
		srv.handleStaticAsset(&buf1, req1, u.Host)
	}()

	// Stagger slightly so Client 1 starts the flight
	time.Sleep(5 * time.Millisecond)

	go func() {
		defer wg.Done()
		srv.handleStaticAsset(&buf2, req2, u.Host)
	}()

	wg.Wait()

	// Client 2 MUST receive 200 OK, never 502 Bad Gateway
	resp2Str := buf2.String()
	if !strings.HasPrefix(resp2Str, "HTTP/1.1 200 OK") {
		t.Fatalf("expected Client 2 to receive HTTP/1.1 200 OK despite Client 1 context abort, got: %s", resp2Str)
	}
}

func TestHandleStaticAsset_FollowerContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, _ := cert.NewManager(filepath.Join(tempDir, "certs"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond) // Slow upstream
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nfollower_cancel_test"))
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	srv.assetClient.Store(ts.Client())

	var wg sync.WaitGroup
	wg.Add(2)

	// Client 1 (Leader): Context remains active
	req1, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://"+u.Host+"/assets/follower_cancel.png", nil)
	var buf1 bytes.Buffer

	// Client 2 (Follower): Context cancels after 15ms
	ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel2()
	req2, _ := http.NewRequestWithContext(ctx2, http.MethodGet, "https://"+u.Host+"/assets/follower_cancel.png", nil)
	var buf2 bytes.Buffer

	go func() {
		defer wg.Done()
		srv.handleStaticAsset(&buf1, req1, u.Host)
	}()

	// Stagger slightly so Client 1 is definitely the leader
	time.Sleep(5 * time.Millisecond)

	followerDone := make(chan struct{})
	go func() {
		defer wg.Done()
		srv.handleStaticAsset(&buf2, req2, u.Host)
		close(followerDone)
	}()

	// Follower must unblock within ~35ms, long before the 80ms upstream finishes
	select {
	case <-followerDone:
		// Follower unblocked promptly on context cancellation
	case <-time.After(50 * time.Millisecond):
		t.Fatal("follower did not unblock on context cancellation, blocked on singleflight leader")
	}

	wg.Wait()

	// Client 1 MUST still succeed with 200 OK
	resp1Str := buf1.String()
	if !strings.HasPrefix(resp1Str, "HTTP/1.1 200 OK") {
		t.Fatalf("expected Client 1 to receive HTTP/1.1 200 OK, got: %s", resp1Str)
	}
}

func TestDynamicAPI_ClientContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, _ := cert.NewManager(filepath.Join(tempDir, "certs"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	upstreamGotCancel := make(chan bool, 1)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			upstreamGotCancel <- true
		case <-time.After(300 * time.Millisecond):
			upstreamGotCancel <- false
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	srv.apiClient.Store(ts.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+u.Host+"/rest/multiraid/condition.json", nil)
	var buf bytes.Buffer
	srv.handleDynamicAPI(&buf, req, u.Host)

	select {
	case cancelled := <-upstreamGotCancel:
		if !cancelled {
			t.Error("expected upstream request to receive context cancellation from client")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for upstream handler to observe context cancellation")
	}
}

func TestAndroidSkyLeapCDN_Matching(t *testing.T) {
	testHosts := []string{
		"prd-game-a-gbf.akamaized.net",
		"prd-game-a-gbf.akamaized.net:443",
		"prd-game-a1-gbf.akamaized.net",
		"prd-game-a2-gbf.akamaized.net",
		"prd-game-a3-gbf.akamaized.net",
		"prd-game-a4-gbf.akamaized.net",
		"prd-game-a5-gbf.akamaized.net",
	}

	for _, host := range testHosts {
		if !isGBFAkamaiHost(host) {
			t.Errorf("host %q must be recognized by isGBFAkamaiHost", host)
		}
		if !isGBFDomain(host) {
			t.Errorf("host %q must be recognized by isGBFDomain", host)
		}
		ns, isGBF := NormalizeAssetNamespace(host)
		if !isGBF || ns != "gbf" {
			t.Errorf("host %q expected NormalizeAssetNamespace ('gbf', true), got (%q, %v)", host, ns, isGBF)
		}
	}

	staticPaths := []string{
		"/assets_en/img/sp/ui/icon.png",
		"/assets_en/css/common.css",
		"/assets_en/js/bundle.js",
		"/sound/se/se_100.ogg",
		"/assets/font/font.woff2",
	}

	for _, path := range staticPaths {
		if !isStaticTarget("prd-game-a-gbf.akamaized.net", path) {
			t.Errorf("static path %q on prd-game-a-gbf.akamaized.net must be recognized as static target", path)
		}
	}

	dynamicPaths := []string{
		"/rest/user/status",
		"/quest/index",
		"/party/deck",
		"/ob/r",
	}

	for _, path := range dynamicPaths {
		if isStaticTarget("prd-game-a-gbf.akamaized.net", path) {
			t.Errorf("dynamic path %q on prd-game-a-gbf.akamaized.net must NOT be recognized as static target", path)
		}
	}
}

func TestProxyRedirectTransparency(t *testing.T) {
	tmpDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tmpDir, "config.json"))
	cfgMgr.Update(func(c *config.Config) {
		c.DirectMode = true
		c.VerifyUpstreamTLS = false
		c.CacheDir = tmpDir
	})

	certMgr, err := cert.NewManager(filepath.Join(tmpDir, "certs"))
	if err != nil {
		t.Fatalf("failed to init cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tmpDir, 16)
	stats := telemetry.NewStats()
	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	activeCfg := cfgMgr.Get()
	srv.updateClients(&activeCfg)

	// Upstream TLS server that returns 302 Found redirect
	redirectCount := 0
	targetHit := false
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			redirectCount++
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		if r.URL.Path == "/target" {
			targetHit = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("target page"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	u, _ := url.Parse(ts.URL)

	req, _ := http.NewRequest(http.MethodGet, "https://"+u.Host+"/redirect", nil)
	var buf bytes.Buffer
	srv.handleDynamicAPI(&buf, req, u.Host)

	// Verify that the proxy returned 302 Found directly to the client
	respStr := buf.String()
	if !strings.Contains(respStr, "302 Found") && !strings.Contains(respStr, "HTTP/1.1 302") {
		t.Errorf("expected proxy to return 302 Found directly, got: %s", respStr)
	}
	if !strings.Contains(respStr, "Location: /target") && !strings.Contains(respStr, "Location: "+ts.URL+"/target") {
		t.Errorf("expected proxy to preserve Location header in 302 response, got: %s", respStr)
	}
	if targetHit {
		t.Error("apiClient must NOT auto-follow redirect to /target; it must be returned as-is to the client")
	}
}


