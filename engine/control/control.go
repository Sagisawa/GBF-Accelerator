package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/desktop"
	"gbf-proxy/firewall"
	"gbf-proxy/proxy"
	"gbf-proxy/res"
	"gbf-proxy/startup"
	"gbf-proxy/sysproxy"
	"gbf-proxy/telemetry"
	"gbf-proxy/ui"
	"gbf-proxy/updater"
)

type ControlServer struct {
	cfgMgr     *config.Manager
	certMgr    *cert.Manager
	cacheMgr   *cache.Manager
	proxySrv   *proxy.ProxyServer
	stats      *telemetry.Stats
	server     *http.Server
	listener    net.Listener
	listenerGen uint64
	mu          sync.RWMutex
	applyMu     sync.Mutex
	distDir    string
	running    bool
	closedChan chan struct{}
	quitFunc   func()

	// Background Cache Task state
	cacheTaskMu     sync.Mutex
	isAuditing      bool
	isSlimming      bool
	auditProgress   cache.AuditProgress
	slimProgress    cache.SlimProgress
	lastAuditResult map[string]interface{}
	lastSlimResult  map[string]interface{}
	cacheCancelCh   chan struct{}


	// Background Download state
	dlMu       sync.Mutex
	dlActive   bool
	dlProgress int64
	dlTotal    int64
	dlPercent  float64
	dlDone     bool
	dlDest     string
	dlError    string
	dlVersion  string
	dlSHA256   string
	dlManaged  bool
	dlApplying bool
	dlCancelFn context.CancelFunc

	// Broadcasters for SSE custom events
	sseMu      sync.RWMutex
	sseClients []chan []byte

	// Side-effect hooks for system proxy and startup registration (mockable in tests)
	enablePACProxyFn   func(string) error
	disablePACProxyFn  func(bool) error
	setStartupEnabledFn func(bool) error
}

func NewControlServer(cfgMgr *config.Manager, certMgr *cert.Manager, cacheMgr *cache.Manager, proxySrv *proxy.ProxyServer, stats *telemetry.Stats) *ControlServer {
	// Locate web/dist
	distDir := ""
	candidates := []string{
		filepath.Join("web", "dist"),
		filepath.Join("..", "web", "dist"),
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "web", "dist"),
			filepath.Join(exeDir, "..", "web", "dist"),
			filepath.Join(exeDir, "..", "..", "web", "dist"),
		)
	}
	for _, candidate := range candidates {
		if fi, err := os.Stat(candidate); err == nil && fi.IsDir() {
			distDir, _ = filepath.Abs(candidate)
			break
		}
	}

	return &ControlServer{
		cfgMgr:              cfgMgr,
		certMgr:             certMgr,
		cacheMgr:            cacheMgr,
		proxySrv:            proxySrv,
		stats:               stats,
		distDir:             distDir,
		closedChan:          make(chan struct{}),
		enablePACProxyFn:    sysproxy.EnablePACProxy,
		disablePACProxyFn:   sysproxy.DisablePACProxy,
		setStartupEnabledFn: startup.SetStartupEnabled,
	}
}

func (c *ControlServer) SetQuitFunc(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quitFunc = fn
}

func (c *ControlServer) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return nil
	}

	cfg := c.cfgMgr.Get()
	addr := fmt.Sprintf("127.0.0.1:%d", cfg.ControlPort)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to bind control server on %s: %w", addr, err)
	}

	c.listener = ln
	c.listenerGen++
	c.running = true
	c.closedChan = make(chan struct{})

	c.startServe(ln, c.listenerGen)
	return nil
}

func (c *ControlServer) startServe(ln net.Listener, gen uint64) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.handleRoute)

	srv := &http.Server{
		Handler: mux,
	}
	c.server = srv

	go func() {
		_ = srv.Serve(ln)
		c.mu.RLock()
		running := c.running
		currentGen := c.listenerGen
		c.mu.RUnlock()
		if !running || gen != currentGen {
			return
		}
	}()
}

func (c *ControlServer) ReloadListener(newPort int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return nil
	}

	newAddr := fmt.Sprintf("127.0.0.1:%d", newPort)
	if c.listener != nil && c.listener.Addr().String() == newAddr {
		return nil
	}

	// 1. Bind new listener on the new port FIRST without closing old listener.
	// If binding fails, the old server and in-flight requests remain completely intact.
	newLn, err := net.Listen("tcp", newAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", newAddr, err)
	}

	oldLn := c.listener
	oldSrv := c.server

	// 2. Set new listener and start new http.Server on the new port
	c.listener = newLn
	c.listenerGen++
	c.startServe(newLn, c.listenerGen)

	// 3. Gracefully shut down old server in background so in-flight HTTP requests
	// (specifically POST /api/config/apply that initiated this reload) can finish sending
	// their response to the browser before the old socket is terminated.
	if oldLn != nil {
		_ = oldLn.Close() // stop accepting new connections on the old port
	}
	if oldSrv != nil {
		go func(srv *http.Server) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}(oldSrv)
	}

	if c.stats != nil {
		c.stats.Log("INFO", fmt.Sprintf("[CONTROL] 管理控制面已动态迁移至: %s (generation: %d)", newAddr, c.listenerGen))
	}
	return nil
}

func (c *ControlServer) ListenerAddr() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.listener != nil {
		return c.listener.Addr().String()
	}
	return ""
}

func (c *ControlServer) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return
	}
	c.running = false
	c.listenerGen++
	if c.server != nil {
		_ = c.server.Close()
		c.server = nil
	}
	if c.listener != nil {
		_ = c.listener.Close()
		c.listener = nil
	}
	select {
	case <-c.closedChan:
	default:
		close(c.closedChan)
	}
}

func isAllowedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	h := u.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func isAllowedControlHost(reqHost string, allowLAN bool) bool {
	if reqHost == "" {
		return false
	}
	host := reqHost
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return true
	}
	if allowLAN {
		lanIP := strings.ToLower(config.GetLANIP())
		if lanIP != "" && host == lanIP {
			return true
		}
	}
	return false
}

