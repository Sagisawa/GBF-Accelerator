//go:build !windows

package control

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/firewall"
	"gbf-proxy/telemetry"
)

func TestControlServerFirewallStatusUnsupportedOnNonWindows(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	req := httptest.NewRequest(http.MethodGet, "/api/firewall/status", nil)
	req.Host = "127.0.0.1:8125"
	rec := httptest.NewRecorder()
	ctrl.handleRoute(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for unsupported firewall integration, got %d", rec.Code)
	}
	if firewall.IsSupported() {
		t.Fatal("non-Windows test compiled with supported firewall implementation")
	}
}
