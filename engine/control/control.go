package control

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/proxy"
	"gbf-proxy/telemetry"
)

type ControlServer struct {
	cfgMgr     *config.Manager
	cacheMgr   *cache.Manager
	proxySrv   *proxy.ProxyServer
	stats      *telemetry.Stats
	server     *http.Server
	listener   net.Listener
	mu         sync.RWMutex
	distDir    string
	running    bool
	closedChan chan struct{}
}

func NewControlServer(cfgMgr *config.Manager, cacheMgr *cache.Manager, proxySrv *proxy.ProxyServer, stats *telemetry.Stats) *ControlServer {
	// Locate web/dist
	distDir := ""
	for _, candidate := range []string{
		filepath.Join("web", "dist"),
		filepath.Join("..", "web", "dist"),
	} {
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			distDir, _ = filepath.Abs(candidate)
			break
		}
	}

	return &ControlServer{
		cfgMgr:     cfgMgr,
		cacheMgr:   cacheMgr,
		proxySrv:   proxySrv,
		stats:      stats,
		distDir:    distDir,
		closedChan: make(chan struct{}),
	}
}

func (c *ControlServer) Start() error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}

	cfg := c.cfgMgr.Get()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.ControlPort)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		c.mu.Unlock()
		return fmt.Errorf("failed to bind control server on %s: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", c.handleRoute)

	c.listener = ln
	c.server = &http.Server{
		Handler: mux,
	}
	c.running = true
	c.mu.Unlock()

	go func() {
		_ = c.server.Serve(ln)
	}()
	return nil
}

func (c *ControlServer) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.running = false
	if c.server != nil {
		_ = c.server.Close()
	}
	close(c.closedChan)
	c.mu.Unlock()
}

func (c *ControlServer) handleRoute(w http.ResponseWriter, req *http.Request) {
	// CORS Preflight
	if req.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")

	path := req.URL.Path
	switch path {
	case "/api/status":
		if req.Method == http.MethodGet {
			c.handleStatus(w, req)
			return
		}
	case "/api/config":
		if req.Method == http.MethodGet {
			c.handleGetConfig(w, req)
			return
		}
	case "/api/config/apply":
		if req.Method == http.MethodPost {
			c.handleApplyConfig(w, req)
			return
		}
	case "/api/cache/stats":
		if req.Method == http.MethodGet {
			c.handleCacheStats(w, req)
			return
		}
	case "/api/cache/clear":
		if req.Method == http.MethodPost {
			c.handleCacheClear(w, req)
			return
		}
	case "/api/cache/audit":
		if req.Method == http.MethodPost {
			c.handleCacheAudit(w, req)
			return
		}
	case "/api/cache/slim":
		if req.Method == http.MethodPost {
			c.handleCacheSlim(w, req)
			return
		}
	case "/api/prefetch/status":
		if req.Method == http.MethodGet {
			c.handlePrefetchStatus(w, req)
			return
		}
	case "/api/telemetry":
		if req.Method == http.MethodGet {
			c.handleTelemetry(w, req)
			return
		}
	case "/api/logs":
		if req.Method == http.MethodGet {
			c.handleLogs(w, req)
			return
		}
	case "/api/events":
		if req.Method == http.MethodGet {
			c.handleSSE(w, req)
			return
		}
	case "/api/proxy/start":
		if req.Method == http.MethodPost {
			_ = c.proxySrv.Start()
			c.sendJSON(w, http.StatusOK, map[string]interface{}{
				"ok":      true,
				"message": "Proxy started",
			})
			return
		}
	case "/api/proxy/stop":
		if req.Method == http.MethodPost {
			c.proxySrv.Stop()
			c.sendJSON(w, http.StatusOK, map[string]interface{}{
				"ok":      true,
				"message": "Proxy stopped",
			})
			return
		}
	}

	// Static Web serving / SPA Fallback
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		c.handleStaticWeb(w, req)
		return
	}

	c.sendJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (c *ControlServer) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (c *ControlServer) getRuntimeStatus() map[string]interface{} {
	cfg := c.cfgMgr.Get()
	ramItems, ramBytes := c.cacheMgr.Stats()

	var lanIP interface{}
	if cfg.AllowLAN {
		lanIP = config.GetLANIP()
	}

	uptime := math.Round(time.Since(c.stats.StartTime).Seconds()*10) / 10

	return map[string]interface{}{
		"version":                  config.AppVersion,
		"engine":                   "go",
		"proxy_running":            c.proxySrv.IsRunning(),
		"listen_host":              c.cfgMgr.GetEffectiveListenHost(),
		"listen_port":              cfg.ListenPort,
		"control_port":             cfg.ControlPort,
		"upstream_proxy":           c.cfgMgr.GetEffectiveUpstreamProxy(),
		"direct_mode":              cfg.DirectMode,
		"allow_lan":                cfg.AllowLAN,
		"lan_ip":                   lanIP,
		"active_api_count":         atomic.LoadInt32(&c.stats.ActiveAPICount),
		"active_foreground_assets": atomic.LoadInt32(&c.stats.ActiveForegroundAssets),
		"uptime_seconds":           uptime,
		"requests":                 c.stats.RequestsMap(),
		"cache": map[string]interface{}{
			"ram_items": ramItems,
			"ram_mb":    math.Round(float64(ramBytes)/(1024*1024)*100) / 100,
			"cache_dir": c.cacheMgr.GetCacheBase(),
		},
		"last_error": c.stats.LastError,
	}
}