func (c *ControlServer) handleRoute(w http.ResponseWriter, req *http.Request) {
	if !isAllowedControlHost(req.Host, c.cfgMgr.Get().AllowLAN) {
		http.Error(w, "Forbidden: Invalid Host header (DNS Rebinding Protection)", http.StatusForbidden)
		return
	}

	origin := req.Header.Get("Origin")
	if origin != "" {
		if !isAllowedOrigin(origin) {
			http.Error(w, "Forbidden: Cross-Origin Request Blocked", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}

	// CORS Preflight
	if req.Method == http.MethodOptions {
		if origin != "" && !isAllowedOrigin(origin) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}

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
	case "/api/cache/open-folder":
		if req.Method == http.MethodPost {
			c.handleOpenCacheFolder(w, req)
			return
		}
	case "/api/utils/browse-dir":
		if req.Method == http.MethodPost {
			c.handleBrowseDir(w, req)
			return
		}
	case "/api/cache/audit":
		if req.Method == http.MethodPost {
			c.handleCacheAudit(w, req)
			return
		}
	case "/ca.crt", "/ca.pem":
		if (req.Method == http.MethodGet || req.Method == http.MethodHead) && c.certMgr != nil {
			caBytes := c.certMgr.GetCAPEM()
			w.Header().Set("Content-Type", "application/x-x509-ca-cert")
			w.Header().Set("Content-Length", strconv.Itoa(len(caBytes)))
			w.Header().Set("Content-Disposition", "attachment; filename=\"gbf_ca.crt\"")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			if req.Method != http.MethodHead {
				_, _ = w.Write(caBytes)
			}
			return
		}
	case "/proxy.pac", "/pac":
		if req.Method == http.MethodGet || req.Method == http.MethodHead {
			cfg := c.cfgMgr.Get()
			h := "127.0.0.1"
			pacText := proxy.GetPAC(h, cfg.ListenPort)
			w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
			w.Header().Set("Content-Length", strconv.Itoa(len(pacText)))
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			w.WriteHeader(http.StatusOK)
			if req.Method != http.MethodHead {
				_, _ = w.Write([]byte(pacText))
			}
			return
		}
	case "/api/cache/slim", "/api/cache/prune-stale":
		if req.Method == http.MethodPost {
			c.handleCacheSlim(w, req)
			return
		}
	case "/api/cache/task-status":
		if req.Method == http.MethodGet {
			c.handleCacheTaskStatus(w, req)
			return
		}
	case "/api/cache/cancel-task":
		if req.Method == http.MethodPost {
			c.handleCacheCancelTask(w, req)
			return
		}
	case "/api/cache/detect-acgpower":
		if req.Method == http.MethodGet || req.Method == http.MethodPost {
			c.handleDetectACGPower(w, req)
			return
		}
	case "/api/upstream/detect":
		if req.Method == http.MethodGet || req.Method == http.MethodPost {
			c.handleDetectUpstream(w, req)
			return
		}
	case "/api/upstream/status":
		if req.Method == http.MethodGet {
			c.handleUpstreamStatus(w, req)
			return
		}
	case "/api/latency-test", "/api/utils/test-latency":
		if req.Method == http.MethodGet || req.Method == http.MethodPost {
			c.handleLatencyTest(w, req)
			return
		}
	case "/api/update/check", "/api/updater/check":
		if req.Method == http.MethodGet {
			c.handleUpdateCheck(w, req)
			return
		}
	case "/api/update/download", "/api/updater/download":
		if req.Method == http.MethodPost {
			c.handleUpdateDownload(w, req)
			return
		}
	case "/api/update/download-status", "/api/updater/download-status":
		if req.Method == http.MethodGet {
			c.handleUpdateDownloadStatus(w, req)
			return
		}
	case "/api/update/download-cancel", "/api/updater/download-cancel":
		if req.Method == http.MethodPost {
			c.handleUpdateDownloadCancel(w, req)
			return
		}
	case "/api/update/apply", "/api/updater/apply":
		if req.Method == http.MethodPost {
			c.handleUpdateApply(w, req)
			return
		}
	case "/api/update/open-folder", "/api/updater/open-folder":
		if req.Method == http.MethodPost || req.Method == http.MethodGet {
			c.handleUpdateOpenFolder(w, req)
			return
		}
	case "/api/firewall/status":
		if req.Method == http.MethodGet {
			c.handleFirewallStatus(w, req)
			return
		}
	case "/api/firewall/apply":
		if req.Method == http.MethodPost {
			c.handleFirewallApply(w, req)
			return
		}
	case "/api/cert/status":
		if req.Method == http.MethodGet {
			c.handleCertStatus(w, req)
			return
		}
	case "/api/cert/install":
		if req.Method == http.MethodPost {
			c.handleCertInstall(w, req)
			return
		}
	case "/api/cert/uninstall":
		if req.Method == http.MethodPost {
			c.handleCertUninstall(w, req)
			return
		}
	case "/api/cert/clean-legacy":
		if req.Method == http.MethodPost {
			c.handleCertCleanLegacy(w, req)
			return
		}
	case "/api/startup/status":
		if req.Method == http.MethodGet {
			c.handleStartupStatus(w, req)
			return
		}
	case "/api/startup/set":
		if req.Method == http.MethodPost {
			c.handleStartupSet(w, req)
			return
		}
	case "/api/sysproxy/enable":
		if req.Method == http.MethodPost {
			c.handleSysProxyEnable(w, req)
			return
		}
	case "/api/sysproxy/disable":
		if req.Method == http.MethodPost {
			c.handleSysProxyDisable(w, req)
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
			if c.proxySrv == nil {
				c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok":      false,
					"error":   "proxy server not initialized",
					"message": "Failed to start proxy: server not initialized",
					"status":  c.getRuntimeStatus(),
				})
				return
			}
			if err := c.proxySrv.Start(); err != nil {
				c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok":      false,
					"error":   err.Error(),
					"message": fmt.Sprintf("Failed to start proxy: %v", err),
					"status":  c.getRuntimeStatus(),
				})
				return
			}
			c.sendJSON(w, http.StatusOK, map[string]interface{}{
				"ok":      true,
				"message": "Proxy started",
				"status":  c.getRuntimeStatus(),
			})
			return
		}
	case "/api/proxy/stop":
		if req.Method == http.MethodPost {
			if c.proxySrv != nil {
				c.proxySrv.Stop()
			}
			c.sendJSON(w, http.StatusOK, map[string]interface{}{
				"ok":      true,
				"message": "Proxy stopped",
				"status":  c.getRuntimeStatus(),
			})
			return
		}
	case "/api/app/quit", "/api/app/exit":
		if req.Method == http.MethodPost {
			c.handleAppQuit(w, req)
			return
		}
	}

	// Reject unmatched API routes with 404 JSON
	if strings.HasPrefix(path, "/api/") {
		c.sendJSON(w, http.StatusNotFound, map[string]interface{}{"error": "not found", "path": path})
		return
	}

	// Static Web serving / SPA Fallback
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		c.handleStaticWeb(w, req)
		return
	}

	c.sendJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func (c *ControlServer) handleAppQuit(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "GBF-Accelerator 正在安全退出...",
	})
	go func() {
		time.Sleep(300 * time.Millisecond)
		c.mu.RLock()
		q := c.quitFunc
		c.mu.RUnlock()
		if q != nil {
			q()
		} else {
			os.Exit(0)
		}
	}()
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

	proxyRunning := false
	if c.proxySrv != nil {
		proxyRunning = c.proxySrv.IsRunning()
	}

	caInstalled := false
	caFingerprint := ""
	if c.certMgr != nil {
		caInstalled = c.certMgr.IsInstalled()
		caFingerprint = c.certMgr.GetFingerprintSHA256()
	}

	c.cacheTaskMu.Lock()
	isAuditing := c.isAuditing
	isSlimming := c.isSlimming
	c.cacheTaskMu.Unlock()

	effectiveUpstreamProxy := c.cfgMgr.GetEffectiveUpstreamProxy()
	if c.proxySrv != nil {
		effectiveUpstreamProxy = c.proxySrv.GetEffectiveUpstreamProxy()
	}

	return map[string]interface{}{
		"version":                  config.AppVersion,
		"engine":                   "go",
		"proxy_running":            proxyRunning,
		"listen_host":              c.cfgMgr.GetEffectiveListenHost(),
		"listen_port":              cfg.ListenPort,
		"control_port":             cfg.ControlPort,
		"upstream_proxy":           effectiveUpstreamProxy,
		"direct_mode":              cfg.DirectMode,
		"allow_lan":                cfg.AllowLAN,
		"lan_ip":                   lanIP,
		"system_proxy_enabled":     sysproxy.IsPACProxyEnabled(cfg.ListenPort),
		"system_proxy_conflict":    sysproxy.CheckProxyConflict(cfg.ListenPort),
		"ca_installed":             caInstalled,
		"ca_fingerprint":           caFingerprint,
		"startup_enabled":          startup.IsStartupEnabled(),
		"startup_supported":        startup.IsStartupSupported(),
		"is_auditing_cache":        isAuditing,
		"is_slimming_cache":        isSlimming,
		"active_api_count":         c.stats.ActiveAPICount.Load(),
		"active_foreground_assets": c.stats.ActiveForegroundAssets.Load(),
		"uptime_seconds":           uptime,
		"requests":                 c.stats.RequestsMap(),
		"telemetry":                c.getTelemetrySummary(),
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

	// Serialize the full apply/commit/rebind/rollback transaction. Without this,
	// overlapping ApplyConfig requests can roll each other back from stale candidates.
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	candidate := c.cfgMgr.Candidate()
	oldCandidate := candidate

	if val, ok := patch["listen_port"].(float64); ok && val > 0 && val < 65536 {
		candidate.ListenPort = int(val)
	} else if val, ok := patch["port"].(float64); ok && val > 0 && val < 65536 {
		candidate.ListenPort = int(val)
	}
	if val, ok := patch["control_port"].(float64); ok && val > 0 && val < 65536 {
		candidate.ControlPort = int(val)
	}
	if val, ok := patch["upstream_proxy"].(string); ok {
			candidate.UpstreamProxy = strings.TrimSpace(val)
	}

	if val, ok := patch["backup_upstream_proxy"].(string); ok {
			backup := strings.TrimSpace(val)
			if backup != "" && !isSupportedBackupProxyURL(backup) {
				c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid backup_upstream_proxy"})
				return
			}
			candidate.BackupUpstreamProxy = backup
	}
	if val, ok := patch["enable_upstream_failover"].(bool); ok {
		candidate.EnableUpstreamFailover = val
	}
	if val, ok := patch["upstream_failover_threshold_ms"].(float64); ok {
		candidate.UpstreamFailoverThresholdMS = int(val)
	}
	if val, ok := patch["upstream_failover_consecutive_failures"].(float64); ok {
		candidate.UpstreamFailoverConsecutiveFailures = int(val)
	}
	if val, ok := patch["upstream_failover_cooldown_seconds"].(float64); ok {
		candidate.UpstreamFailoverCooldownSeconds = int(val)
	}
	if val, ok := patch["upstream_failover_auto_recover"].(bool); ok {
		candidate.UpstreamFailoverAutoRecover = val
	}
	if val, ok := patch["direct_mode"].(bool); ok {
		candidate.DirectMode = val
	}
	if val, ok := patch["verify_upstream_tls"].(bool); ok {
		candidate.VerifyUpstreamTLS = val
	}
	if val, ok := patch["allow_lan"].(bool); ok {
		candidate.AllowLAN = val
	}
	if val, ok := patch["cache_dir"].(string); ok {
		if strings.EqualFold(val, "auto") {
			if detected := config.AutoDetectACGPowerCache(); detected != "" {
				val = detected
			}
		}
		candidate.CacheDir = config.NormalizeCacheDir(val)
	}
	if val, ok := patch["ram_cache_max_mb"].(float64); ok {
		candidate.RAMCacheMaxMB = int(val)
	}
	if val, ok := patch["enable_ram_cache"].(bool); ok {
		candidate.EnableRAMCache = val
	}
	if val, ok := patch["enable_browser_cache"].(bool); ok {
		candidate.EnableBrowserCache = val
	}
	if val, ok := patch["enable_prefetch"].(bool); ok {
		candidate.EnablePrefetch = val
	}
	if val, ok := patch["enable_auto_repair"].(bool); ok {
		candidate.EnableAutoRepair = val
	}
	if val, ok := patch["enable_ram_warmup"].(bool); ok {
		candidate.EnableRAMWarmup = val
	}
	if val, ok := patch["auto_system_proxy"].(bool); ok {
		candidate.AutoSystemProxy = val
	} else if val, ok := patch["auto_pac"].(bool); ok {
		candidate.AutoSystemProxy = val
	}
	if val, ok := patch["auto_start"].(bool); ok {
		candidate.AutoStart = val
	}
	if val, ok := patch["auto_check_update"].(bool); ok {
		candidate.AutoCheckUpdate = val
	}
	if val, ok := patch["shimakaze_mode"].(bool); ok {
		candidate.ShimakazeMode = val
	}
	if val, ok := patch["api_max_connections"].(float64); ok && val > 0 {
		candidate.APIMaxConnections = int(val)
	}
	if val, ok := patch["api_max_keepalive"].(float64); ok && val >= 0 {
		candidate.APIMaxKeepalive = int(val)
	}
	if val, ok := patch["api_keepalive_expiry"].(float64); ok && val > 0 {
		candidate.APIKeepaliveExpiry = val
	}
	if val, ok := patch["asset_max_connections"].(float64); ok && val > 0 {
		candidate.AssetMaxConnections = int(val)
	}
	if val, ok := patch["asset_max_keepalive"].(float64); ok && val >= 0 {
		candidate.AssetMaxKeepalive = int(val)
	}
	if val, ok := patch["asset_keepalive_expiry"].(float64); ok && val > 0 {
		candidate.AssetKeepaliveExpiry = val
	}
	if val, ok := patch["ram_warmup_max_items"].(float64); ok && val > 0 {
		candidate.RAMWarmupMaxItems = int(val)
	}
	if val, ok := patch["enable_api_telemetry"].(bool); ok {
		candidate.EnableAPITelemetry = val
	}
	if val, ok := patch["clean_zombies"].(bool); ok {
		candidate.CleanZombies = val
	}
	if val, ok := patch["window_width"].(float64); ok && val >= 400 {
		candidate.WindowWidth = int(val)
	}
	if val, ok := patch["window_height"].(float64); ok && val >= 400 {
		candidate.WindowHeight = int(val)
	}
	if val, ok := patch["window_maximized"].(bool); ok {
		candidate.WindowMaximized = val
	}

	proxyNeedsReload := c.proxySrv != nil &&
		(candidate.AllowLAN != oldCandidate.AllowLAN || candidate.ListenPort != oldCandidate.ListenPort)
	controlNeedsReload := candidate.ControlPort != oldCandidate.ControlPort

	// 1. Persist and commit configuration BEFORE mutating listeners.
	// A failed disk write must leave all runtime listeners untouched.
	if err := c.cfgMgr.Commit(candidate); err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("配置持久化失败: %v", err),
		})
		return
	}

	proxyRebound := false
	controlRebound := false

	rollbackRuntime := func() error {
		var errs []string
		if controlRebound {
			if err := c.ReloadListener(oldCandidate.ControlPort); err != nil {
				errs = append(errs, fmt.Sprintf("control rollback: %v", err))
			}
		}
		if proxyRebound && c.proxySrv != nil {
			if err := c.proxySrv.ReloadListener(oldCandidate.GetEffectiveListenHost(), oldCandidate.ListenPort); err != nil {
				errs = append(errs, fmt.Sprintf("proxy rollback: %v", err))
			}
		}
		if err := c.cfgMgr.Commit(oldCandidate); err != nil {
			errs = append(errs, fmt.Sprintf("config rollback: %v", err))
		} else {
			// Config callbacks restore connection/runtime switches, but CacheDir and
			// RAM limit are managed explicitly by this handler and therefore must be
			// restored here as well.
			if c.cacheMgr != nil {
				if candidate.CacheDir != oldCandidate.CacheDir {
					c.cacheMgr.SetCacheBase(oldCandidate.CacheDir)
				}
				if candidate.RAMCacheMaxMB != oldCandidate.RAMCacheMaxMB {
					c.cacheMgr.SetRAMLimit(oldCandidate.RAMCacheMaxMB)
				}
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}

	restoreSystemSideEffects := func() error {
		var errs []string
		if candidate.AutoSystemProxy != oldCandidate.AutoSystemProxy || candidate.ListenPort != oldCandidate.ListenPort {
			var err error
			if oldCandidate.AutoSystemProxy {
				err = c.enablePACProxyFn(fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", oldCandidate.ListenPort))
			} else {
				err = c.disablePACProxyFn(false)
			}
			if err != nil {
				errs = append(errs, fmt.Sprintf("system proxy restore: %v", err))
			}
		}
		if candidate.AutoStart != oldCandidate.AutoStart {
			if err := c.setStartupEnabledFn(oldCandidate.AutoStart); err != nil {
				errs = append(errs, fmt.Sprintf("startup restore: %v", err))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}

	// 2. Apply listener changes only after configuration persistence succeeds.
	if proxyNeedsReload {
		newHost := candidate.GetEffectiveListenHost()
		if err := c.proxySrv.ReloadListener(newHost, candidate.ListenPort); err != nil {
			if rollbackErr := c.cfgMgr.Commit(oldCandidate); rollbackErr != nil {
				c.stats.Log("ERROR", fmt.Sprintf("[CONFIG] 严重：代理监听重载失败且配置回滚也失败: reload=%v rollback=%v", err, rollbackErr))
			}
			c.sendJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("代理监听重载失败: %v", err),
			})
			return
		}
		proxyRebound = true
	}

	if controlNeedsReload {
		if err := c.ReloadListener(candidate.ControlPort); err != nil {
			var rollbackErr error
			if proxyRebound && c.proxySrv != nil {
				rollbackErr = c.proxySrv.ReloadListener(oldCandidate.GetEffectiveListenHost(), oldCandidate.ListenPort)
			}
			if cfgErr := c.cfgMgr.Commit(oldCandidate); rollbackErr == nil {
				rollbackErr = cfgErr
			} else if cfgErr != nil {
				rollbackErr = fmt.Errorf("listener rollback: %v; config rollback: %v", rollbackErr, cfgErr)
			}
			if rollbackErr != nil {
				c.stats.Log("ERROR", fmt.Sprintf("[CONFIG] 严重：控制监听重载失败，回滚存在错误: %v", rollbackErr))
			}
			c.sendJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("控制端口重载失败: %v", err),
			})
			return
		}
		controlRebound = true
	}

	// 3. Apply non-listener runtime state
	if candidate.CacheDir != oldCandidate.CacheDir {
		c.cacheMgr.SetCacheBase(candidate.CacheDir)
	}
	if candidate.RAMCacheMaxMB != oldCandidate.RAMCacheMaxMB {
		c.cacheMgr.SetRAMLimit(candidate.RAMCacheMaxMB)
	}

	if candidate.AutoSystemProxy != oldCandidate.AutoSystemProxy || candidate.ListenPort != oldCandidate.ListenPort {
		var err error
		if candidate.AutoSystemProxy {
			err = c.enablePACProxyFn(fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", candidate.ListenPort))
		} else {
			err = c.disablePACProxyFn(false)
		}
		if err != nil {
			restoreErr := restoreSystemSideEffects()
			runtimeRollbackErr := rollbackRuntime()
			if restoreErr != nil || runtimeRollbackErr != nil {
				c.stats.Log("ERROR", fmt.Sprintf("[CONFIG] 严重：系统代理应用失败且回滚存在错误: apply=%v restore=%v runtime=%v", err, restoreErr, runtimeRollbackErr))
			}
			c.sendJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("系统代理设置失败: %v", err),
			})
			return
		}
	}

	if candidate.AutoStart != oldCandidate.AutoStart {
		if err := c.setStartupEnabledFn(candidate.AutoStart); err != nil {
			restoreErr := restoreSystemSideEffects()
			runtimeRollbackErr := rollbackRuntime()
			if restoreErr != nil || runtimeRollbackErr != nil {
				c.stats.Log("ERROR", fmt.Sprintf("[CONFIG] 严重：开机启动设置失败且回滚存在错误: apply=%v restore=%v runtime=%v", err, restoreErr, runtimeRollbackErr))
			}
			c.sendJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("开机启动设置失败: %v", err),
			})
			return
		}
	}

	if err := res.EnsureHelperFiles(config.GetBaseDir(), candidate.ListenPort); err != nil {
		c.stats.Log("WARN", fmt.Sprintf("[RES] 更新辅助文件失败: %v", err))
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"message":     "Configuration updated",
		"config":      candidate,
		"control_url": fmt.Sprintf("http://127.0.0.1:%d", candidate.ControlPort),
	})
}

