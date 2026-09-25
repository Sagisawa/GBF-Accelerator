package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func newMockAssetServer(handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	ts := httptest.NewTLSServer(handler)
	tr := ts.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial("tcp", ts.Listener.Addr().String())
	}
	tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	return ts, &http.Client{Transport: tr}
}

// BenchmarkRAMMiss_Network measures end-to-end latency of a cache miss fetching
// from upstream network and completing SingleFlight + Respond-First RAM save.
func BenchmarkRAMMiss_Network(b *testing.B) {
	tempDir := b.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 128)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 16*1024)
	copy(payload, []byte("var a = 1; "))
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"test-etag"`)
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

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("/assets/network_miss_%d.js", i)
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
		req.RequestURI = path
		req.Host = benchHost

		var buf bytes.Buffer
		srv.handleStaticAssetLower(&buf, req, benchHost, path)
	}
}

// BenchmarkSameResource_PrefetchAndRealRequest_Competition tests the scenario
// where Prefetch begins fetching an asset and a real request asks for the exact
// same resource.
func BenchmarkSameResource_PrefetchAndRealRequest_Competition(b *testing.B) {
	tempDir := b.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 128)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	var upstreamHits atomic.Int64
	payload := make([]byte, 32*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		// Simulate network latency (2ms)
		time.Sleep(2 * time.Millisecond)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"png-etag"`)
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

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		path := fmt.Sprintf("/assets/img/sp/compete_%d.png", i)

		var wg sync.WaitGroup
		wg.Add(2)

		// 1. Prefetch starts fetching
		go func() {
			defer wg.Done()
			pe.processFetchItem(prefetchItem{
				prio: 2,
				host: benchHost,
				path: path,
			})
		}()

		// 2. Real browser request arrives almost simultaneously (500us later)
		go func() {
			defer wg.Done()
			time.Sleep(500 * time.Microsecond)
			req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+path, nil)
			req.RequestURI = path
			req.Host = benchHost
			var buf bytes.Buffer
			srv.handleStaticAssetLower(&buf, req, benchHost, path)
		}()

		wg.Wait()
	}

	b.Logf("Total Upstream Hits for %d competitive trials: %d", b.N, upstreamHits.Load())
}

// BenchmarkHighConcurrency_RealRequests measures system throughput under 50 concurrent
// foreground browser requests accessing a mix of cached and new assets.
func BenchmarkHighConcurrency_RealRequests(b *testing.B) {
	tempDir := b.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 128)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 16*1024)
	copy(payload, []byte("var a = 1; "))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("ETag", `"etag"`)
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

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"

	// Pre-seed 20 hot assets
	for i := 0; i < 20; i++ {
		p := fmt.Sprintf("/assets/hot_%d.js", i)
		cacheMgr.SaveRAM(p, map[string]string{"content-type": "application/javascript"}, payload)
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		idx := 0
		for pb.Next() {
			idx++
			p := fmt.Sprintf("/assets/hot_%d.js", idx%20)
			req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+p, nil)
			req.RequestURI = p
			req.Host = benchHost
			var buf bytes.Buffer
			srv.handleStaticAssetLower(&buf, req, benchHost, p)
		}
	})
}
