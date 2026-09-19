package control

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
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

	// 3. /api/update/check
	wUpd := testRoute(http.MethodGet, "/api/update/check", "")
	if wUpd.Code != http.StatusOK {
		t.Errorf("/api/update/check expected 200, got %d", wUpd.Code)
	}

	// 4. /api/update/download-status
	wUpdStat := testRoute(http.MethodGet, "/api/update/download-status", "")
	if wUpdStat.Code != http.StatusOK {
		t.Errorf("/api/update/download-status expected 200, got %d", wUpdStat.Code)
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
