package proxy

// Regression test: after the in-flight watchdog has already recorded a
// latency_threshold failure for a request, a genuine hard connection error
// returned later by the SAME request must NOT be swallowed. Hard errors are
// immediate failover and must win over the watchdog's soft-failure accounting.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestDynamicAPIWatchdogThenHardErrorNotSwallowed(t *testing.T) {
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
	defer srv.Stop()

	// Primary upstream: waits 100ms (past the 50ms watchdog threshold), then
	// drops the connection without any response -> genuine hard connection error.
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("primary test server does not support hijacking")
			return
		}
		conn, _, herr := hj.Hijack()
		if herr != nil {
			t.Errorf("primary hijack failed: %v", herr)
			return
		}
		_ = conn.Close() // hard connection drop, no response bytes
	}))
	defer primary.Close()

	// Backup upstream: locally controlled healthy upstream.
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backup ok"))
	}))
	defer backup.Close()

	// enable failover = true, threshold = 50ms, consecutive failures = 2
	cfg := cfgMgr.Get()
	cfg.EnableUpstreamFailover = true
	cfg.UpstreamProxy = primary.URL
	cfg.BackupUpstreamProxy = backup.URL
	cfg.UpstreamFailoverThresholdMS = 50
	cfg.UpstreamFailoverConsecutiveFailures = 2
	srv.updateClients(&cfg)

	targetHost := "game.granbluefantasy.jp"
	nonRetryablePath := "/rest/user/status"
	if isRetryableAPI(nonRetryablePath) {
		t.Fatalf("%s must NOT be in retryable API whitelist", nonRetryablePath)
	}

	reqDone := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+nonRetryablePath, nil)
		var buf bytes.Buffer
		srv.handleDecryptedRequest(&buf, req, targetHost)
		reqDone <- firstLine(buf.String())
	}()

	// ~80ms: watchdog (50ms) MUST have fired and recorded one latency_threshold
	// failure, but consecutive=2 means NO failover yet; request still in-flight.
	time.Sleep(80 * time.Millisecond)
	stMid := srv.GetUpstreamStatus()
	if stMid.FailureCount != 1 {
		t.Fatalf("expected watchdog to have recorded exactly 1 latency_threshold failure mid-flight, got failure_count=%d (active=%s reason=%q)", stMid.FailureCount, stMid.Active, stMid.Reason)
	}
	if stMid.Active != "primary" {
		t.Fatalf("expected active to remain primary after first watchdog fire (consecutive=2), got %s", stMid.Active)
	}

	// Wait for the request to finish (~100ms: hard connection error from primary).
	var statusLine string
	select {
	case statusLine = <-reqDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for request to complete")
	}
	if !strings.HasPrefix(statusLine, "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway from hard connection error, got: %q", statusLine)
	}

	// The hard connection error is an IMMEDIATE failover trigger. It must not be
	// swallowed just because the watchdog fired first: the route must switch to
	// backup with reason connection_error, not stall at failure_count=1.
	stFinal := srv.GetUpstreamStatus()
	if stFinal.Active != "backup" {
		t.Fatalf("hard error after watchdog fire must trigger immediate failover to backup, got active=%s (failure_count=%d reason=%q)", stFinal.Active, stFinal.FailureCount, stFinal.Reason)
	}
	if stFinal.Reason != "connection_error" {
		t.Fatalf("expected reason connection_error, got %q", stFinal.Reason)
	}
}

// Boundary test: watchdog fires with consecutive=1 and switches primary ->
// backup immediately. When the same in-flight request later returns a hard
// error, the failover manager must NOT switch again nor corrupt its counters:
// observe()'s route != active guard makes the late hard-error report a no-op.
func TestDynamicAPIWatchdogSwitchThenHardErrorNoDoubleSwitch(t *testing.T) {
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
	defer srv.Stop()

	// Primary upstream: waits 100ms (past the 50ms watchdog threshold), then
	// drops the connection without any response -> genuine hard connection error.
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("primary test server does not support hijacking")
			return
		}
		conn, _, herr := hj.Hijack()
		if herr != nil {
			t.Errorf("primary hijack failed: %v", herr)
			return
		}
		_ = conn.Close()
	}))
	defer primary.Close()

	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backup ok"))
	}))
	defer backup.Close()

	// consecutive=1: the first watchdog fire switches to backup immediately.
	cfg := cfgMgr.Get()
	cfg.EnableUpstreamFailover = true
	cfg.UpstreamProxy = primary.URL
	cfg.BackupUpstreamProxy = backup.URL
	cfg.UpstreamFailoverThresholdMS = 50
	cfg.UpstreamFailoverConsecutiveFailures = 1
	srv.updateClients(&cfg)

	targetHost := "game.granbluefantasy.jp"
	nonRetryablePath := "/rest/user/status"

	reqDone := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "https://"+targetHost+nonRetryablePath, nil)
		var buf bytes.Buffer
		srv.handleDecryptedRequest(&buf, req, targetHost)
		reqDone <- firstLine(buf.String())
	}()

	// ~80ms: watchdog already fired and switched primary -> backup.
	time.Sleep(80 * time.Millisecond)
	stMid := srv.GetUpstreamStatus()
	if stMid.Active != "backup" || stMid.Reason != "latency_threshold" {
		t.Fatalf("expected watchdog switch to backup/latency_threshold mid-flight, got active=%s reason=%q", stMid.Active, stMid.Reason)
	}
	if stMid.FailureCount != 0 {
		t.Fatalf("expected failure_count reset to 0 after switch, got %d", stMid.FailureCount)
	}

	var statusLine string
	select {
	case statusLine = <-reqDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for request to complete")
	}
	if !strings.HasPrefix(statusLine, "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway from hard connection error, got: %q", statusLine)
	}

	// Late hard error from the ALREADY-SWITCHED primary route must be a no-op:
	// still backup, reason unchanged, failure_count not wrongly incremented.
	stFinal := srv.GetUpstreamStatus()
	if stFinal.Active != "backup" {
		t.Fatalf("active route must remain backup after late hard error, got %s", stFinal.Active)
	}
	if stFinal.Reason != "latency_threshold" {
		t.Fatalf("reason must remain latency_threshold after late hard error, got %q", stFinal.Reason)
	}
	if stFinal.FailureCount != 0 {
		t.Fatalf("failure_count must not be incremented by late hard error, got %d", stFinal.FailureCount)
	}
}
