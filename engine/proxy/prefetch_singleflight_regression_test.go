package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// TestSingleFlight_PrefetchAndForegroundCoalescing verifies that when Prefetch is
// downloading an asset, an incoming browser request for the same asset coalesces into
// the in-flight download without launching a second request to upstream.
func TestSingleFlight_PrefetchAndForegroundCoalescing(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := make([]byte, 8*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(30 * time.Millisecond) // slow download
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"singleflight-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/singleflight_coalesce.png"

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Prefetch starts download
	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: path,
		})
	}()

	// 2. Real browser request arrives 5ms later while download is in-flight
	var respBuf bytes.Buffer
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
		req.RequestURI = path
		req.Host = benchHost
		srv.handleStaticAssetLower(&respBuf, req, benchHost, path)
	}()

	wg.Wait()

	// Assert: Upstream was contacted exactly ONCE
	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected strictly 1 upstream hit (coalesced), got %d", hits)
	}

	// Assert: Browser received the full asset
	if !bytes.Contains(respBuf.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("browser response did not contain asset data: %s", respBuf.String())
	}
}

// TestSingleFlight_ForegroundPreemptsPrefetch verifies that when a browser request
// is already in-flight, a subsequent prefetch task for the same resource detects
// that the flight is already active and skips launching a duplicate request.
func TestSingleFlight_ForegroundPreemptsPrefetch(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := make([]byte, 8*1024)
	copy(payload, []byte("var fg = 1; "))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"fg-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/js/fg_first.js"

	var wg sync.WaitGroup
	wg.Add(2)

	// 1. Foreground request starts download
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
		req.RequestURI = path
		req.Host = benchHost
		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, path)
	}()

	// 2. Prefetch tries to fetch the same item 5ms later
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		pe.processFetchItem(prefetchItem{
			prio: 1,
			host: benchHost,
			path: path,
		})
	}()

	wg.Wait()

	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected strictly 1 upstream hit, got %d", hits)
	}
}

// TestPrefetch_OversizedAssetSkipped_ForegroundWaiterFallsBackCleanly verifies that
// when Prefetch encounters an asset larger than MaxRAMItemSize, it skips buffering
// the oversized body and returns (nil, nil) so any coalesced foreground waiter
// falls back via serveStaticAssetFollowerFallback to stream and receive 200 OK (not 502).
func TestPrefetch_OversizedAssetSkipped_ForegroundWaiterFallsBackCleanly(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		name := "KnownContentLength"
		if chunked {
			name = "ChunkedUnknownLength"
		}
		t.Run(name, func(t *testing.T) {
			tempDir := t.TempDir()
			cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
			// 1MB RAM -> MaxRAMItemSize is 64KB
			cacheMgr := cache.NewManager(tempDir, 1)
			defer cacheMgr.Close()
			stats := telemetry.NewStats()
			defer stats.Close()

			const totalSize = 128 * 1024 // 128KB > 64KB MaxRAMItemSize
			payload := make([]byte, totalSize)
			copy(payload, []byte("\x89PNG\r\n\x1a\n"))
			for i := 8; i < totalSize; i++ {
				payload[i] = byte(i % 251)
			}

			prefetchStarted := make(chan struct{}, 1)
			allowUpstreamWrite := make(chan struct{})

			ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") == prefetchUserAgent {
					select {
					case prefetchStarted <- struct{}{}:
					default:
					}
					<-allowUpstreamWrite
				}
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("ETag", `"oversized-etag"`)
				if !chunked {
					w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
				}
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok && chunked {
					f.Flush()
				}
				_, _ = w.Write(payload)
			})
			defer ts.Close()

			srv := &ProxyServer{
				cfgMgr:   cfgMgr,
				cacheMgr: cacheMgr,
				stats:    stats,
			}
			setTestAssetClient(srv, client)
			pe := newPrefetchEngine(srv)
			defer pe.Stop()

			const host = "prd-game-a-granbluefantasy.akamaized.net"
			const path = "/assets/img/sp/oversized_coalesce.png"

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				pe.processFetchItem(prefetchItem{
					prio: 2,
					host: host,
					path: path,
				})
			}()

			<-prefetchStarted

			var respBuf bytes.Buffer
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
				req.RequestURI = path
				req.Host = host
				srv.handleStaticAssetLower(&respBuf, req, host, path)
			}()

			// Wait until foreground waiter attaches to SingleFlight
			deadline := time.Now().Add(2 * time.Second)
			for cacheMgr.SingleFlight().Waiters("gbf:"+path) < 1 && time.Now().Before(deadline) {
				time.Sleep(2 * time.Millisecond)
			}
			close(allowUpstreamWrite)

			wg.Wait()

			req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
			resp, err := http.ReadResponse(bufio.NewReader(&respBuf), req)
			if err != nil {
				t.Fatalf("failed to parse foreground response: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected foreground waiter to receive 200 OK via fallback, got %d", resp.StatusCode)
			}
			gotBody, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read foreground body: %v", err)
			}
			if !bytes.Equal(gotBody, payload) {
				t.Fatalf("foreground body mismatch: got %d bytes, want %d bytes", len(gotBody), len(payload))
			}
		})
	}
}

