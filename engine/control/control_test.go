package control

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/proxy"
	"gbf-proxy/telemetry"
)

func TestAllowedOrigin(t *testing.T) {
	allowed := []string{
		"",
		"http://localhost",
		"http://localhost:8125",
		"http://127.0.0.1",
		"http://127.0.0.1:8125",
		"http://[::1]:8125",
	}
	for _, o := range allowed {
		if !isAllowedOrigin(o) {
			t.Errorf("origin %q should be allowed", o)
		}
	}

	disallowed := []string{
		"http://evil.com",
		"http://attacker.org:8125",
		"http://localhost.evil.com",
		"http://127.0.0.1.attacker.com",
		"://invalid-url",
	}
	for _, o := range disallowed {
		if isAllowedOrigin(o) {
			t.Errorf("origin %q should be blocked", o)
		}
	}
}

func TestControlCORSProtection(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	// 1. Request with evil Origin must be blocked with 403 Forbidden
	reqEvil := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	reqEvil.Host = "127.0.0.1:8125"
	reqEvil.Header.Set("Origin", "http://evil.com")
	wEvil := httptest.NewRecorder()
	ctrl.handleRoute(wEvil, reqEvil)

	if wEvil.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for evil origin, got %d", wEvil.Code)
	}

	// 2. Request with local Origin must be accepted with 200 OK and matching Access-Control-Allow-Origin
	reqLocal := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	reqLocal.Host = "127.0.0.1:8125"
	reqLocal.Header.Set("Origin", "http://127.0.0.1:8125")
	wLocal := httptest.NewRecorder()
	ctrl.handleRoute(wLocal, reqLocal)

	if wLocal.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for local origin, got %d", wLocal.Code)
	}
	if wLocal.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8125" {
		t.Fatalf("expected Access-Control-Allow-Origin to be reflected, got %q", wLocal.Header().Get("Access-Control-Allow-Origin"))
	}

	// 3. Request without Origin (e.g. desktop CLI, curl) must succeed
	reqDirect := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	reqDirect.Host = "127.0.0.1:8125"
	wDirect := httptest.NewRecorder()
	ctrl.handleRoute(wDirect, reqDirect)

	if wDirect.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for direct request, got %d", wDirect.Code)
	}
}

func TestControlServerLifecycleRestart(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	// Bind to port 0 for automatic available port
	cfgMgr.Update(func(c *config.Config) {
		c.ControlPort = 0
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	// First lifecycle
	if err := ctrl.Start(); err != nil {
		t.Fatalf("first start failed: %v", err)
	}
	ctrl.Stop()

	// Second lifecycle: must restart cleanly without panic
	if err := ctrl.Start(); err != nil {
		t.Fatalf("second start after stop failed: %v", err)
	}
	ctrl.Stop()
}

func TestControlDNSRebindingProtection(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	// Blocked hosts (DNS rebinding attacks)
	blockedHosts := []string{
		"evil.com",
		"attacker.org:8125",
		"localhost.evil.com",
		"127.0.0.1.attacker.com",
		"example.com",
		"example.com:8125",
		"192.168.1.99:8125", // LAN disabled by default
	}
	for _, h := range blockedHosts {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = h
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("host %q should be rejected with 403 Forbidden, got %d", h, w.Code)
		}
	}

	// Allowed hosts (loopback)
	allowedHosts := []string{
		"127.0.0.1",
		"127.0.0.1:8125",
		"localhost",
		"localhost:8125",
		"[::1]:8125",
	}
	for _, h := range allowedHosts {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = h
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("host %q should be allowed with 200 OK, got %d", h, w.Code)
		}
	}

	// Test AllowLAN
	cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = true
	})
	lanIP := config.GetLANIP()
	if lanIP != "" {
		req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
		req.Host = lanIP + ":8125"
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("LAN IP %q should be allowed when AllowLAN is true, got %d", req.Host, w.Code)
		}
	}
}

