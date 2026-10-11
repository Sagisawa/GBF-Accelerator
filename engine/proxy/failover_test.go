package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestFailoverManagerRequiresConsecutiveFailures(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 3, 60*time.Second, true)
	now := time.Unix(100, 0)

	for i := 0; i < 2; i++ {
		if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now.Add(time.Duration(i)*time.Second)); tr != nil {
			t.Fatalf("unexpected transition: %+v", tr)
		}
	}
	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now.Add(2*time.Second)); tr == nil || tr.to != failoverRouteBackup {
		t.Fatalf("expected backup transition, got %+v", tr)
	}
}

func TestFailoverManagerConnectionErrorCounts(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 0, 3, 60*time.Second, true)
	now := time.Unix(200, 0)
	err := errors.New("connect failed")

	// Hard connection errors bypass the consecutive counter — first error
	// triggers immediate failover regardless of threshold setting.
	if tr := m.observe(failoverRoutePrimary, false, 50*time.Millisecond, err, now); tr == nil || tr.reason != "connection_error" {
		t.Fatalf("expected immediate connection_error transition, got %+v", tr)
	}
}

func TestFailoverManagerRecoveryTrial(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 10*time.Second, true)
	now := time.Unix(300, 0)

	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now); tr == nil {
		t.Fatal("expected switch")
	}
	if route, trial := m.routeForRequest(true, now.Add(5*time.Second)); route != failoverRouteBackup || trial {
		t.Fatalf("expected cooldown, got %s/%v", route, trial)
	}
	if route, trial := m.routeForRequest(true, now.Add(11*time.Second)); route != failoverRoutePrimary || !trial {
		t.Fatalf("expected recovery trial, got %s/%v", route, trial)
	}
	if route, trial := m.routeForRequest(true, now.Add(12*time.Second)); route != failoverRouteBackup || trial {
		t.Fatalf("expected backup while trial runs, got %s/%v", route, trial)
	}
	if tr := m.observe(failoverRoutePrimary, true, 50*time.Millisecond, nil, now.Add(11*time.Second)); tr == nil || tr.reason != "primary_recovered" {
		t.Fatalf("expected recovery transition, got %+v", tr)
	}
	if route, trial := m.routeForRequest(true, now.Add(12*time.Second)); route != failoverRoutePrimary || trial {
		t.Fatalf("expected primary after recovery, got %s/%v", route, trial)
	}
}

func TestFailoverManagerFailedRecoveryTrialKeepsBackup(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 10*time.Second, true)
	now := time.Unix(400, 0)

	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now); tr == nil {
		t.Fatal("expected switch")
	}
	_, trial := m.routeForRequest(true, now.Add(11*time.Second))
	if !trial {
		t.Fatal("expected recovery trial")
	}
	if tr := m.observe(failoverRoutePrimary, true, 3*time.Second, nil, now.Add(11*time.Second)); tr != nil {
		t.Fatalf("unexpected recovery transition: %+v", tr)
	}
	if route, trial := m.routeForRequest(true, now.Add(12*time.Second)); route != failoverRouteBackup || trial {
		t.Fatalf("expected backup after failed trial, got %s/%v", route, trial)
	}
}

func TestFailoverManagerIgnoresBackupObservations(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 60*time.Second, true)
	now := time.Unix(500, 0)

	if tr := m.observe(failoverRoutePrimary, false, 3*time.Second, nil, now); tr == nil {
		t.Fatal("expected switch")
	}
	if tr := m.observe(failoverRouteBackup, false, 10*time.Second, errors.New("backup failed"), now.Add(time.Second)); tr != nil {
		t.Fatalf("unexpected backup transition: %+v", tr)
	}
}

func TestFailoverManagerDisabledNeverSwitches(t *testing.T) {
	m := newFailoverManager()
	m.configure(false, true, 2*time.Second, 1, 60*time.Second, true)
	if tr := m.observe(failoverRoutePrimary, false, 10*time.Second, errors.New("fail"), time.Now()); tr != nil {
		t.Fatalf("disabled switched: %+v", tr)
	}
	if got := m.activeRoute(); got != failoverRoutePrimary {
		t.Fatalf("expected primary, got %s", got)
	}
}