func (c *ControlServer) handleCacheStats(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	items, bytes := c.cacheMgr.Stats()
	totalLookups := c.stats.TotalHits.Load() + c.stats.CacheMisses.Load()
	hitRatio := 0.0
	if totalLookups > 0 {
		hitRatio = math.Round(float64(c.stats.TotalHits.Load())/float64(totalLookups)*1000) / 10
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"cache_base":         c.cacheMgr.GetCacheBase(),
		"ram_items":          items,
		"ram_bytes":          bytes,
		"ram_mb":             math.Round(float64(bytes)/(1024*1024)*100) / 100,
		"ram_max_mb":         cfg.RAMCacheMaxMB,
		"hits_total":         c.stats.TotalHits.Load(),
		"hits_ram":           c.stats.RAMHits.Load(),
		"hits_disk":          c.stats.DiskHits.Load(),
		"misses":             c.stats.CacheMisses.Load(),
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

func (c *ControlServer) handleOpenCacheFolder(w http.ResponseWriter, req *http.Request) {
	base := c.cacheMgr.GetCacheBase()
	_ = os.MkdirAll(base, 0755)
	go func() {
		_ = desktop.OpenFolder(base)
	}()
	c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "path": base})
}

func (c *ControlServer) handleBrowseDir(w http.ResponseWriter, req *http.Request) {
	chosen, _ := desktop.ChooseFolder("选择 GBF 本地静态缓存保存目录")
	c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "path": chosen})
}