func TestControlApplyConfigAllFields(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	patchJSON := `{
		"control_port": 8130,
		"api_max_connections": 20,
		"api_max_keepalive": 8,
		"api_keepalive_expiry": 45.0,
		"asset_max_connections": 32,
		"asset_max_keepalive": 16,
		"asset_keepalive_expiry": 75.0,
		"ram_warmup_max_items": 2000,
		"enable_api_telemetry": false,
		"clean_zombies": false
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/config/apply", strings.NewReader(patchJSON))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	cfg := cfgMgr.Get()
	if cfg.ControlPort != 8130 {
		t.Errorf("expected ControlPort 8130, got %d", cfg.ControlPort)
	}
	if cfg.APIMaxConnections != 20 {
		t.Errorf("expected APIMaxConnections 20, got %d", cfg.APIMaxConnections)
	}
	if cfg.APIMaxKeepalive != 8 {
		t.Errorf("expected APIMaxKeepalive 8, got %d", cfg.APIMaxKeepalive)
	}
	if cfg.APIKeepaliveExpiry != 45.0 {
		t.Errorf("expected APIKeepaliveExpiry 45.0, got %f", cfg.APIKeepaliveExpiry)
	}
	if cfg.AssetMaxConnections != 32 {
		t.Errorf("expected AssetMaxConnections 32, got %d", cfg.AssetMaxConnections)
	}
	if cfg.AssetMaxKeepalive != 16 {
		t.Errorf("expected AssetMaxKeepalive 16, got %d", cfg.AssetMaxKeepalive)
	}
	if cfg.AssetKeepaliveExpiry != 75.0 {
		t.Errorf("expected AssetKeepaliveExpiry 75.0, got %f", cfg.AssetKeepaliveExpiry)
	}
	if cfg.RAMWarmupMaxItems != 2000 {
		t.Errorf("expected RAMWarmupMaxItems 2000, got %d", cfg.RAMWarmupMaxItems)
	}
	if cfg.EnableAPITelemetry != false {
		t.Errorf("expected EnableAPITelemetry false, got true")
	}
	if cfg.CleanZombies != false {
		t.Errorf("expected CleanZombies false, got true")
	}
}

func TestNewControlAPIs(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	testRoute := func(method, path string, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Host = "127.0.0.1:8125"
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, r)
		return w
	}

	// 1. /api/upstream/detect
	wUp := testRoute(http.MethodGet, "/api/upstream/detect", "")
	if wUp.Code != http.StatusOK {
		t.Errorf("/api/upstream/detect expected 200, got %d", wUp.Code)
	}

	// 2. /api/cache/detect-acgpower
	wAcg := testRoute(http.MethodGet, "/api/cache/detect-acgpower", "")
	if wAcg.Code != http.StatusOK {
		t.Errorf("/api/cache/detect-acgpower expected 200, got %d", wAcg.Code)
	}

	// 3. /api/update/check and /api/updater/check alias
	wUpd := testRoute(http.MethodGet, "/api/update/check", "")
	if wUpd.Code != http.StatusOK {
		t.Errorf("/api/update/check expected 200, got %d", wUpd.Code)
	}
	wUpdater := testRoute(http.MethodGet, "/api/updater/check", "")
	if wUpdater.Code != http.StatusOK {
		t.Errorf("/api/updater/check expected 200, got %d", wUpdater.Code)
	}

	// 4. /api/update/download-status and /api/updater/download-status alias
	wUpdStat := testRoute(http.MethodGet, "/api/update/download-status", "")
	if wUpdStat.Code != http.StatusOK {
		t.Errorf("/api/update/download-status expected 200, got %d", wUpdStat.Code)
	}
	wUpdaterStat := testRoute(http.MethodGet, "/api/updater/download-status", "")
	if wUpdaterStat.Code != http.StatusOK {
		t.Errorf("/api/updater/download-status expected 200, got %d", wUpdaterStat.Code)
	}

	// 5. /api/cert/status
	wCert := testRoute(http.MethodGet, "/api/cert/status", "")
	if wCert.Code != http.StatusOK {
		t.Errorf("/api/cert/status expected 200, got %d", wCert.Code)
	}

	// 6. /api/startup/status
	wStart := testRoute(http.MethodGet, "/api/startup/status", "")
	if wStart.Code != http.StatusOK {
		t.Errorf("/api/startup/status expected 200, got %d", wStart.Code)
	}

	// 7. /api/cache/task-status
	wTask := testRoute(http.MethodGet, "/api/cache/task-status", "")
	if wTask.Code != http.StatusOK {
		t.Errorf("/api/cache/task-status expected 200, got %d", wTask.Code)
	}

	// 8. Mutual exclusion: while auditing is active, slimming must return 409 Conflict
	ctrl.cacheTaskMu.Lock()
	ctrl.isAuditing = true
	ctrl.cacheTaskMu.Unlock()

	wSlimConflict := testRoute(http.MethodPost, "/api/cache/slim", "")
	if wSlimConflict.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for slim while auditing, got %d", wSlimConflict.Code)
	}

	ctrl.cacheTaskMu.Lock()
	ctrl.isAuditing = false
	ctrl.isSlimming = true
	ctrl.cacheTaskMu.Unlock()

	wAuditConflict := testRoute(http.MethodPost, "/api/cache/audit", "")
	if wAuditConflict.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict for audit while slimming, got %d", wAuditConflict.Code)
	}

	ctrl.cacheTaskMu.Lock()
	ctrl.isSlimming = false
	ctrl.cacheTaskMu.Unlock()

	// 9. Cancel task
	wCancel := testRoute(http.MethodPost, "/api/cache/cancel-task", "")
	if wCancel.Code != http.StatusOK {
		t.Errorf("/api/cache/cancel-task expected 200, got %d", wCancel.Code)
	}
}

// getTelemetrySummary must report REAL observed connection reuse, protocol mix and
// latency percentiles, not hardcoded placeholder values.
func TestGetTelemetrySummaryRealData(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	// Seed real observations: 3 reused + 1 new conn => 75% reuse; protocol mix 3x H2, 1x H1.
	stats.RecordConnReuse(true)
	stats.RecordConnReuse(true)
	stats.RecordConnReuse(true)
	stats.RecordConnReuse(false)
	stats.RecordProtocol("HTTP/2.0")
	stats.RecordProtocol("HTTP/2.0")
	stats.RecordProtocol("HTTP/2.0")
	stats.RecordProtocol("HTTP/1.1")
	for i := 1; i <= 10; i++ {
		stats.RecordLatency(float64(i * 10)) // 10ms..100ms
	}

	sum := ctrl.getTelemetrySummary()

	if got := sum["reused_connections"].(int64); got != 3 {
		t.Errorf("expected 3 reused connections, got %v", got)
	}
	if got := sum["new_connections"].(int64); got != 1 {
		t.Errorf("expected 1 new connection, got %v", got)
	}
	if got := sum["reuse_rate_percent"].(float64); got != 75.0 {
		t.Errorf("expected reuse_rate_percent 75.0, got %v", got)
	}

	protos := sum["protocols"].(map[string]int64)
	if protos["HTTP/2"] != 3 || protos["HTTP/1.1"] != 1 {
		t.Errorf("expected protocols H2=3 H1=1, got %v", protos)
	}

	pct := sum["percentiles"].(map[string]interface{})
	if pct["samples"].(int) != 10 {
		t.Errorf("expected 10 latency samples, got %v", pct["samples"])
	}
	if pct["max_ms"].(float64) != 100.0 {
		t.Errorf("expected max_ms 100.0, got %v", pct["max_ms"])
	}
	if pct["min_ms"].(float64) != 10.0 {
		t.Errorf("expected min_ms 10.0, got %v", pct["min_ms"])
	}
}

func TestControlProxyStartFailureAndSuccess(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)

	// Pick a free port and keep it occupied to force proxy listen failure
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	defer l.Close()

	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = port
	})

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	proxySrv := proxy.NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	defer proxySrv.Stop()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, proxySrv, stats)

	// 1. Calling /api/proxy/start when port is occupied should fail with 500
	req := httptest.NewRequest(http.MethodPost, "/api/proxy/start", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500 when proxy cannot bind port, got %d", w.Code)
	}

	var failResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &failResp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if failResp["ok"] != false {
		t.Errorf("expected ok=false in error response, got %v", failResp["ok"])
	}
	if errMsg, ok := failResp["error"].(string); !ok || errMsg == "" {
		t.Errorf("expected non-empty error message, got %v", failResp["error"])
	}

	// 2. Free the port and try again -> should succeed with 200
	_ = l.Close()

	wSuccess := httptest.NewRecorder()
	ctrl.handleRoute(wSuccess, req)

	if wSuccess.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 when proxy starts successfully, got %d", wSuccess.Code)
	}

	var okResp map[string]interface{}
	if err := json.Unmarshal(wSuccess.Body.Bytes(), &okResp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if okResp["ok"] != true {
		t.Errorf("expected ok=true, got %v", okResp["ok"])
	}
}

func TestControlProxyStartNilProxyServer(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// ControlServer with nil ProxyServer
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	req := httptest.NewRequest(http.MethodPost, "/api/proxy/start", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500 when proxy server is nil, got %d", w.Code)
	}

	var failResp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &failResp); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if failResp["ok"] != false {
		t.Errorf("expected ok=false in error response, got %v", failResp["ok"])
	}
}

func TestControlServer_ReloadListener_InFlightRequestCompletes(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// Pick two free ports
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick port 1: %v", err)
	}
	port1 := l1.Addr().(*net.TCPAddr).Port
	_ = l1.Close()

	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick port 2: %v", err)
	}
	port2 := l2.Addr().(*net.TCPAddr).Port
	_ = l2.Close()

	cfgMgr.Update(func(c *config.Config) {
		c.ControlPort = port1
	})

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("failed to start control server on port %d: %v", port1, err)
	}
	defer ctrl.Stop()

	// Send HTTP POST to port1 requesting migration to port2
	applyURL := fmt.Sprintf("http://127.0.0.1:%d/api/config/apply", port1)
	body := fmt.Sprintf(`{"control_port": %d}`, port2)
	req, err := http.NewRequest(http.MethodPost, applyURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("in-flight request failed during reload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}

	var resData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}

	expectedURL := fmt.Sprintf("http://127.0.0.1:%d", port2)
	if resData["control_url"] != expectedURL {
		t.Errorf("expected control_url=%q, got %v", expectedURL, resData["control_url"])
	}

	// Verify new port2 is responsive
	statusURL := fmt.Sprintf("http://127.0.0.1:%d/api/status", port2)
	sResp, err := http.Get(statusURL)
	if err != nil {
		t.Fatalf("failed to reach new control port %d: %v", port2, err)
	}
	sResp.Body.Close()
	if sResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on new port, got %d", sResp.StatusCode)
	}
}

func TestControlServer_ApplyConfig_ProxySuccess_ControlFail_Rollback(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgFile)

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// Pick 4 ports:
	// proxyPort1: initial proxy port
	// proxyPort2: target proxy port
	// ctrlPort1: initial control port
	// ctrlPort2: target control port (occupied by dummy listener to force Control reload failure)
	lP1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick proxyPort1: %v", err)
	}
	proxyPort1 := lP1.Addr().(*net.TCPAddr).Port
	_ = lP1.Close()

	lP2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick proxyPort2: %v", err)
	}
	proxyPort2 := lP2.Addr().(*net.TCPAddr).Port
	_ = lP2.Close()

	lC1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to pick ctrlPort1: %v", err)
	}
	ctrlPort1 := lC1.Addr().(*net.TCPAddr).Port
	_ = lC1.Close()

	// Keep lC2 open so ctrlPort2 is OCCUPIED
	lC2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind dummy listener on ctrlPort2: %v", err)
	}
	defer lC2.Close()
	ctrlPort2 := lC2.Addr().(*net.TCPAddr).Port

	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = proxyPort1
		c.ControlPort = ctrlPort1
	})

	proxySrv := proxy.NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}
	defer proxySrv.Stop()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, proxySrv, stats)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("failed to start control server: %v", err)
	}
	defer ctrl.Stop()

	// Verify initial proxy connectivity on proxyPort1
	pConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort1), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to connect to initial proxy port %d: %v", proxyPort1, err)
	}
	_ = pConn.Close()

	// Send POST /api/config/apply requesting proxyPort2 (succeeds) and ctrlPort2 (fails)
	applyURL := fmt.Sprintf("http://127.0.0.1:%d/api/config/apply", ctrlPort1)
	body := fmt.Sprintf(`{"listen_port": %d, "control_port": %d}`, proxyPort2, ctrlPort2)
	req, err := http.NewRequest(http.MethodPost, applyURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to apply config failed: %v", err)
	}
	defer resp.Body.Close()

	// Expect HTTP 500 Internal Server Error due to control rebind failure
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500, got %d", resp.StatusCode)
	}

	var resData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if errMsg, _ := resData["error"].(string); !strings.Contains(errMsg, "控制端口重载失败") {
		t.Errorf("expected error message to contain '控制端口重载失败', got %q", errMsg)
	}

	// Verify proxy rolled back to proxyPort1
	pConnRollback, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort1), 500*time.Millisecond)
	if err != nil {
		t.Errorf("proxy failed to rollback to original port %d: %v", proxyPort1, err)
	} else {
		_ = pConnRollback.Close()
	}

	// Verify proxy is NOT listening on proxyPort2
	pConnP2, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort2), 100*time.Millisecond)
	if err == nil {
		_ = pConnP2.Close()
		t.Errorf("proxy is unexpectedly still listening on port %d after rollback", proxyPort2)
	}

	// Verify control server is still running on ctrlPort1
	statusURL := fmt.Sprintf("http://127.0.0.1:%d/api/status", ctrlPort1)
	sResp, err := http.Get(statusURL)
	if err != nil {
		t.Fatalf("control server on original port %d unreachable after rollback: %v", ctrlPort1, err)
	}
	sResp.Body.Close()
	if sResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on original control port, got %d", sResp.StatusCode)
	}

	// Verify configuration was NOT committed
	currentCfg := cfgMgr.Get()
	if currentCfg.ListenPort != proxyPort1 {
		t.Errorf("expected ListenPort to remain %d, got %d", proxyPort1, currentCfg.ListenPort)
	}
	if currentCfg.ControlPort != ctrlPort1 {
		t.Errorf("expected ControlPort to remain %d, got %d", ctrlPort1, currentCfg.ControlPort)
	}
}

func TestControlServer_ApplyConfig_SaveFail_RollbackBoth(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "config_sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create config subdir: %v", err)
	}
	cfgFile := filepath.Join(subDir, "config.json")
	cfgMgr := config.NewManager(cfgFile)

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// Pick 4 free ports
	lP1, _ := net.Listen("tcp", "127.0.0.1:0")
	proxyPort1 := lP1.Addr().(*net.TCPAddr).Port
	_ = lP1.Close()

	lP2, _ := net.Listen("tcp", "127.0.0.1:0")
	proxyPort2 := lP2.Addr().(*net.TCPAddr).Port
	_ = lP2.Close()

	lC1, _ := net.Listen("tcp", "127.0.0.1:0")
	ctrlPort1 := lC1.Addr().(*net.TCPAddr).Port
	_ = lC1.Close()

	lC2, _ := net.Listen("tcp", "127.0.0.1:0")
	ctrlPort2 := lC2.Addr().(*net.TCPAddr).Port
	_ = lC2.Close()

	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = proxyPort1
		c.ControlPort = ctrlPort1
	})

	proxySrv := proxy.NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}
	defer proxySrv.Stop()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, proxySrv, stats)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("failed to start control server: %v", err)
	}
	defer ctrl.Stop()

	// Make Save() fail by replacing subDir directory with a plain regular file
	if err := os.RemoveAll(subDir); err != nil {
		t.Fatalf("failed to remove config subdir: %v", err)
	}
	if err := os.WriteFile(subDir, []byte("blocker_file"), 0644); err != nil {
		t.Fatalf("failed to write blocker file: %v", err)
	}

	// Send POST /api/config/apply requesting both ports to change
	applyURL := fmt.Sprintf("http://127.0.0.1:%d/api/config/apply", ctrlPort1)
	body := fmt.Sprintf(`{"listen_port": %d, "control_port": %d}`, proxyPort2, ctrlPort2)
	req, err := http.NewRequest(http.MethodPost, applyURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to apply config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500 due to save failure, got %d", resp.StatusCode)
	}

	var resData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if errMsg, _ := resData["error"].(string); !strings.Contains(errMsg, "配置持久化失败") {
		t.Errorf("expected error message to contain '配置持久化失败', got %q", errMsg)
	}

	// Verify proxy rolled back to proxyPort1
	pConn1, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort1), 500*time.Millisecond)
	if err != nil {
		t.Errorf("proxy failed to rollback to proxyPort1 %d: %v", proxyPort1, err)
	} else {
		_ = pConn1.Close()
	}

	// Verify proxy is NOT listening on proxyPort2
	pConn2, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort2), 100*time.Millisecond)
	if err == nil {
		_ = pConn2.Close()
		t.Errorf("proxy should NOT be listening on proxyPort2 after rollback")
	}

	// Verify control rolled back to ctrlPort1
	statusURL := fmt.Sprintf("http://127.0.0.1:%d/api/status", ctrlPort1)
	sResp, err := http.Get(statusURL)
	if err != nil {
		t.Fatalf("control server unreachable on rolled-back port %d: %v", ctrlPort1, err)
	}
	sResp.Body.Close()
	if sResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on rolled-back control port, got %d", sResp.StatusCode)
	}

	// Verify ctrlPort2 is NOT listening
	cConn2, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ctrlPort2), 100*time.Millisecond)
	if err == nil {
		_ = cConn2.Close()
		t.Errorf("control should NOT be listening on ctrlPort2 after rollback")
	}

	// Verify memory config rolled back to old values
	currentCfg := cfgMgr.Get()
	if currentCfg.ListenPort != proxyPort1 {
		t.Errorf("expected ListenPort %d, got %d", proxyPort1, currentCfg.ListenPort)
	}
	if currentCfg.ControlPort != ctrlPort1 {
		t.Errorf("expected ControlPort %d, got %d", ctrlPort1, currentCfg.ControlPort)
	}
}

func TestControlServer_AppQuit(t *testing.T) {
	tempDir := t.TempDir()
	cfgFile := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgFile)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)
	quitCalled := make(chan struct{})
	ctrl.SetQuitFunc(func() {
		close(quitCalled)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/app/quit", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}

	select {
	case <-quitCalled:
		// Succeeded
	case <-time.After(1 * time.Second):
		t.Fatal("quitFunc was not called within timeout")
	}
}

func TestControlServer_LANIsolation(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// Allocate 2 distinct free ports
	lP, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate proxy port: %v", err)
	}
	proxyPort := lP.Addr().(*net.TCPAddr).Port
	_ = lP.Close()

	lC, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate control port: %v", err)
	}
	ctrlPort := lC.Addr().(*net.TCPAddr).Port
	_ = lC.Close()

	// Configure with AllowLAN = true
	cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = true
		c.ListenPort = proxyPort
		c.ControlPort = ctrlPort
		c.DirectMode = true
		c.CacheDir = tempDir
	})

	certMgr, err := cert.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	proxySrv := proxy.NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}
	defer proxySrv.Stop()

	ctrl := NewControlServer(cfgMgr, certMgr, cacheMgr, proxySrv, stats)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("failed to start control server: %v", err)
	}
	defer ctrl.Stop()

	// 1. Verify control server strictly binds to 127.0.0.1, even when AllowLAN = true
	ctrlAddr := ctrl.listener.Addr().String()
	expectedCtrlAddr := fmt.Sprintf("127.0.0.1:%d", ctrlPort)
	if ctrlAddr != expectedCtrlAddr {
		t.Fatalf("control server must bind strictly to %s, got %s", expectedCtrlAddr, ctrlAddr)
	}

	// 2. 127.0.0.1:8125 -> Connection SUCCEEDS (HTTP 200)
	statusURL := fmt.Sprintf("http://127.0.0.1:%d/api/status", ctrlPort)
	resp, err := http.Get(statusURL)
	if err != nil {
		t.Fatalf("failed to connect to control server on 127.0.0.1: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from 127.0.0.1:%d, got %d", ctrlPort, resp.StatusCode)
	}

	// 3. 127.0.0.1:8124 -> Connection SUCCEEDS
	proxyConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), 1*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to proxy on 127.0.0.1: %v", err)
	}
	proxyConn.Close()

	// 4. Physical / Virtual LAN Interface test
	lanIP := config.GetLANIP()
	if lanIP == "" || lanIP == "127.0.0.1" {
		t.Log("no non-loopback LAN IP detected on this host, skipping interface-level socket dial")
		return
	}

	lanCtrlAddr := net.JoinHostPort(lanIP, fmt.Sprintf("%d", ctrlPort))
	lanProxyAddr := net.JoinHostPort(lanIP, fmt.Sprintf("%d", proxyPort))

	// LAN-IP:8125 -> Connection MUST FAIL (connection refused or timeout)
	ctrlConn, err := net.DialTimeout("tcp", lanCtrlAddr, 300*time.Millisecond)
	if err == nil {
		ctrlConn.Close()
		t.Fatalf("CRITICAL SECURITY VIOLATION: control server (port %d) is reachable via LAN IP %s when AllowLAN=true!", ctrlPort, lanIP)
	}

	// LAN-IP:8124 -> Connection SUCCEEDS (proxy is 0.0.0.0)
	pLANConn, err := net.DialTimeout("tcp", lanProxyAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("proxy on LAN IP %s:%d should be accessible when AllowLAN=true, got err: %v", lanIP, proxyPort, err)
	}
	defer pLANConn.Close()

	// Verify proxy actually serves HTTP on LAN-IP:8124
	req := fmt.Sprintf("GET /ca.crt HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", lanIP)
	if _, err := pLANConn.Write([]byte(req)); err != nil {
		t.Fatalf("failed to write to proxy via LAN socket: %v", err)
	}
	br := bufio.NewReader(pLANConn)
	pResp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("failed to read response from proxy via LAN socket: %v", err)
	}
	pResp.Body.Close()
	if pResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from proxy over LAN socket, got %d", pResp.StatusCode)
	}
}

func TestControlServer_ApplyConfig_SaveFail_NoRuntimeSideEffects(t *testing.T) {
	tempDir := t.TempDir()
	subDir := filepath.Join(tempDir, "config_sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create config subdir: %v", err)
	}
	cfgFile := filepath.Join(subDir, "config.json")
	cfgMgr := config.NewManager(cfgFile)

	initialCacheDir := filepath.Join(tempDir, "initial_cache")
	if err := os.MkdirAll(initialCacheDir, 0755); err != nil {
		t.Fatalf("failed to create initial cache dir: %v", err)
	}
	unwantedCacheDir := filepath.Join(tempDir, "unwanted_cache")

	cacheMgr := cache.NewManager(initialCacheDir, 32)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// Pick 2 free ports
	lP, _ := net.Listen("tcp", "127.0.0.1:0")
	proxyPort := lP.Addr().(*net.TCPAddr).Port
	_ = lP.Close()

	lC, _ := net.Listen("tcp", "127.0.0.1:0")
	ctrlPort := lC.Addr().(*net.TCPAddr).Port
	_ = lC.Close()

	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = proxyPort
		c.ControlPort = ctrlPort
		c.AutoSystemProxy = false
		c.AutoStart = false
		c.CacheDir = initialCacheDir
		c.RAMCacheMaxMB = 32
	})

	proxySrv := proxy.NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}
	defer proxySrv.Stop()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, proxySrv, stats)

	var sysproxyCalled atomic.Bool
	var startupCalled atomic.Bool

	ctrl.enablePACProxyFn = func(url string) error {
		sysproxyCalled.Store(true)
		return nil
	}
	ctrl.disablePACProxyFn = func(force bool) error {
		sysproxyCalled.Store(true)
		return nil
	}
	ctrl.setStartupEnabledFn = func(enabled bool) error {
		startupCalled.Store(true)
		return nil
	}

	if err := ctrl.Start(); err != nil {
		t.Fatalf("failed to start control server: %v", err)
	}
	defer ctrl.Stop()

	// Make Save() fail by replacing subDir directory with a plain regular file
	if err := os.RemoveAll(subDir); err != nil {
		t.Fatalf("failed to remove config subdir: %v", err)
	}
	if err := os.WriteFile(subDir, []byte("blocker_file"), 0644); err != nil {
		t.Fatalf("failed to write blocker file: %v", err)
	}

	// Send POST /api/config/apply requesting side-effecting changes
	applyURL := fmt.Sprintf("http://127.0.0.1:%d/api/config/apply", ctrlPort)
	patchBody := map[string]interface{}{
		"auto_system_proxy": true,
		"auto_start":        true,
		"cache_dir":         unwantedCacheDir,
		"ram_cache_max_mb":  256,
	}
	jsonBytes, _ := json.Marshal(patchBody)

	req, err := http.NewRequest(http.MethodPost, applyURL, bytes.NewReader(jsonBytes))
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request to apply config failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected HTTP 500 due to save failure, got %d", resp.StatusCode)
	}

	var resData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		t.Fatalf("failed to decode response JSON: %v", err)
	}
	if errMsg, _ := resData["error"].(string); !strings.Contains(errMsg, "配置持久化失败") {
		t.Errorf("expected error message to contain '配置持久化失败', got %q", errMsg)
	}

	// 1. Verify sysproxy was NOT called
	if sysproxyCalled.Load() {
		t.Errorf("VIOLATION: sysproxy was executed despite Save() failure")
	}

	// 2. Verify startup was NOT called
	if startupCalled.Load() {
		t.Errorf("VIOLATION: startup was executed despite Save() failure")
	}

	// 3. Verify cache runtime did NOT switch
	if currentBase := cacheMgr.GetCacheBase(); currentBase != initialCacheDir {
		t.Errorf("VIOLATION: cache base switched to %s, expected to remain %s", currentBase, initialCacheDir)
	}

	// 4. Verify in-memory config was rolled back to initial state
	activeCfg := cfgMgr.Get()
	if activeCfg.AutoSystemProxy != false {
		t.Errorf("expected AutoSystemProxy to remain false, got true")
	}
	if activeCfg.AutoStart != false {
		t.Errorf("expected AutoStart to remain false, got true")
	}
	if activeCfg.CacheDir != initialCacheDir {
		t.Errorf("expected CacheDir to remain %s, got %s", initialCacheDir, activeCfg.CacheDir)
	}
	if activeCfg.RAMCacheMaxMB != 32 {
		t.Errorf("expected RAMCacheMaxMB to remain 32, got %d", activeCfg.RAMCacheMaxMB)
	}
}

func TestControlServer_ReloadListener_SamePortNoOp(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	lC, _ := net.Listen("tcp", "127.0.0.1:0")
	ctrlPort := lC.Addr().(*net.TCPAddr).Port
	_ = lC.Close()

	cfgMgr.Update(func(c *config.Config) {
		c.ControlPort = ctrlPort
	})

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)
	if err := ctrl.Start(); err != nil {
		t.Fatalf("failed to start control server: %v", err)
	}
	defer ctrl.Stop()

	ctrl.mu.RLock()
	origLn := ctrl.listener
	origGen := ctrl.listenerGen
	ctrl.mu.RUnlock()

	// Calling ReloadListener with same port
	err := ctrl.ReloadListener(ctrlPort)
	if err != nil {
		t.Fatalf("expected ReloadListener with same port to succeed, got %v", err)
	}

	ctrl.mu.RLock()
	currentLn := ctrl.listener
	currentGen := ctrl.listenerGen
	ctrl.mu.RUnlock()

	if currentLn != origLn {
		t.Errorf("expected control listener pointer to remain unchanged, orig=%p, current=%p", origLn, currentLn)
	}
	if currentGen != origGen {
		t.Errorf("expected control generation to remain %d, got %d", origGen, currentGen)
	}

	// Verify server is still responding
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", ctrlPort))
	if err != nil {
		t.Fatalf("failed to request status after no-op reload: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
}




