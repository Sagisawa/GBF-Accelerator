//go:build !windows

package control

import (
	"encoding/json"
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

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 status response for unsupported firewall integration, got %d", rec.Code)
	}
	var payload struct {
		Supported bool `json:"supported"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode firewall status: %v", err)
	}
	if payload.Supported {
		t.Fatal("expected firewall integration to report supported=false on non-Windows")
	}
	if firewall.IsSupported() {
		t.Fatal("non-Windows test compiled with supported firewall implementation")
	}
}