func (c *ControlServer) broadcastEvent(event string, data interface{}) {
	payload, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, payload))

	c.sseMu.RLock()
	defer c.sseMu.RUnlock()
	for _, ch := range c.sseClients {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (c *ControlServer) handleCacheAudit(w http.ResponseWriter, req *http.Request) {
	c.cacheTaskMu.Lock()
	if c.isAuditing || c.isSlimming {
		c.cacheTaskMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "当前有缓存维护任务（体检或瘦身）正在进行中，请稍候完成",
		})
		return
	}
	c.isAuditing = true
	c.auditProgress = cache.AuditProgress{}
	c.cacheCancelCh = make(chan struct{})
	cancelCh := c.cacheCancelCh
	c.cacheTaskMu.Unlock()

	c.stats.Log("INFO", "[CACHE-AUDIT] Starting static cache audit and repair task...")

	go func() {
		defer func() {
			c.cacheTaskMu.Lock()
			c.isAuditing = false
			c.cacheTaskMu.Unlock()
		}()

		res := c.cacheMgr.AuditAndRepairWithProgress(func(p cache.AuditProgress) {
			c.cacheTaskMu.Lock()
			c.auditProgress = p
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("audit_progress", p)
		}, cancelCh)

		c.cacheTaskMu.Lock()
		c.isAuditing = false
		c.lastAuditResult = res
		c.cacheTaskMu.Unlock()
		c.broadcastEvent("audit_done", res)

		c.stats.Log("INFO", fmt.Sprintf("[CACHE-AUDIT] Completed: scanned %v, healthy %v, repaired/cleaned %v",
			res["scanned"], res["healthy"], res["corrupted"]))
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

	c.cacheTaskMu.Lock()
	if c.isAuditing || c.isSlimming {
		c.cacheTaskMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "当前有缓存维护任务（体检或瘦身）正在进行中，请稍候完成",
		})
		return
	}
	c.isSlimming = true
	c.slimProgress = cache.SlimProgress{}
	c.cacheCancelCh = make(chan struct{})
	cancelCh := c.cacheCancelCh
	c.cacheTaskMu.Unlock()

	c.stats.Log("INFO", fmt.Sprintf("[CACHE-SLIM] Starting cache pruning (keeping newest %d versions)...", keep))

	go func() {
		defer func() {
			c.cacheTaskMu.Lock()
			c.isSlimming = false
			c.cacheTaskMu.Unlock()
		}()

		delDirs, delFiles, freedBytes := c.cacheMgr.PruneStaleVersionsWithProgress(keep, func(p cache.SlimProgress) {
			c.cacheTaskMu.Lock()
			c.slimProgress = p
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("slim_progress", p)
		}, cancelCh)

		freedMB := math.Round(float64(freedBytes)/(1024*1024)*100) / 100
		res := map[string]interface{}{
			"deleted_dirs":  delDirs,
			"deleted_files": delFiles,
			"freed_bytes":   freedBytes,
			"freed_mb":      freedMB,
		}
		c.cacheTaskMu.Lock()
		c.isSlimming = false
		c.lastSlimResult = res
		c.cacheTaskMu.Unlock()
		c.broadcastEvent("slim_done", res)

		c.stats.Log("INFO", fmt.Sprintf("[CACHE-SLIM] Completed: pruned %d version dirs, deleted %d stale files, freed %.2f MB",
			delDirs, delFiles, freedMB))
	}()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Cache slim started in background",
		"task":    "slim",
		"keep":    keep,
	})
}