func TestFailoverProxySchemeValidation(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:7890", "https://127.0.0.1:8443", "socks5://127.0.0.1:10808", "direct"} {
		if !isSupportedFailoverProxy(raw) { t.Errorf("unsupported: %q", raw) }
	}
	for _, raw := range []string{"", "ftp://127.0.0.1:21", "not-a-url"} {
		if isSupportedFailoverProxy(raw) { t.Errorf("unexpected support: %q", raw) }
	}
}

func TestFailoverManagerInFlightTimeout(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 2, 60*time.Second, true)
	now := time.Unix(600, 0)

	// First in-flight timeout: count becomes 1, no switch yet
	if tr := m.observeInFlightTimeout(failoverRoutePrimary, false, now); tr != nil {
		t.Fatalf("unexpected transition on first in-flight timeout: %+v", tr)
	}
	if m.status().FailureCount != 1 {
		t.Fatalf("expected failure count 1, got %d", m.status().FailureCount)
	}

	// Second in-flight timeout: reaches consecutive (2), switches to backup!
	tr := m.observeInFlightTimeout(failoverRoutePrimary, false, now.Add(time.Second))
	if tr == nil || tr.to != failoverRouteBackup || tr.reason != "latency_threshold" {
		t.Fatalf("expected backup transition with latency_threshold, got %+v", tr)
	}
	if m.activeRoute() != failoverRouteBackup {
		t.Fatalf("expected active route backup, got %s", m.activeRoute())
	}
}

func TestFailoverManagerInFlightRecoveryTrialTimeout(t *testing.T) {
	m := newFailoverManager()
	m.configure(true, true, 2*time.Second, 1, 10*time.Second, true)
	now := time.Unix(700, 0)

	// Switch to backup
	if tr := m.observeInFlightTimeout(failoverRoutePrimary, false, now); tr == nil {
		t.Fatal("expected switch to backup")
	}

	// Trial request
	_, trial := m.routeForRequest(true, now.Add(11*time.Second))
	if !trial {
		t.Fatal("expected recovery trial")
	}

	// Trial request times out in flight
	if tr := m.observeInFlightTimeout(failoverRoutePrimary, true, now.Add(13*time.Second)); tr != nil {
		t.Fatalf("unexpected transition on failed trial: %+v", tr)
	}
	if m.activeRoute() != failoverRouteBackup {
		t.Fatalf("expected active route to remain backup, got %s", m.activeRoute())
	}
	if m.status().Reason != "primary_recovery_failed" {
		t.Fatalf("expected reason primary_recovery_failed, got %s", m.status().Reason)
	}
}