// TestPrefetch_StopCancelsInFlightRequest_ForegroundWaiterFallsBackCleanly verifies that
// calling pe.Stop() cancels an in-flight prefetch HTTP request immediately and returns (nil, nil)
// so any coalesced foreground waiter falls back cleanly and receives 200 OK instead of 502.
func TestPrefetch_StopCancelsInFlightRequest_ForegroundWaiterFallsBackCleanly(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := []byte("console.log('foreground_fallback_after_prefetch_stop');")
	prefetchEntered := make(chan struct{}, 1)
	prefetchCtxCanceled := make(chan struct{}, 1)

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == prefetchUserAgent {
			select {
			case prefetchEntered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			select {
			case prefetchCtxCanceled <- struct{}{}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"stop-fallback-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/js/stop_cancel_fallback.js"

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		pe.processFetchItem(prefetchItem{
			prio: 1,
			host: host,
			path: path,
		})
	}()

	<-prefetchEntered

	var respBuf bytes.Buffer
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&respBuf, req, host, path)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for cacheMgr.SingleFlight().Waiters("gbf:"+path) < 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}

	// Stop prefetch while its upstream request is blocked
	pe.Stop()

	select {
	case <-prefetchCtxCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected in-flight prefetch request context to be canceled promptly on pe.Stop()")
	}

	wg.Wait()

	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	resp, err := http.ReadResponse(bufio.NewReader(&respBuf), req)
	if err != nil {
		t.Fatalf("failed to parse foreground response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected foreground waiter to receive 200 OK after prefetch cancellation, got %d", resp.StatusCode)
	}
	gotBody, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(gotBody, payload) {
		t.Fatalf("foreground body mismatch: got %q, want %q", string(gotBody), string(payload))
	}
}