func (c *ControlServer) handleCacheTaskStatus(w http.ResponseWriter, req *http.Request) {
	c.cacheTaskMu.Lock()
	defer c.cacheTaskMu.Unlock()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                true,
		"is_auditing":       c.isAuditing,
		"is_slimming":       c.isSlimming,
		"audit_progress":    c.auditProgress,
		"slim_progress":     c.slimProgress,
		"last_audit_result": c.lastAuditResult,
		"last_slim_result":  c.lastSlimResult,
	})
}

func (c *ControlServer) handleCacheCancelTask(w http.ResponseWriter, req *http.Request) {
	c.cacheTaskMu.Lock()
	defer c.cacheTaskMu.Unlock()

	if c.cacheCancelCh != nil {
		select {
		case <-c.cacheCancelCh:
		default:
			close(c.cacheCancelCh)
		}
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Active cache task cancellation requested",
	})
}

func (c *ControlServer) handleDetectACGPower(w http.ResponseWriter, req *http.Request) {
	detected := config.AutoDetectACGPowerCache()
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"found": detected != "",
		"path":  detected,
	})
}

func (c *ControlServer) handleDetectUpstream(w http.ResponseWriter, req *http.Request) {
	candidates := config.DetectUpstreamProxies()
	rec := config.AutoDetectUpstreamProxy()
	found := len(candidates) > 0
	primary := ""
	if found {
		primary = candidates[0].URL
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"found":       found,
		"primary":     primary,
		"candidates":  candidates,
		"recommended": rec,
	})
}

func (c *ControlServer) handleUpstreamStatus(w http.ResponseWriter, req *http.Request) {
	if c.proxySrv == nil {
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok": true,
			"status": proxy.UpstreamRuntimeStatus{Active: "primary"},
		})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"status": c.proxySrv.GetUpstreamStatus(),
	})
}

func isSupportedBackupProxyURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.EqualFold(raw, "direct") {
		return true
	}
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return true
	default:
		return false
	}
}

