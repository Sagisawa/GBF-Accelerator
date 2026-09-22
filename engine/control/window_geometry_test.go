package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestControlServer_ApplyConfig_WindowGeometry(t *testing.T) {
	dir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(dir, "config.json"))
	ctrl := NewControlServer(cfgMgr, nil, nil, nil, telemetry.NewStats())

	body, err := json.Marshal(map[string]interface{}{
		"window_width":     1200,
		"window_height":    800,
		"window_maximized": true,
	})
	if err != nil {
		t.Fatalf("failed to encode patch: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/config/apply", bytes.NewReader(body))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	cfg := cfgMgr.Get()
	if cfg.WindowWidth != 1200 || cfg.WindowHeight != 800 || !cfg.WindowMaximized {
		t.Fatalf("window geometry was not persisted: %+v", cfg)
	}

	invalidReq := httptest.NewRequest(
		http.MethodPost,
		"/api/config/apply",
		bytes.NewReader([]byte(`{"window_width":399,"window_height":399,"window_maximized":false}`)),
	)
	invalidReq.Host = "127.0.0.1:8125"
	invalidW := httptest.NewRecorder()
	ctrl.handleRoute(invalidW, invalidReq)

	if invalidW.Code != http.StatusOK {
		t.Fatalf("expected invalid geometry patch to be ignored successfully, got %d: %s", invalidW.Code, invalidW.Body.String())
	}

	cfg = cfgMgr.Get()
	if cfg.WindowWidth != 1200 || cfg.WindowHeight != 800 || cfg.WindowMaximized {
		t.Fatalf("invalid geometry patch changed persisted state: %+v", cfg)
	}
}