// TestPrefetch_InflightKeyNormalizedWithQueryAndCase verifies that dispatchDiscoveredRefs
// and fetchWorker use the same normalized inflight key when paths contain query strings
// or hosts contain uppercase characters.
func TestPrefetch_InflightKeyNormalizedWithQueryAndCase(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("console.log('inflight_key_norm');"))
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	pe.dispatchDiscoveredRefs([]discoveredRef{
		{
			host: "PRD-GAME-A-GRANBLUEFANTASY.AKAMAIZED.NET",
			path: "/assets/js/query_norm.js?v=20261013",
			prio: 1,
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pe.inflightMu.Lock()
		n := len(pe.inflight)
		pe.inflightMu.Unlock()
		if n == 0 && cacheMgr.HasCacheWithNamespace("gbf", "/assets/js/query_norm.js") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	pe.inflightMu.Lock()
	remaining := len(pe.inflight)
	pe.inflightMu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected pe.inflight to be empty after fetchWorker processed query-string item, got %d entries", remaining)
	}
}

// TestPrefetch_FollowerExitsPromptlyOnStop_WhileForegroundLeaderCompletes verifies the second
// SingleFlight ordering scenario: when a foreground request becomes the SingleFlight Leader first
// and Prefetch enters DoContext as a Follower, calling pe.Stop() causes the Prefetch Follower to
// exit promptly without waiting for the foreground request to complete, while the foreground
// Leader continues normally and receives the full 200 OK response.
func TestPrefetch_FollowerExitsPromptlyOnStop_WhileForegroundLeaderCompletes(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := []byte("console.log('foreground_leader_continues_after_prefetch_follower_stops');")
	var upstreamHits atomic.Int64
	fgUpstreamEntered := make(chan struct{}, 1)
	allowForegroundFinish := make(chan struct{})

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		select {
		case fgUpstreamEntered <- struct{}{}:
		default:
		}
		<-allowForegroundFinish
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"fg-leader-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/js/fg_leader_prefetch_follower.js"
	const flightKey = "gbf:" + path

	var fgBuf bytes.Buffer
	fgDone := make(chan struct{})

	oldHook := testBeforePrefetchSingleFlightDoHook
	testBeforePrefetchSingleFlightDoHook = func(fk string) {
		if fk != flightKey {
			return
		}
		// Start foreground request so it becomes the SingleFlight Leader just before Prefetch enters DoContext
		go func() {
			defer close(fgDone)
			req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
			req.RequestURI = path
			req.Host = host
			srv.handleStaticAssetLower(&fgBuf, req, host, path)
		}()
		<-fgUpstreamEntered
	}
	defer func() {
		testBeforePrefetchSingleFlightDoHook = oldHook
	}()

	prefetchDone := make(chan struct{})
	go func() {
		defer close(prefetchDone)
		pe.processFetchItem(prefetchItem{
			prio: 1,
			host: host,
			path: path,
		})
	}()

	// Wait until Prefetch has joined SingleFlight as a Follower behind the Foreground Leader
	deadline := time.Now().Add(2 * time.Second)
	for cacheMgr.SingleFlight().Waiters(flightKey) < 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if gotWaiters := cacheMgr.SingleFlight().Waiters(flightKey); gotWaiters != 1 {
		t.Fatalf("expected Prefetch to be waiting as SingleFlight follower (waiters=1), got %d", gotWaiters)
	}

	// Stop Prefetch while Foreground Leader is still blocked on allowForegroundFinish
	pe.Stop()

	// Assert 1: Prefetch Follower exits promptly without waiting for Foreground Leader to finish
	select {
	case <-prefetchDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected Prefetch SingleFlight follower to exit promptly on pe.Stop() while foreground leader is still in flight")
	}

	if gotWaiters := cacheMgr.SingleFlight().Waiters(flightKey); gotWaiters != 0 {
		t.Fatalf("expected SingleFlight waiters to drop to 0 after Prefetch follower canceled, got %d", gotWaiters)
	}

	// Assert 2: Release Foreground Leader and verify it completes with full 200 OK response
	close(allowForegroundFinish)

	select {
	case <-fgDone:
	case <-time.After(2 * time.Second):
		t.Fatal("foreground leader did not complete after releasing upstream")
	}

	if hits := upstreamHits.Load(); hits != 1 {
		t.Fatalf("expected strictly 1 upstream hit from foreground leader, got %d", hits)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	resp, err := http.ReadResponse(bufio.NewReader(&fgBuf), req)
	if err != nil {
		t.Fatalf("failed to parse foreground leader response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected foreground leader to receive 200 OK, got %d", resp.StatusCode)
	}
	gotBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read foreground leader body: %v", err)
	}
	if !bytes.Equal(gotBody, payload) {
		t.Fatalf("foreground leader body mismatch: got %q, want %q", string(gotBody), string(payload))
	}
}

// TestPrefetch_UpstreamFailure_ForegroundWaiterFallsBackCleanly verifies that a background
// prefetch which wins the SingleFlight leadership race cannot propagate its own upstream
// failure to a coalesced foreground GET waiter. The waiter must fall back to its own
// request path and receive 200 OK with the asset body, instead of a 502 synthesized from
// the background prefetch's error.
//
// Two upstream failure modes are covered: a non-200 HTTP status and a transport-level
// connection abort.
func TestPrefetch_UpstreamFailure_ForegroundWaiterFallsBackCleanly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failMode string
	}{
		{name: "UpstreamReturns500", failMode: "status"},
		{name: "UpstreamTransportError", failMode: "transport"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
			cacheMgr := cache.NewManager(tempDir, 16)
			defer cacheMgr.Close()
			stats := telemetry.NewStats()
			defer stats.Close()

			payload := []byte("console.log('foreground_fallback_after_prefetch_upstream_failure');")
			prefetchEntered := make(chan struct{}, 1)
			releasePrefetch := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releasePrefetch) }) }
			defer release()

			ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("User-Agent") == prefetchUserAgent {
					select {
					case prefetchEntered <- struct{}{}:
					default:
					}
					<-releasePrefetch
					if tc.failMode == "transport" {
						panic(http.ErrAbortHandler)
					}
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/javascript")
				w.Header().Set("ETag", `"upstream-failure-fallback-etag"`)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(payload)
			})
			defer ts.Close()

			srv := &ProxyServer{
				cfgMgr:   cfgMgr,
				cacheMgr: cacheMgr,
				stats:    stats,
			}
			setTestAssetClient(srv, client)
			pe := newPrefetchEngine(srv)
			defer pe.Stop()

			const host = "prd-game-a-granbluefantasy.akamaized.net"
			const path = "/assets/js/prefetch_upstream_failure.js"

			var wg sync.WaitGroup
			wg.Add(2)

			// 1. Background prefetch becomes the SingleFlight leader and blocks inside its upstream request.
			go func() {
				defer wg.Done()
				pe.processFetchItem(prefetchItem{
					prio: 1,
					host: host,
					path: path,
				})
			}()

			select {
			case <-prefetchEntered:
			case <-time.After(2 * time.Second):
				t.Fatal("prefetch never reached upstream")
			}

			// 2. Foreground GET arrives while the prefetch flight is in-flight, so it attaches as a follower.
			var respBuf bytes.Buffer
			go func() {
				defer wg.Done()
				req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
				req.RequestURI = path
				req.Host = host
				srv.handleStaticAssetLower(&respBuf, req, host, path)
			}()

			deadline := time.Now().Add(2 * time.Second)
			for cacheMgr.SingleFlight().Waiters("gbf:"+path) < 1 && time.Now().Before(deadline) {
				time.Sleep(2 * time.Millisecond)
			}
			if cacheMgr.SingleFlight().Waiters("gbf:"+path) < 1 {
				t.Fatal("foreground request never attached to the prefetch's in-flight SingleFlight call")
			}

			// 3. Fail the prefetch's upstream request now that the foreground waiter is attached.
			release()
			wg.Wait()

			req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
			resp, err := http.ReadResponse(bufio.NewReader(&respBuf), req)
			if err != nil {
				t.Fatalf("failed to parse foreground response: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("foreground waiter inherited the background prefetch failure: got %d, want 200", resp.StatusCode)
			}
			gotBody, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read foreground body: %v", err)
			}
			if !bytes.Equal(gotBody, payload) {
				t.Fatalf("foreground body mismatch: got %q, want %q", string(gotBody), string(payload))
			}
		})
	}
}