func (c *ControlServer) handleLatencyTest(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	target := "https://game.granbluefantasy.jp/"
	if customTarget := strings.TrimSpace(req.URL.Query().Get("target")); customTarget != "" {
		if u, err := url.Parse(customTarget); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			target = customTarget
		}
	}

	proxyURL := c.cfgMgr.GetEffectiveUpstreamProxy()
	if custom := strings.TrimSpace(req.URL.Query().Get("proxy")); custom != "" {
		if strings.EqualFold(custom, "direct") {
			proxyURL = ""
		} else if u, err := url.Parse(custom); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "socks5") {
			proxyURL = custom
		}
	}

	routeDesc := fmt.Sprintf("经上游代理 %s", proxyURL)
	if proxyURL == "" || cfg.DirectMode && strings.TrimSpace(req.URL.Query().Get("proxy")) == "" {
		routeDesc = "直连模式（不经过上游代理）"
	}

	isFast := req.URL.Query().Get("fast") == "1"
	timeout := 12 * time.Second
	if isFast {
		timeout = 4 * time.Second
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !cfg.VerifyUpstreamTLS || cfg.ShimakazeMode || strings.Contains(proxyURL, ":8099") || strings.Contains(proxyURL, ":8123"),
		},
		DisableKeepAlives: false,
		MaxIdleConns:      5,
		IdleConnTimeout:   30 * time.Second,
	}

	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Probe 1: Cold connect
	t0 := time.Now()
	testReq, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         false,
			"target":     target,
			"route_desc": routeDesc,
			"error":      err.Error(),
		})
		return
	}
	testReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(testReq)
	if err != nil {
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":         false,
			"target":     target,
			"route_desc": routeDesc,
			"error":      fmt.Sprintf("网络连接异常: %v", err),
		})
		return
	}
	coldMs := math.Round(float64(time.Since(t0).Microseconds())/10.0) / 100.0
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Probes: Warm keep-alive RTT
	warmCount := 3
	sleepDuration := 30 * time.Millisecond
	if isFast {
		warmCount = 2
		sleepDuration = 15 * time.Millisecond
	}
	var warm []float64
	for i := 0; i < warmCount; i++ {
		time.Sleep(sleepDuration)
		tw0 := time.Now()
		warmReq, _ := http.NewRequest(http.MethodGet, target, nil)
		warmReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		wResp, wErr := client.Do(warmReq)
		if wErr == nil {
			warmMs := math.Round(float64(time.Since(tw0).Microseconds())/10.0) / 100.0
			warm = append(warm, warmMs)
			_, _ = io.Copy(io.Discard, wResp.Body)
			_ = wResp.Body.Close()
		}
	}

	wMin := 0.0
	wMid := 0.0
	wMax := 0.0
	if len(warm) > 0 {
		sortedWarm := make([]float64, len(warm))
		copy(sortedWarm, warm)
		sort.Float64s(sortedWarm)
		wMin = sortedWarm[0]
		wMid = sortedWarm[len(sortedWarm)/2]
		wMax = sortedWarm[len(sortedWarm)-1]
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":           true,
		"target":       target,
		"route_desc":   routeDesc,
		"cold_ms":      coldMs,
		"warm_samples": warm,
		"warm_list":    warm,
		"warm_min_ms":  wMin,
		"warm_mid_ms":  wMid,
		"warm_max_ms":  wMax,
		"error":        "",
	})
}

func (c *ControlServer) handleUpdateCheck(w http.ResponseWriter, req *http.Request) {
	proxyURL := c.cfgMgr.GetEffectiveUpstreamProxy()
	info := updater.CheckForUpdate(proxyURL, 8*time.Second, config.AppVersion)
	c.sendJSON(w, http.StatusOK, info)
}

func (c *ControlServer) handleUpdateDownload(w http.ResponseWriter, req *http.Request) {
	c.dlMu.Lock()
	if c.dlActive || c.dlApplying {
		c.dlMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "更新任务正在进行中",
		})
		return
	}

	var reqBody map[string]string
	_ = json.NewDecoder(req.Body).Decode(&reqBody)
	downloadURL := strings.TrimSpace(reqBody["url"])
	destPath := strings.TrimSpace(reqBody["dest"])
	expectedSHA256 := strings.TrimSpace(reqBody["sha256"])
	releaseVersion := strings.TrimSpace(reqBody["version"])
	managedDownload := false

	if downloadURL == "" {
		proxyURL := c.cfgMgr.GetEffectiveUpstreamProxy()
		info := updater.CheckForUpdate(proxyURL, 8*time.Second, config.AppVersion)
		if info.DownloadURL == "" {
			c.dlMu.Unlock()
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": "未能获取到可用的下载地址",
			})
			return
		}
		if !info.HasUpdate {
			c.dlMu.Unlock()
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": "当前已经是最新版本，无需下载",
			})
			return
		}
		downloadURL = info.DownloadURL
		if expectedSHA256 == "" {
			expectedSHA256 = info.SHA256
		}
		if releaseVersion == "" {
			releaseVersion = info.LatestVersion
		}
		if strings.TrimSpace(expectedSHA256) == "" {
			c.dlMu.Unlock()
			c.sendJSON(w, http.StatusPreconditionFailed, map[string]interface{}{
				"ok":    false,
				"error": "该 Release 缺少 SHA-256 校验值，已阻止自动更新下载",
			})
			return
		}
		managedDownload = true
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.dlActive = true
	c.dlDone = false
	c.dlProgress = 0
	c.dlTotal = 0
	c.dlPercent = 0.0
	c.dlError = ""
	c.dlDest = ""
	c.dlVersion = releaseVersion
	c.dlSHA256 = expectedSHA256
	c.dlManaged = managedDownload
	c.dlCancelFn = cancel
	c.dlMu.Unlock()

	proxyURL := c.cfgMgr.GetEffectiveUpstreamProxy()

	go func() {
		finalPath, err := updater.DownloadReleaseAsset(
			downloadURL,
			destPath,
			proxyURL,
			expectedSHA256,
			func(downloaded, total int64) {
				c.dlMu.Lock()
				c.dlProgress = downloaded
				c.dlTotal = total
				if total > 0 {
					c.dlPercent = math.Round(float64(downloaded)/float64(total)*1000) / 10
				}
				c.dlMu.Unlock()
			},
			ctx,
		)

		c.dlMu.Lock()
		c.dlActive = false
		c.dlDone = (err == nil)
		if err != nil {
			c.dlError = err.Error()
		} else {
			c.dlDest = finalPath
		}
		c.dlMu.Unlock()
	}()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Download initiated in background",
	})
}

func (c *ControlServer) handleUpdateApply(w http.ResponseWriter, req *http.Request) {
	c.dlMu.Lock()
	if c.dlActive || c.dlApplying {
		c.dlMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "更新任务正在进行中",
		})
		return
	}
	if !c.dlDone || c.dlDest == "" || !c.dlManaged {
		c.dlMu.Unlock()
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": "没有可应用的已验证官方更新包",
		})
		return
	}
	archivePath := c.dlDest
	sha256 := strings.TrimSpace(c.dlSHA256)
	version := strings.TrimSpace(c.dlVersion)
	c.dlApplying = true
	c.dlMu.Unlock()

	if sha256 == "" {
		c.dlMu.Lock()
		c.dlApplying = false
		c.dlMu.Unlock()
		c.sendJSON(w, http.StatusPreconditionFailed, map[string]interface{}{
			"ok":    false,
			"error": "该 Release 没有可验证的 SHA-256，暂不能执行自动更新，请手动下载更新包",
		})
		return
	}
	if version == "" {
		c.dlMu.Lock()
		c.dlApplying = false
		c.dlMu.Unlock()
		c.sendJSON(w, http.StatusPreconditionFailed, map[string]interface{}{
			"ok":    false,
			"error": "缺少更新版本信息，暂不能执行自动更新",
		})
		return
	}

	restartArgs := append([]string(nil), os.Args[1:]...)
	if err := updater.LaunchSelfUpdater(archivePath, sha256, version, restartArgs); err != nil {
		c.dlMu.Lock()
		c.dlApplying = false
		c.dlError = err.Error()
		c.dlMu.Unlock()
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": fmt.Sprintf("v%s 更新已准备，程序即将重启", version),
		"version": version,
	})

	go func() {
		time.Sleep(300 * time.Millisecond)
		c.mu.RLock()
		quit := c.quitFunc
		c.mu.RUnlock()
		if quit != nil {
			quit()
		}
	}()
}