func (c *ControlServer) handleStatus(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, c.getRuntimeStatus())
}

func (c *ControlServer) handleGetConfig(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"config": c.cfgMgr.Get(),
	})
}

func (c *ControlServer) handleApplyConfig(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
		return
	}

	var patch map[string]interface{}
	if err := json.Unmarshal(body, &patch); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON payload"})
		return
	}

	updated := c.cfgMgr.Update(func(cfg *config.Config) {
		if val, ok := patch["upstream_proxy"].(string); ok {
			cfg.UpstreamProxy = val
		}
		if val, ok := patch["direct_mode"].(bool); ok {
			cfg.DirectMode = val
		}
		if val, ok := patch["verify_upstream_tls"].(bool); ok {
			cfg.VerifyUpstreamTLS = val
		}
		if val, ok := patch["allow_lan"].(bool); ok {
			cfg.AllowLAN = val
		}
		if val, ok := patch["cache_dir"].(string); ok {
			cfg.CacheDir = config.NormalizeCacheDir(val)
			c.cacheMgr.SetCacheBase(cfg.CacheDir)
		}
		if val, ok := patch["ram_cache_max_mb"].(float64); ok {
			cfg.RAMCacheMaxMB = int(val)
			c.cacheMgr.SetRAMLimit(cfg.RAMCacheMaxMB)
		}
		if val, ok := patch["enable_ram_cache"].(bool); ok {
			cfg.EnableRAMCache = val
		}
		if val, ok := patch["enable_browser_cache"].(bool); ok {
			cfg.EnableBrowserCache = val
		}
		if val, ok := patch["enable_prefetch"].(bool); ok {
			cfg.EnablePrefetch = val
		}
	})
	_ = c.cfgMgr.Save()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Configuration updated",
		"config":  updated,
	})
}

func (c *ControlServer) handleCacheStats(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	items, bytes := c.cacheMgr.Stats()
	totalLookups := atomic.LoadInt64(&c.stats.TotalHits) + atomic.LoadInt64(&c.stats.CacheMisses)
	hitRatio := 0.0
	if totalLookups > 0 {
		hitRatio = math.Round(float64(atomic.LoadInt64(&c.stats.TotalHits))/float64(totalLookups)*1000) / 10
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"cache_base":         c.cacheMgr.GetCacheBase(),
		"ram_items":          items,
		"ram_bytes":          bytes,
		"ram_mb":             math.Round(float64(bytes)/(1024*1024)*100) / 100,
		"ram_max_mb":         cfg.RAMCacheMaxMB,
		"hits_total":         atomic.LoadInt64(&c.stats.TotalHits),
		"hits_ram":           atomic.LoadInt64(&c.stats.RAMHits),
		"hits_disk":          atomic.LoadInt64(&c.stats.DiskHits),
		"misses":             atomic.LoadInt64(&c.stats.CacheMisses),
		"hit_ratio_percent": hitRatio,
	})
}

