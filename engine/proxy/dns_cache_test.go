package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestDNSCache_LookupAndExpiration(t *testing.T) {
	c := newDNSCache(60*time.Millisecond, 2)
	var calls atomic.Int32
	resolveFn := func(ctx context.Context, host string) ([]net.IP, error) {
		calls.Add(1)
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}

	ips1, err := c.lookup(context.Background(), "cdn.example.net", resolveFn)
	if err != nil || len(ips1) != 1 {
		t.Fatalf("first lookup failed: %v", err)
	}
	ips2, err := c.lookup(context.Background(), "CDN.EXAMPLE.NET.", resolveFn)
	if err != nil || len(ips2) != 1 {
		t.Fatalf("second lookup failed: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 resolver call due to cache hit, got %d", calls.Load())
	}

	time.Sleep(80 * time.Millisecond)
	_, err = c.lookup(context.Background(), "cdn.example.net", resolveFn)
	if err != nil {
		t.Fatalf("post-expiry lookup failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected resolver call after TTL expiry, got %d", calls.Load())
	}

	// Capacity capping
	_, _ = c.lookup(context.Background(), "a.example.net", resolveFn)
	_, _ = c.lookup(context.Background(), "b.example.net", resolveFn)
	c.mu.RLock()
	n := len(c.entries)
	c.mu.RUnlock()
	if n > 2 {
		t.Fatalf("expected dnsCache bounded to maxSize=2, got %d", n)
	}
}

func TestSplitUpstreamRouting_AssetOverrideIsolatedFromFailover(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("cert.NewManager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	var primaryHits atomic.Int32
	primaryProxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer primaryProxySrv.Close()

	var assetHits atomic.Int32
	assetProxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer assetProxySrv.Close()

	_ = cfgMgr.Update(func(c *config.Config) {
		c.DirectMode = false
		c.UpstreamProxy = primaryProxySrv.URL
		c.AssetUpstreamProxy = assetProxySrv.URL
		c.BackupUpstreamProxy = "direct"
		c.EnableUpstreamFailover = true
	})

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	defer srv.Stop()

	apiCli, apiRoute, _ := srv.getAPIClientForRequest(http.MethodGet)
	assetCli, assetRoute, trial := srv.getAssetClientForRequest(http.MethodGet)

	if apiRoute != failoverRoutePrimary {
		t.Fatalf("expected API route primary, got %v", apiRoute)
	}
	if assetRoute != failoverRouteOverride || trial {
		t.Fatalf("expected asset route override with trial=false, got route=%v trial=%v", assetRoute, trial)
	}
	if apiCli == assetCli {
		t.Fatal("expected distinct http.Client instances for API and overridden asset upstream")
	}

	// Verify asset client points to assetProxySrv while API client points to primaryProxySrv
	apiReq, _ := http.NewRequest(http.MethodGet, "http://example.com/rest", nil)
	assetReq, _ := http.NewRequest(http.MethodGet, "http://example.com/assets/test.png", nil)
	r1, err := apiCli.Do(apiReq)
	if err != nil {
		t.Fatalf("apiCli.Do: %v", err)
	}
	_ = r1.Body.Close()
	r2, err := assetCli.Do(assetReq)
	if err != nil {
		t.Fatalf("assetCli.Do: %v", err)
	}
	_ = r2.Body.Close()

	if primaryHits.Load() != 1 || assetHits.Load() != 1 {
		t.Fatalf("expected primaryHits=1 assetHits=1, got primary=%d asset=%d", primaryHits.Load(), assetHits.Load())
	}

	// Verify ClientSessionCache is populated on upstream transports
	apiTr := apiCli.Transport.(*http.Transport)
	assetTr := assetCli.Transport.(*http.Transport)
	if apiTr.TLSClientConfig == nil || apiTr.TLSClientConfig.ClientSessionCache == nil {
		t.Fatal("expected ClientSessionCache on apiTr")
	}
	if assetTr.TLSClientConfig == nil || assetTr.TLSClientConfig.ClientSessionCache == nil {
		t.Fatal("expected ClientSessionCache on assetTr")
	}
}