func (c *ControlServer) handleUpdateDownloadStatus(w http.ResponseWriter, req *http.Request) {
	c.dlMu.Lock()
	defer c.dlMu.Unlock()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"active":     c.dlActive,
		"done":       c.dlDone,
		"downloaded": c.dlProgress,
		"total":      c.dlTotal,
		"percent":    c.dlPercent,
		"dest":       c.dlDest,
		"version":    c.dlVersion,
		"sha256":     c.dlSHA256,
		"managed":    c.dlManaged,
		"applying":   c.dlApplying,
		"error":      c.dlError,
	})
}

func (c *ControlServer) handleUpdateDownloadCancel(w http.ResponseWriter, req *http.Request) {
	c.dlMu.Lock()
	defer c.dlMu.Unlock()
	if c.dlCancelFn != nil {
		c.dlCancelFn()
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Download cancelled",
	})
}

func (c *ControlServer) handleUpdateOpenFolder(w http.ResponseWriter, req *http.Request) {
	c.dlMu.Lock()
	dest := strings.TrimSpace(c.dlDest)
	c.dlMu.Unlock()

	target := dest
	if target == "" {
		target = updater.GetDefaultDownloadDir()
	}

	if err := desktop.ShowInFolder(target); err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("无法打开文件夹: %v", err),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"path": target,
	})
}


func (c *ControlServer) handleFirewallStatus(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	status, err := firewall.GetStatus(cfg.ListenPort)
	if err != nil {
		code := firewall.ErrorCode(err)
		httpStatus := http.StatusInternalServerError
		if code == firewall.CodeInvalidPort {
			httpStatus = http.StatusBadRequest
		} else if code == firewall.CodeUnsupported {
			httpStatus = http.StatusNotImplemented
		}
		c.sendJSON(w, httpStatus, map[string]interface{}{"ok": false, "code": code, "message": err.Error(), "status": status})
		return
	}
	c.sendJSON(w, http.StatusOK, status)
}

func (c *ControlServer) handleFirewallApply(w http.ResponseWriter, req *http.Request) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	cfg := c.cfgMgr.Get()
	code, err := firewall.ApplyRule(cfg.ListenPort)
	if err != nil {
		codeName := firewall.ErrorCode(err)
		httpStatus := http.StatusInternalServerError
		switch codeName {
		case firewall.CodeInvalidPort:
			httpStatus = http.StatusBadRequest
		case firewall.CodeUserCancelled:
			httpStatus = http.StatusConflict
		case firewall.CodeUnsupported:
			httpStatus = http.StatusNotImplemented
		}
		c.sendJSON(w, httpStatus, map[string]interface{}{"ok": false, "code": codeName, "message": err.Error()})
		return
	}

	status, statusErr := firewall.GetStatus(cfg.ListenPort)
	if statusErr != nil || !status.Allowed {
		message := "firewall rule verification failed"
		if statusErr != nil {
			message = statusErr.Error()
		}
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "code": firewall.CodeVerificationFailed, "message": message, "status": status})
		return
	}

	message := "Windows 防火墙规则已配置并验证"
	if code == firewall.CodeAlreadyAllowed {
		message = "Windows 防火墙规则已经满足要求，无需重复配置"
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "code": code, "message": message, "status": status})
}

func (c *ControlServer) handleCertStatus(w http.ResponseWriter, req *http.Request) {
	installed := false
	sha256 := ""
	sha1 := ""
	store := ""
	if c.certMgr != nil {
		installed = c.certMgr.IsInstalled()
		sha256 = c.certMgr.GetFingerprintSHA256()
		sha1 = c.certMgr.GetFingerprintSHA1()
		store = c.certMgr.GetInstallStore()
	}
	legacy := cert.CheckLegacyLeakedCAInstalled()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                      true,
		"installed":               installed,
		"sha256":                  sha256,
		"sha1":                    sha1,
		"store":                   store,
		"legacy_leaked_installed": legacy,
	})
}

func (c *ControlServer) handleCertInstall(w http.ResponseWriter, req *http.Request) {
	if c.certMgr == nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "certificate manager uninitialized"})
		return
	}
	err := c.certMgr.Install("")
	if err != nil {
		// Persist the full install failure (including certutil output) to the
		// program log for diagnostics, while returning the same detail in JSON.
		if c.stats != nil {
			c.stats.Log("ERROR", fmt.Sprintf("[CERT] Root CA install failed: %v", err))
		}
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "Root CA installed and trusted"})
}

func (c *ControlServer) handleCertUninstall(w http.ResponseWriter, req *http.Request) {
	if c.certMgr == nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": "certificate manager uninitialized"})
		return
	}
	err := c.certMgr.Uninstall()
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "Root CA uninstalled"})
}

func (c *ControlServer) handleCertCleanLegacy(w http.ResponseWriter, req *http.Request) {
	err := cert.CleanLegacyLeakedCA()
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "Legacy leaked CA cleaned"})
}

func (c *ControlServer) handleStartupStatus(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"supported": startup.IsStartupSupported(),
		"enabled":   startup.IsStartupEnabled(),
	})
}

func (c *ControlServer) handleStartupSet(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "invalid JSON"})
		return
	}

	oldEnabled := c.cfgMgr.Get().AutoStart
	if err := c.setStartupEnabledFn(body.Enabled); err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if _, err := c.cfgMgr.UpdateWithError(func(cfg *config.Config) {
		cfg.AutoStart = body.Enabled
	}); err != nil {
		rollbackErr := c.setStartupEnabledFn(oldEnabled)
		if rollbackErr != nil {
			c.stats.Log("ERROR", fmt.Sprintf("[STARTUP] 配置保存失败且开机启动回滚失败: save=%v rollback=%v", err, rollbackErr))
		}
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("配置保存失败: %v", err)})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"enabled": body.Enabled,
	})
}
func (c *ControlServer) handleSysProxyEnable(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	pacURL := fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", cfg.ListenPort)
	oldEnabled := cfg.AutoSystemProxy
	if err := c.enablePACProxyFn(pacURL); err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if _, err := c.cfgMgr.UpdateWithError(func(cfg *config.Config) {
		cfg.AutoSystemProxy = true
	}); err != nil {
		var rollbackErr error
		if oldEnabled {
			rollbackErr = c.enablePACProxyFn(fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", cfg.ListenPort))
		} else {
			rollbackErr = c.disablePACProxyFn(false)
		}
		if rollbackErr != nil {
			c.stats.Log("ERROR", fmt.Sprintf("[SYSPROXY] 配置保存失败且系统 PAC 回滚失败: save=%v rollback=%v", err, rollbackErr))
		}
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("配置保存失败: %v", err)})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "System PAC proxy enabled",
		"url":     pacURL,
	})
}
func (c *ControlServer) handleSysProxyDisable(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	oldEnabled := cfg.AutoSystemProxy
	if err := c.disablePACProxyFn(false); err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if _, err := c.cfgMgr.UpdateWithError(func(cfg *config.Config) {
		cfg.AutoSystemProxy = false
	}); err != nil {
		if oldEnabled {
			if rollbackErr := c.enablePACProxyFn(fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", cfg.ListenPort)); rollbackErr != nil {
				c.stats.Log("ERROR", fmt.Sprintf("[SYSPROXY] 配置保存失败且系统 PAC 回滚失败: save=%v rollback=%v", err, rollbackErr))
			}
		}
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("配置保存失败: %v", err)})
		return
	}
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "System PAC proxy disabled",
	})
}
func (c *ControlServer) handlePrefetchStatus(w http.ResponseWriter, req *http.Request) {
	cfg := c.cfgMgr.Get()
	queueSize := 0
	if c.proxySrv != nil {
		queueSize = c.proxySrv.PrefetchQueueLen()
	}
	yielding := c.stats.ActiveAPICount.Load() > 0 || c.stats.ActiveForegroundAssets.Load() > 0
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                 true,
		"enabled":            cfg.EnablePrefetch,
		"queue_size":         queueSize,
		"is_yielding":        yielding,
		"active_api_count":   c.stats.ActiveAPICount.Load(),
		"prefetch_requests":  c.stats.PrefetchRequests.Load(),
		"prefetch_successes": c.stats.PrefetchSuccesses.Load(),
		"prefetch_reused":    c.stats.PrefetchReused.Load(),
	})
}