func TestFailoverRecoveryTrialUsesPrimaryClient_APIAndAssets(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	var primaryHits atomic.Int64
	var backupHits atomic.Int64
	var primaryHealthy atomic.Bool

	pngBytes := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("P", 64))

	primarySrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		if !primaryHealthy.Load() {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".png") {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pngBytes)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"route":"primary"}`))
	}))
	defer primarySrv.Close()

	backupSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupHits.Add(1)
		if strings.HasSuffix(r.URL.Path, ".png") {
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pngBytes)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"route":"backup"}`))
	}))
	defer backupSrv.Close()

	makeClient := func(ts *httptest.Server) *http.Client {
		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ts.Listener.Addr().String())
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		return &http.Client{Transport: tr}
	}

	primaryClient := makeClient(primarySrv)
	backupClient := makeClient(backupSrv)

	srv.primaryClient.Store(&upstreamClients{api: primaryClient, asset: primaryClient, proxy: "http://127.0.0.1:18080"})
	srv.backupClient.Store(&upstreamClients{api: backupClient, asset: backupClient, proxy: "http://127.0.0.1:18081"})
	srv.failover.configure(true, true, 500*time.Millisecond, 1, time.Second, true)
	srv.activateRoute(failoverRoutePrimary)

	const apiHost = "game.granbluefantasy.jp"
	const assetHost = "prd-game-a-granbluefantasy.akamaized.net"

	// 1. Primary is unhealthy -> first GET fails and switches active route to backup
	req1, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status", nil)
	var buf1 bytes.Buffer
	srv.handleDecryptedRequest(&buf1, req1, apiHost)
	if st := srv.GetUpstreamStatus(); st.Active != "backup" {
		t.Fatalf("expected failover to backup, got %+v", st)
	}
	if primaryHits.Load() != 1 || backupHits.Load() != 0 {
		t.Fatalf("expected primary=1 backup=0 after initial failure, got primary=%d backup=%d", primaryHits.Load(), backupHits.Load())
	}

	// 2. During cooldown, requests must go to backup
	req2, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status", nil)
	var buf2 bytes.Buffer
	srv.handleDecryptedRequest(&buf2, req2, apiHost)
	if !strings.Contains(buf2.String(), `{"route":"backup"}`) {
		t.Fatalf("expected backup response during cooldown, got: %s", buf2.String())
	}
	if primaryHits.Load() != 1 || backupHits.Load() != 1 {
		t.Fatalf("expected primary=1 backup=1 during cooldown, got primary=%d backup=%d", primaryHits.Load(), backupHits.Load())
	}

	// 3. Advance cooldown; write request (POST) must NOT trigger trial and must stay on backup
	srv.failover.mu.Lock()
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()

	reqPost, _ := http.NewRequest(http.MethodPost, "https://"+apiHost+"/rest/raid/ability_result.json", strings.NewReader(`{"id":1}`))
	var bufPost bytes.Buffer
	srv.handleDecryptedRequest(&bufPost, reqPost, apiHost)
	if primaryHits.Load() != 1 || backupHits.Load() != 2 {
		t.Fatalf("POST must use active backup route without triggering trial: got primary=%d backup=%d", primaryHits.Load(), backupHits.Load())
	}

	// 4. Primary still unhealthy -> GET triggers recovery trial on PRIMARY client, fails, and stays on backup
	req3, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status", nil)
	var buf3 bytes.Buffer
	srv.handleDecryptedRequest(&buf3, req3, apiHost)
	if primaryHits.Load() != 2 || backupHits.Load() != 2 {
		t.Fatalf("expected failed trial to hit primary (primary=2 backup=2), got primary=%d backup=%d", primaryHits.Load(), backupHits.Load())
	}
	if st := srv.GetUpstreamStatus(); st.Active != "backup" || st.Reason != "primary_recovery_failed" {
		t.Fatalf("expected backup with primary_recovery_failed after failed trial, got %+v", st)
	}

	// 5. Primary becomes healthy; advance cooldown -> GET triggers recovery trial on PRIMARY client and recovers!
	primaryHealthy.Store(true)
	srv.failover.mu.Lock()
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()

	req4, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status", nil)
	var buf4 bytes.Buffer
	srv.handleDecryptedRequest(&buf4, req4, apiHost)
	if !strings.Contains(buf4.String(), `{"route":"primary"}`) {
		t.Fatalf("expected trial request to hit primary and return primary response, got: %s", buf4.String())
	}
	if primaryHits.Load() != 3 || backupHits.Load() != 2 {
		t.Fatalf("expected successful API trial to hit primary (primary=3 backup=2), got primary=%d backup=%d", primaryHits.Load(), backupHits.Load())
	}
	if st := srv.GetUpstreamStatus(); st.Active != "primary" || st.Reason != "primary_recovered" {
		t.Fatalf("expected active route primary with primary_recovered, got %+v", st)
	}

	// 6. Verify static asset path also uses primary client on recovery trial (both failed trial and successful recovery)
	primaryHealthy.Store(false)
	reqAssetFail, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets/img/trial_fail.png", nil)
	var bufAssetFail bytes.Buffer
	srv.handleDecryptedRequest(&bufAssetFail, reqAssetFail, assetHost)
	if st := srv.GetUpstreamStatus(); st.Active != "backup" {
		t.Fatalf("expected asset failure on primary to switch to backup, got %+v", st)
	}

	// Advance cooldown while primary is still unhealthy -> asset trial hits primary, fails, stays on backup
	srv.failover.mu.Lock()
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()
	beforePrimary := primaryHits.Load()
	beforeBackup := backupHits.Load()

	reqAssetTrialFail, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets/img/trial_probe1.png", nil)
	var bufAssetTrialFail bytes.Buffer
	srv.handleDecryptedRequest(&bufAssetTrialFail, reqAssetTrialFail, assetHost)
	if primaryHits.Load() != beforePrimary+1 || backupHits.Load() != beforeBackup {
		t.Fatalf("expected static asset trial to hit primary client, got primary=%d (want %d), backup=%d (want %d)",
			primaryHits.Load(), beforePrimary+1, backupHits.Load(), beforeBackup)
	}
	if st := srv.GetUpstreamStatus(); st.Active != "backup" || st.Reason != "primary_recovery_failed" {
		t.Fatalf("expected backup and primary_recovery_failed after failed asset trial, got %+v", st)
	}

	// Now make primary healthy and advance cooldown -> static asset trial hits primary and recovers!
	primaryHealthy.Store(true)
	srv.failover.mu.Lock()
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()
	beforePrimary = primaryHits.Load()
	beforeBackup = backupHits.Load()

	reqAssetTrialOK, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets/img/trial_probe2.png", nil)
	var bufAssetTrialOK bytes.Buffer
	srv.handleDecryptedRequest(&bufAssetTrialOK, reqAssetTrialOK, assetHost)
	if !strings.HasPrefix(bufAssetTrialOK.String(), "HTTP/1.1 200 OK") {
		t.Fatalf("expected 200 OK from recovered primary asset fetch, got: %s", firstLine(bufAssetTrialOK.String()))
	}
	if primaryHits.Load() != beforePrimary+1 || backupHits.Load() != beforeBackup {
		t.Fatalf("expected successful static asset trial to hit primary client, got primary=%d (want %d), backup=%d (want %d)",
			primaryHits.Load(), beforePrimary+1, backupHits.Load(), beforeBackup)
	}
	if st := srv.GetUpstreamStatus(); st.Active != "primary" || st.Reason != "primary_recovered" {
		t.Fatalf("expected primary and primary_recovered after asset trial, got %+v", st)
	}
}