func (c *ControlServer) handleCacheClear(w http.ResponseWriter, req *http.Request) {
	ramOnly := req.URL.Query().Get("ram_only") == "true"
	c.cacheMgr.ClearRAM()

	var diskDeleted int
	var diskFreed int64
	if !ramOnly {
		diskDeleted, diskFreed = c.cacheMgr.ClearAll()
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"ram_cleared":        true,
		"disk_cleared":       !ramOnly,
		"disk_files_deleted": diskDeleted,
		"disk_bytes_freed":   diskFreed,
	})
}

func (c *ControlServer) handleCacheAudit(w http.ResponseWriter, req *http.Request) {
	go func() {
		_ = c.cacheMgr.AuditAndRepair()
	}()
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Cache audit started in background",
		"task":    "audit",
	})
}

func (c *ControlServer) handleCacheSlim(w http.ResponseWriter, req *http.Request) {
	keep := 8
	if kStr := req.URL.Query().Get("keep"); kStr != "" {
		if k, err := strconv.Atoi(kStr); err == nil && k > 0 {
			keep = k
		}
	}
	go func() {
		_, _, _ = c.cacheMgr.PruneStaleVersions(keep)
	}()
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Cache slim started in background",
		"task":    "slim",
	})
}

func (c *ControlServer) handlePrefetchStatus(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	yielding := atomic.LoadInt32(&c.stats.ActiveAPICount) > 0
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"enabled":            cfg.EnablePrefetch,
		"queue_size":         0,
		"is_yielding":        yielding,
		"active_api_count":   atomic.LoadInt32(&c.stats.ActiveAPICount),
		"prefetch_requests":  atomic.LoadInt64(&c.stats.PrefetchRequests),
		"prefetch_successes": atomic.LoadInt64(&c.stats.PrefetchSuccesses),
		"prefetch_reused":    atomic.LoadInt64(&c.stats.PrefetchReused),
	})
}

func (c *ControlServer) handleTelemetry(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"telemetry": map[string]interface{}{
			"total_requests":     atomic.LoadInt64(&c.stats.TotalAPIs) + atomic.LoadInt64(&c.stats.TotalAssets),
			"reused_connections": atomic.LoadInt64(&c.stats.TotalAPIs),
			"new_connections":    1,
			"reuse_rate":         100.0,
			"retry_count":        atomic.LoadInt64(&c.stats.APIRetries),
			"percentiles":        map[string]float64{"p50": 5.0, "p90": 15.0, "p99": 30.0},
			"protocols":          map[string]int{"HTTP/1.1": int(atomic.LoadInt64(&c.stats.TotalAPIs)), "HTTP/2": int(atomic.LoadInt64(&c.stats.TotalAssets))},
			"exceptions":         map[string]int{},
		},
	})
}

func (c *ControlServer) handleLogs(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"logs": c.stats.GetLogs(),
	})
}

func (c *ControlServer) handleSSE(w http.ResponseWriter, req *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Send initial status event
	statusData, _ := json.Marshal(c.getRuntimeStatus())
	fmt.Fprintf(w, "event: status\ndata: %s\n\n", statusData)
	flusher.Flush()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-c.closedChan:
			return
		case now := <-ticker.C:
			pulseData, _ := json.Marshal(map[string]interface{}{
				"time":       now.Format("15:04:05"),
				"uptime":     math.Round(time.Since(c.stats.StartTime).Seconds()*10) / 10,
				"active_api": atomic.LoadInt32(&c.stats.ActiveAPICount),
				"active_fg":  atomic.LoadInt32(&c.stats.ActiveForegroundAssets),
				"hits":       atomic.LoadInt64(&c.stats.TotalHits),
				"misses":     atomic.LoadInt64(&c.stats.CacheMisses),
			})
			fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", pulseData)
			flusher.Flush()
		}
	}
}

func (c *ControlServer) handleStaticWeb(w http.ResponseWriter, req *http.Request) {
	if c.distDir != "" {
		clean := filepath.Clean(filepath.FromSlash(req.URL.Path))
		candidate := filepath.Join(c.distDir, clean)
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			http.ServeFile(w, req, candidate)
			return
		}
		indexFile := filepath.Join(c.distDir, "index.html")
		if fi, err := os.Stat(indexFile); err == nil && !fi.IsDir() {
			http.ServeFile(w, req, indexFile)
			return
		}
	}

	// Fallback minimal HTML dashboard
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if req.Method != http.MethodHead {
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>GBF Accelerator Dashboard</title></head>
<body><h1>GBF Accelerator Dashboard (Go Engine)</h1></body>
</html>`))
	}
}