func (c *ControlServer) getTelemetrySummary() map[string]interface{} {
	totalAPIs := c.stats.TotalAPIs.Load()
	totalAssets := c.stats.TotalAssets.Load()
	totalReqs := totalAPIs + totalAssets

	// Real connection-pool behaviour observed via httptrace.GotConn hooks.
	reused := c.stats.ReusedConns.Load()
	newConns := c.stats.NewConns.Load()
	connTotal := reused + newConns
	reuseRate := 0.0
	if connTotal > 0 {
		reuseRate = math.Round((float64(reused)/float64(connTotal))*1000) / 10
	}

	// Real negotiated protocol distribution observed from upstream responses.
	h1 := c.stats.ProtoH1.Load()
	h2 := c.stats.ProtoH2.Load()

	// Real latency percentiles over the most recent upstream request samples.
	lat := c.stats.LatencySnapshot()

	return map[string]interface{}{
		"total_requests":     totalReqs,
		"reused_connections": reused,
		"new_connections":    newConns,
		"reuse_rate_percent": reuseRate,
		"retry_count":        c.stats.APIRetries.Load(),
		"percentiles": map[string]interface{}{
			"p50_ms":  round1(lat.P50),
			"p95_ms":  round1(lat.P95),
			"p99_ms":  round1(lat.P99),
			"avg_ms":  round1(lat.Avg),
			"min_ms":  round1(lat.Min),
			"max_ms":  round1(lat.Max),
			"samples": lat.Samples,
		},
		"protocols":  map[string]int64{"HTTP/1.1": h1, "HTTP/2": h2},
		"exceptions": map[string]int{},
	}
}

// round1 rounds to one decimal place for stable, human-readable telemetry output.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func (c *ControlServer) handleTelemetry(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"telemetry": c.getTelemetrySummary(),
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

	// 1. Send initial status event
	statusData, _ := json.Marshal(c.getRuntimeStatus())
	if _, err := fmt.Fprintf(w, "event: status\ndata: %s\n\n", statusData); err != nil {
		return
	}

	// 2. Send recent logs on connect (up to last 20)
	recentLogs := c.stats.GetLogs()
	startIdx := 0
	if len(recentLogs) > 20 {
		startIdx = len(recentLogs) - 20
	}
	for _, l := range recentLogs[startIdx:] {
		logData, _ := json.Marshal(l)
		if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", logData); err != nil {
			return
		}
	}
	flusher.Flush()

	// 3. Subscribe to real-time logs and custom events
	logCh, unsubscribe := c.stats.SubscribeLogs()
	defer unsubscribe()

	sseCh := make(chan []byte, 64)
	c.sseMu.Lock()
	c.sseClients = append(c.sseClients, sseCh)
	c.sseMu.Unlock()
	defer func() {
		c.sseMu.Lock()
		for i, ch := range c.sseClients {
			if ch == sseCh {
				c.sseClients = append(c.sseClients[:i], c.sseClients[i+1:]...)
				break
			}
		}
		c.sseMu.Unlock()
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-req.Context().Done():
			return
		case <-c.closedChan:
			return
		case rawEvent, ok := <-sseCh:
			if !ok {
				return
			}
			if _, err := w.Write(rawEvent); err != nil {
				return
			}
			flusher.Flush()
		case logEnt, ok := <-logCh:
			if !ok {
				return
			}
			logData, _ := json.Marshal(logEnt)
			if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", logData); err != nil {
				return
			}
			flusher.Flush()
		case now := <-ticker.C:
			pulseData, _ := json.Marshal(map[string]interface{}{
				"time":       now.Format("15:04:05"),
				"uptime":     math.Round(time.Since(c.stats.StartTime).Seconds()*10) / 10,
				"active_api": c.stats.ActiveAPICount.Load(),
				"active_fg":  c.stats.ActiveForegroundAssets.Load(),
				"telemetry":  c.getTelemetrySummary(),
				"requests":   c.stats.RequestsMap(),
			})
			if _, err := fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", pulseData); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (c *ControlServer) handleStaticWeb(w http.ResponseWriter, req *http.Request) {
	cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(req.URL.Path)), "/")
	if cleanPath == "." {
		cleanPath = ""
	}

	// 1. If external dist directory is configured on disk and file exists, prefer disk
	if c.distDir != "" {
		candidate := filepath.Join(c.distDir, filepath.FromSlash(cleanPath))
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			w.Header().Set("Content-Type", ui.MIMEType(cleanPath))
			if strings.HasPrefix(cleanPath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else if cleanPath == "index.html" || cleanPath == "" {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			}
			http.ServeFile(w, req, candidate)
			return
		}
		if cleanPath == "" || cleanPath == "index.html" || cleanPath == "dashboard" || !strings.Contains(cleanPath, ".") {
			indexFile := filepath.Join(c.distDir, "index.html")
			if fi, err := os.Stat(indexFile); err == nil && !fi.IsDir() {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				http.ServeFile(w, req, indexFile)
				return
			}
		}
	}

	// 2. Serve from Go embedded FS (self-contained single binary)
	target := cleanPath
	if target == "" || target == "dashboard" {
		target = "index.html"
	}

	// Direct match in embedded assets
	if data, err := ui.ReadFile(target); err == nil {
		w.Header().Set("Content-Type", ui.MIMEType(target))
		if strings.HasPrefix(target, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if target == "index.html" {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if req.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return
	}

	// SPA fallback: routes without file extensions map to embedded index.html
	baseName := filepath.Base(cleanPath)
	if !strings.Contains(baseName, ".") || cleanPath == "index.html" {
		if data, err := ui.ReadFile("index.html"); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			if req.Method != http.MethodHead {
				_, _ = w.Write(data)
			}
			return
		}
	}

	// 3. Fallback minimal HTML dashboard only for root or index.html / dashboard
	if cleanPath == "" || cleanPath == "index.html" || cleanPath == "dashboard" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if req.Method != http.MethodHead {
			w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>GBF Accelerator Dashboard</title></head>
<body><h1>GBF Accelerator Dashboard (Go Engine)</h1></body>
</html>`))
		}
		return
	}

	c.sendJSON(w, http.StatusNotFound, map[string]string{"error": "File not found", "path": req.URL.Path})
}