type slowClientWriter struct {
	buf   bytes.Buffer
	delay time.Duration
}

func (s *slowClientWriter) Write(p []byte) (int, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return s.buf.Write(p)
}

// TestFailover_FastHeaderSlowBodyAndSlowClientIsolation verifies:
// 1. When a recovery trial receives response headers immediately (<5ms) but the response body is slower
//    than the failover timeout threshold, the trial is NOT mistakenly marked as recovered (both API and Asset paths).
// 2. On the streaming static asset path, a slow client writer does NOT trigger a false failover to backup
//    because tracker.pause() excludes client write time from upstream latency tracking.
// 3. trialInFlight is properly released across slow-body failures and context cancellations.
func TestFailover_FastHeaderSlowBodyAndSlowClientIsolation(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	var primarySlowBody atomic.Bool
	pngPayload := make([]byte, 32*1024)
	copy(pngPayload, []byte("\x89PNG\r\n\x1a\n"))
	for i := 8; i < len(pngPayload); i++ {
		pngPayload[i] = byte((i * 11) % 251)
	}

	primaryServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pngPayload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pngPayload[:4096])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if primarySlowBody.Load() {
				time.Sleep(150 * time.Millisecond)
			}
			_, _ = w.Write(pngPayload[4096:])
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"part":1`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if primarySlowBody.Load() {
			time.Sleep(150 * time.Millisecond)
		}
		_, _ = w.Write([]byte(`,"part":2}`))
	}))
	defer primaryServer.Close()

	backupServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"route":"backup"}`))
	}))
	defer backupServer.Close()

	makeClient := func(ts *httptest.Server) *http.Client {
		tr := ts.Client().Transport.(*http.Transport).Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("tcp", ts.Listener.Addr().String())
		}
		tr.TLSClientConfig.InsecureSkipVerify = true
		wrapAPITransportSingleAttempt(tr)
		return &http.Client{Transport: tr}
	}

	primaryClient := makeClient(primaryServer)
	backupClient := makeClient(backupServer)

	srv.primaryClient.Store(&upstreamClients{api: primaryClient, asset: primaryClient, proxy: "http://127.0.0.1:10808"})
	srv.backupClient.Store(&upstreamClients{api: backupClient, asset: backupClient, proxy: "http://127.0.0.1:10809"})
	srv.failover.configure(true, true, 60*time.Millisecond, 1, time.Second, true)
	srv.activateRoute(failoverRouteBackup)
	srv.failover.mu.Lock()
	srv.failover.active = failoverRouteBackup
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()

	apiHost := "game.granbluefantasy.jp"
	assetHost := "prd-game-a-granbluefantasy.akamaized.net"

	// 1. API Trial with fast headers (<5ms) but slow body (150ms > 60ms threshold) -> must NOT recover!
	primarySlowBody.Store(true)
	reqAPITrial, _ := http.NewRequest(http.MethodGet, "https://"+apiHost+"/rest/user/status", nil)
	var bufAPITrial bytes.Buffer
	srv.handleDecryptedRequest(&bufAPITrial, reqAPITrial, apiHost)
	if st := srv.GetUpstreamStatus(); st.Active != "backup" || st.Reason != "primary_recovery_failed" {
		t.Fatalf("expected API trial with slow body to stay on backup with primary_recovery_failed, got %+v", st)
	}
	srv.failover.mu.Lock()
	if srv.failover.trialInFlight {
		srv.failover.mu.Unlock()
		t.Fatalf("trialInFlight leaked after slow-body API trial")
	}
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()

	// 2. Static Asset streaming Trial with fast headers + fast initial 4KB chunk, but slow remaining body (150ms > 60ms) -> must NOT recover!
	reqAssetTrial, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets/img/slow_body_trial.png", nil)
	var bufAssetTrial bytes.Buffer
	srv.handleDecryptedRequest(&bufAssetTrial, reqAssetTrial, assetHost)
	if st := srv.GetUpstreamStatus(); st.Active != "backup" || st.Reason != "primary_recovery_failed" {
		t.Fatalf("expected streaming asset trial with slow body to stay on backup with primary_recovery_failed, got %+v", st)
	}
	srv.failover.mu.Lock()
	if srv.failover.trialInFlight {
		srv.failover.mu.Unlock()
		t.Fatalf("trialInFlight leaked after slow-body asset trial")
	}
	// Switch active to primary and verify slow client writer does NOT cause false failover
	srv.failover.active = failoverRoutePrimary
	srv.failover.failureCount = 0
	srv.failover.mu.Unlock()
	srv.activateRoute(failoverRoutePrimary)

	// 3. Fast upstream (<5ms) + Slow client writer (120ms > 60ms threshold) -> must stay on primary!
	primarySlowBody.Store(false)
	slowWriter := &slowClientWriter{delay: 50 * time.Millisecond}
	reqSlowClient, _ := http.NewRequest(http.MethodGet, "https://"+assetHost+"/assets/img/slow_client_fast_up.png", nil)
	srv.handleDecryptedRequest(slowWriter, reqSlowClient, assetHost)
	if st := srv.GetUpstreamStatus(); st.Active != "primary" {
		t.Fatalf("slow client write must not be misclassified as upstream timeout; got status %+v", st)
	}
}


