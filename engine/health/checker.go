package health

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/firewall"
	"gbf-proxy/proxy"
	"gbf-proxy/sysproxy"
	"gbf-proxy/telemetry"
)

var semverPattern = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Checker performs comprehensive diagnostic health checks across all subsystems.
type Checker struct {
	cfgMgr      *config.Manager
	certMgr     *cert.Manager
	cacheMgr    *cache.Manager
	proxySrv    *proxy.ProxyServer
	stats       *telemetry.Stats
	controlAddr      func() string
	probeFn          func(ctx context.Context, url string) (int, []byte, error)
	readProbeFn      func(path string) ([]byte, error)
	firewallStatusFn func(port int) (firewall.Status, error)
}

// NewChecker creates a new Checker instance with default loopback probe implementation.
func NewChecker(
	cfgMgr *config.Manager,
	certMgr *cert.Manager,
	cacheMgr *cache.Manager,
	proxySrv *proxy.ProxyServer,
	stats *telemetry.Stats,
	controlAddr func() string,
) *Checker {
	c := &Checker{
		cfgMgr:      cfgMgr,
		certMgr:     certMgr,
		cacheMgr:    cacheMgr,
		proxySrv:    proxySrv,
		stats:       stats,
		controlAddr: controlAddr,
	}
	c.probeFn = c.defaultProbe
	c.readProbeFn = os.ReadFile
	c.firewallStatusFn = firewall.GetStatus
	return c
}

// SetProbeFunc allows overriding the loopback HTTP probe for unit testing.
func (c *Checker) SetProbeFunc(fn func(ctx context.Context, url string) (int, []byte, error)) {
	c.probeFn = fn
}

// SetReadProbeFunc allows overriding the probe file reader for unit testing corruption scenarios.
func (c *Checker) SetReadProbeFunc(fn func(path string) ([]byte, error)) {
	c.readProbeFn = fn
}

// SetFirewallStatusFunc allows overriding the firewall status checker for unit testing.
func (c *Checker) SetFirewallStatusFunc(fn func(port int) (firewall.Status, error)) {
	c.firewallStatusFn = fn
}

func (c *Checker) defaultProbe(ctx context.Context, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	client := &http.Client{
		Timeout: 100 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	return resp.StatusCode, body, err
}

func (c *Checker) enrichItem(item *HealthItem) {
	if item == nil {
		return
	}
	if item.Status == StatusOk {
		item.Repairable = false
		item.RequiresConfirmation = false
		item.RequiresElevation = false
		item.Action = ""
		return
	}
	desc := GetActionDesc(item.Code)
	item.Repairable = desc.Repairable
	item.RequiresConfirmation = desc.RequiresConfirmation
	item.RequiresElevation = desc.RequiresElevation
	item.Action = desc.Action
}

// Check executes all subsystem checks and compiles the HealthResponse.
func (c *Checker) Check(ctx context.Context) Response {
	cfg := config.DefaultConfig()
	if c.cfgMgr != nil {
		cfg = c.cfgMgr.Get()
	}

	// 1. Core Check
	coreItem := c.checkCore()

	// 2. Control Plane Check
	ctrlAddr := ""
	if c.controlAddr != nil {
		ctrlAddr = c.controlAddr()
	}
	ctrlItem := c.checkControlPlane(ctrlAddr, cfg.ControlPort)

	// 3. Data Plane Check (concurrent loopback probes: 100ms per probe, 500ms global timeout)
	dataPlaneItem, pacProbeBody := c.checkDataPlane(ctx, cfg.ListenPort)

	// 4. PAC Check
	pacItem := c.checkPAC(cfg.ListenPort, pacProbeBody)

	// 5. Root CA Check
	caItem := c.checkRootCA()

	// 6. Cache Check
	cacheItem := c.checkCache(cfg.CacheDir)

	// 7. Configuration Check
	configItem := c.checkConfig(cfg)

	// 8. LAN & Firewall Check
	lanItem := c.checkLANFirewall(cfg)

	coreHealth := CoreHealth{
		Core:         coreItem,
		ControlPlane: ctrlItem,
		DataPlane:    dataPlaneItem,
		PAC:          pacItem,
		RootCA:       caItem,
		Cache:        cacheItem,
		Config:       configItem,
		LANFirewall:  lanItem,
	}

	summary := c.computeSummary(coreHealth)
	overallStatus := StatusOk
	if summary.Errors > 0 {
		overallStatus = StatusError
	} else if summary.Warnings > 0 {
		overallStatus = StatusWarning
	}

	runtimeStatus := c.gatherRuntimeStatus(cfg)

	return Response{
		Status:        overallStatus,
		Timestamp:     time.Now().UTC(),
		Summary:       summary,
		CoreHealth:    coreHealth,
		RuntimeStatus: runtimeStatus,
	}
}

func (c *Checker) checkCore() (item HealthItem) {
	defer c.enrichItem(&item)
	version := strings.TrimSpace(config.AppVersion)
	uptime := 0.0
	if c.stats != nil && !c.stats.StartTime.IsZero() {
		uptime = math.Round(time.Since(c.stats.StartTime).Seconds()*10) / 10
	}

	details := map[string]interface{}{
		"version":        version,
		"pid":            os.Getpid(),
		"platform":       runtime.GOOS,
		"arch":           runtime.GOARCH,
		"uptime_seconds": uptime,
	}

	if version == "" || !semverPattern.MatchString(version) {
		return HealthItem{
			Name:       "核心运行环境",
			Status:     StatusWarning,
			Code:       "CORE_VERSION_INVALID",
			Message:    fmt.Sprintf("核心环境版本号格式异常: %q", version),
			Details:    details,
			Repairable: false,
		}
	}

	return HealthItem{
		Name:       "核心运行环境",
		Status:     StatusOk,
		Code:       "CORE_OK",
		Message:    fmt.Sprintf("核心环境正常运行 (v%s)", version),
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) checkControlPlane(controlAddr string, expectedPort int) (item HealthItem) {
	defer c.enrichItem(&item)
	if controlAddr == "" {
		return HealthItem{
			Name:       "控制面 (Control Plane)",
			Status:     StatusError,
			Code:       "CONTROL_PLANE_LISTENER_MISSING",
			Message:    "控制面 Listener 未就绪",
			Details:    map[string]interface{}{"expected_port": expectedPort},
			Repairable: false,
		}
	}

	host, portStr, err := net.SplitHostPort(controlAddr)
	if err != nil {
		return HealthItem{
			Name:       "控制面 (Control Plane)",
			Status:     StatusError,
			Code:       "CONTROL_PLANE_INVALID_ADDR",
			Message:    fmt.Sprintf("控制面地址格式无效: %s", controlAddr),
			Details:    map[string]interface{}{"addr": controlAddr},
			Repairable: false,
		}
	}

	port, _ := strconv.Atoi(portStr)
	details := map[string]interface{}{
		"addr":          controlAddr,
		"port":          port,
		"expected_port": expectedPort,
	}

	if port <= 0 || port > 65535 {
		return HealthItem{
			Name:       "控制面 (Control Plane)",
			Status:     StatusError,
			Code:       "CONTROL_PLANE_INVALID_ADDR",
			Message:    fmt.Sprintf("控制面端口无效: %d", port),
			Details:    details,
			Repairable: false,
		}
	}

	// Strictly verify loopback address (P0 safety invariant: 127.0.0.1, ::1, or localhost)
	cleanHost := strings.ToLower(strings.Trim(host, "[]"))
	isStrictLoopback := cleanHost == "127.0.0.1" || cleanHost == "::1" || cleanHost == "localhost"
	details["loopback_only"] = isStrictLoopback

	if !isStrictLoopback {
		return HealthItem{
			Name:       "控制面 (Control Plane)",
			Status:     StatusError,
			Code:       "CONTROL_PLANE_NON_LOOPBACK",
			Message:    fmt.Sprintf("安全红线违背: 控制面未绑定在 Loopback 地址 (%s)", controlAddr),
			Details:    details,
			Repairable: false,
		}
	}

	if expectedPort > 0 && port != expectedPort {
		return HealthItem{
			Name:       "控制面 (Control Plane)",
			Status:     StatusWarning,
			Code:       "CONTROL_PLANE_PORT_MISMATCH",
			Message:    fmt.Sprintf("控制面实际端口 %d 与配置端口 %d 不一致", port, expectedPort),
			Details:    details,
			Repairable: false,
		}
	}

	return HealthItem{
		Name:       "控制面 (Control Plane)",
		Status:     StatusOk,
		Code:       "CONTROL_PLANE_OK",
		Message:    fmt.Sprintf("控制面正常运行于 %s (严格本地回环)", controlAddr),
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) checkDataPlane(ctx context.Context, listenPort int) (item HealthItem, pacProbeBody string) {
	defer c.enrichItem(&item)
	if c.proxySrv == nil || !c.proxySrv.IsRunning() {
		return HealthItem{
			Name:       "数据面代理 (Data Plane)",
			Status:     StatusWarning,
			Code:       "DATA_PLANE_STOPPED",
			Message:    "数据面代理服务未启动",
			Details:    map[string]interface{}{"running": false, "port": listenPort},
			Repairable: true,
		}, ""
	}

	addr := c.proxySrv.ListenerAddr()
	if addr == "" {
		return HealthItem{
			Name:       "数据面代理 (Data Plane)",
			Status:     StatusError,
			Code:       "DATA_PLANE_LISTENER_MISSING",
			Message:    "数据面代理正在运行但未检测到 Listener",
			Details:    map[string]interface{}{"running": true, "port": listenPort},
			Repairable: true,
		}, ""
	}

	probePort := listenPort
	if _, portStr, err := net.SplitHostPort(addr); err == nil {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 {
			probePort = p
		}
	}

	// Concurrent probes for /proxy.pac and /ca.crt
	probeTimeoutCtx, probeCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer probeCancel()

	pacURL := fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", probePort)
	caURL := fmt.Sprintf("http://127.0.0.1:%d/ca.crt", probePort)

	var wg sync.WaitGroup
	wg.Add(2)

	var pacStatus, caStatus int
	var pacBody, caBody []byte
	var pacErr, caErr error

	go func() {
		defer wg.Done()
		pacStatus, pacBody, pacErr = c.probeFn(probeTimeoutCtx, pacURL)
	}()

	go func() {
		defer wg.Done()
		caStatus, caBody, caErr = c.probeFn(probeTimeoutCtx, caURL)
	}()

	wg.Wait()

	probePacOk := pacErr == nil && pacStatus == http.StatusOK && len(pacBody) > 0
	probeCaOk := caErr == nil && caStatus == http.StatusOK && len(caBody) > 0

	details := map[string]interface{}{
		"running":         true,
		"addr":            addr,
		"port":            probePort,
		"configured_port": listenPort,
		"probe_pac_ok":    probePacOk,
		"probe_ca_ok":     probeCaOk,
	}

	if !probePacOk || !probeCaOk {
		var failReasons []string
		if !probePacOk {
			failReasons = append(failReasons, fmt.Sprintf("PAC探针失败 (status: %d, err: %v)", pacStatus, pacErr))
		}
		if !probeCaOk {
			failReasons = append(failReasons, fmt.Sprintf("CA探针失败 (status: %d, err: %v)", caStatus, caErr))
		}
		return HealthItem{
			Name:       "数据面代理 (Data Plane)",
			Status:     StatusWarning,
			Code:       "DATA_PLANE_PROBE_FAILED",
			Message:    fmt.Sprintf("数据面本地探针响应异常: %s", strings.Join(failReasons, "; ")),
			Details:    details,
			Repairable: true,
		}, string(pacBody)
	}

	if listenPort > 0 && probePort != listenPort {
		return HealthItem{
			Name:       "数据面代理 (Data Plane)",
			Status:     StatusWarning,
			Code:       "DATA_PLANE_PORT_MISMATCH",
			Message:    fmt.Sprintf("数据面实际监听端口 %d 与配置端口 %d 不一致", probePort, listenPort),
			Details:    details,
			Repairable: false,
		}, string(pacBody)
	}

	return HealthItem{
		Name:       "数据面代理 (Data Plane)",
		Status:     StatusOk,
		Code:       "DATA_PLANE_OK",
		Message:    fmt.Sprintf("数据面正常监听于 %s", addr),
		Details:    details,
		Repairable: false,
	}, string(pacBody)
}

func (c *Checker) checkPAC(listenPort int, pacProbeBody string) (item HealthItem) {
	defer c.enrichItem(&item)
	details := map[string]interface{}{
		"expected_port": listenPort,
	}

	if listenPort <= 0 || listenPort > 65535 {
		return HealthItem{
			Name:       "PAC 自动分流脚本",
			Status:     StatusError,
			Code:       "PAC_INVALID_PORT",
			Message:    fmt.Sprintf("代理监听端口配置无效: %d", listenPort),
			Details:    details,
			Repairable: false,
		}
	}

	pacContent := pacProbeBody
	if pacContent == "" {
		pacContent = proxy.GetPAC("127.0.0.1", listenPort)
	}

	details["pac_available"] = len(pacContent) > 0

	if len(pacContent) == 0 {
		return HealthItem{
			Name:       "PAC 自动分流脚本",
			Status:     StatusWarning,
			Code:       "PAC_EMPTY",
			Message:    "PAC 脚本内容为空",
			Details:    details,
			Repairable: true,
		}
	}

	// Match port with boundary (e.g. :8124;) to prevent matching 80 to 8080 or 8124 to 18124
	portPattern := regexp.MustCompile(fmt.Sprintf(`:%d[^0-9]`, listenPort))
	if !portPattern.MatchString(pacContent) {
		return HealthItem{
			Name:       "PAC 自动分流脚本",
			Status:     StatusWarning,
			Code:       "PAC_PORT_MISMATCH",
			Message:    fmt.Sprintf("PAC 脚本分流端口与当前代理监听端口 %d 不一致", listenPort),
			Details:    details,
			Repairable: true,
		}
	}

	return HealthItem{
		Name:       "PAC 自动分流脚本",
		Status:     StatusOk,
		Code:       "PAC_OK",
		Message:    "PAC 脚本可用且端口配置一致",
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) checkRootCA() (item HealthItem) {
	defer c.enrichItem(&item)
	if c.certMgr == nil {
		return HealthItem{
			Name:       "Root CA 证书与信任",
			Status:     StatusWarning,
			Code:       "CA_NOT_CONFIGURED",
			Message:    "Root CA 证书管理器未初始化",
			Repairable: false,
		}
	}

	installed := c.certMgr.IsInstalled()
	fingerprint := c.certMgr.GetFingerprintSHA256()
	caPEM := c.certMgr.GetCAPEM()

	var notBefore, notAfter time.Time
	var isExpired bool
	var certErr error

	if len(caPEM) > 0 {
		block, _ := pem.Decode(caPEM)
		if block != nil {
			parsedCert, err := x509.ParseCertificate(block.Bytes)
			if err == nil {
				notBefore = parsedCert.NotBefore
				notAfter = parsedCert.NotAfter
				now := time.Now()
				isExpired = now.After(notAfter) || now.Before(notBefore)
			} else {
				certErr = err
			}
		} else {
			certErr = fmt.Errorf("failed to decode ca PEM block")
		}
	} else {
		certErr = fmt.Errorf("empty ca PEM data")
	}

	// Also verify ca.crt on disk if present
	certsDir := c.certMgr.GetCertsDir()
	if certsDir == "" {
		certsDir = "certs"
	}
	caCertPath := filepath.Join(certsDir, "ca.crt")
	if diskBytes, err := os.ReadFile(caCertPath); err == nil {
		if len(diskBytes) > 0 {
			block, _ := pem.Decode(diskBytes)
			if block == nil {
				certErr = fmt.Errorf("ca.crt on disk is corrupted: invalid PEM block")
			} else if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				certErr = fmt.Errorf("ca.crt on disk is corrupted: %w", err)
			}
		} else {
			certErr = fmt.Errorf("ca.crt on disk is empty")
		}
	}

	details := map[string]interface{}{
		"installed":   installed,
		"fingerprint": fingerprint,
		"store":       c.certMgr.GetInstallStore(),
	}
	if !notBefore.IsZero() {
		details["not_before"] = notBefore.Format(time.RFC3339)
	}
	if !notAfter.IsZero() {
		details["not_after"] = notAfter.Format(time.RFC3339)
	}

	if certErr != nil {
		return HealthItem{
			Name:       "Root CA 证书与信任",
			Status:     StatusError,
			Code:       "CA_CORRUPTED",
			Message:    fmt.Sprintf("Root CA 证书损坏或解析失败: %v", certErr),
			Details:    details,
			Repairable: true,
		}
	}

	if isExpired {
		return HealthItem{
			Name:       "Root CA 证书与信任",
			Status:     StatusError,
			Code:       "CA_EXPIRED",
			Message:    "Root CA 证书已过期，需要重新生成与安装",
			Details:    details,
			Repairable: true,
		}
	}

	if !installed {
		return HealthItem{
			Name:       "Root CA 证书与信任",
			Status:     StatusWarning,
			Code:       "CA_NOT_INSTALLED",
			Message:    "Root CA 证书尚未安装或受系统信任",
			Details:    details,
			Repairable: true,
		}
	}

	return HealthItem{
		Name:       "Root CA 证书与信任",
		Status:     StatusOk,
		Code:       "CA_OK",
		Message:    "Root CA 证书已安装且处于有效期内",
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) checkCache(fallbackCacheDir string) (item HealthItem) {
	defer c.enrichItem(&item)
	cacheBase := fallbackCacheDir
	if cacheBase == "" && c.cacheMgr != nil {
		cacheBase = c.cacheMgr.GetCacheBase()
	}
	ramItems := 0
	ramBytes := int64(0)
	if c.cacheMgr != nil {
		ramItems, ramBytes = c.cacheMgr.Stats()
	}

	details := map[string]interface{}{
		"cache_dir": cacheBase,
		"ram_items": ramItems,
		"ram_mb":    math.Round(float64(ramBytes)/(1024*1024)*100) / 100,
	}

	if cacheBase == "" {
		return HealthItem{
			Name:       "本地缓存存储",
			Status:     StatusWarning,
			Code:       "CACHE_DIR_NOT_CONFIGURED",
			Message:    "缓存目录未配置",
			Details:    details,
			Repairable: false,
		}
	}

	fi, err := os.Stat(cacheBase)
	if os.IsNotExist(err) {
		return HealthItem{
			Name:       "本地缓存存储",
			Status:     StatusWarning,
			Code:       "CACHE_DIR_MISSING",
			Message:    fmt.Sprintf("缓存目录不存在: %s", cacheBase),
			Details:    details,
			Repairable: true, // Creating missing cache dir is safe, local, and reversible
		}
	} else if err != nil {
		return HealthItem{
			Name:       "本地缓存存储",
			Status:     StatusError,
			Code:       "CACHE_DIR_ERROR",
			Message:    fmt.Sprintf("访问缓存目录失败: %v", err),
			Details:    details,
			Repairable: false,
		}
	} else if !fi.IsDir() {
		return HealthItem{
			Name:       "本地缓存存储",
			Status:     StatusError,
			Code:       "CACHE_PATH_NOT_DIR",
			Message:    fmt.Sprintf("缓存路径不是有效目录: %s", cacheBase),
			Details:    details,
			Repairable: false,
		}
	}

	// Reversible Cache probe: write -> read -> verify -> defer os.Remove(...)
	probeName := fmt.Sprintf(".health_probe_%d_%d.tmp", os.Getpid(), time.Now().UnixNano())
	probePath := filepath.Join(cacheBase, probeName)
	probeContent := []byte(fmt.Sprintf("GBF_HEALTH_CHECK_%d_%d", os.Getpid(), time.Now().UnixNano()))

	defer func() {
		_ = os.Remove(probePath)
	}()

	if err := os.WriteFile(probePath, probeContent, 0644); err != nil {
		return HealthItem{
			Name:       "本地缓存存储",
			Status:     StatusError,
			Code:       "CACHE_DIR_NOT_WRITABLE",
			Message:    fmt.Sprintf("缓存目录写入失败: %v", err),
			Details:    details,
			Repairable: false,
		}
	}

	readFn := os.ReadFile
	if c.readProbeFn != nil {
		readFn = c.readProbeFn
	}

	readBack, err := readFn(probePath)
	if err != nil || !bytes.Equal(readBack, probeContent) {
		// Strict guardrail 4: Do NOT over-diagnose as "物理磁盘损坏".
		return HealthItem{
			Name:       "本地缓存存储",
			Status:     StatusError,
			Code:       "CACHE_PROBE_CORRUPT",
			Message:    "缓存目录临时探针读写校验失败",
			Details:    details,
			Repairable: false,
		}
	}

	return HealthItem{
		Name:       "本地缓存存储",
		Status:     StatusOk,
		Code:       "CACHE_OK",
		Message:    "缓存目录读写正常",
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) checkConfig(cfg config.Config) (item HealthItem) {
	defer c.enrichItem(&item)
	details := map[string]interface{}{
		"listen_port":  cfg.ListenPort,
		"control_port": cfg.ControlPort,
		"direct_mode":  cfg.DirectMode,
		"allow_lan":    cfg.AllowLAN,
	}

	if cfg.ListenPort <= 0 || cfg.ListenPort > 65535 {
		return HealthItem{
			Name:       "系统网络配置与模式",
			Status:     StatusError,
			Code:       "PORT_INVALID",
			Message:    fmt.Sprintf("代理监听端口配置无效: %d", cfg.ListenPort),
			Details:    details,
			Repairable: false,
		}
	}

	if cfg.ControlPort <= 0 || cfg.ControlPort > 65535 {
		return HealthItem{
			Name:       "系统网络配置与模式",
			Status:     StatusError,
			Code:       "PORT_INVALID",
			Message:    fmt.Sprintf("控制面端口配置无效: %d", cfg.ControlPort),
			Details:    details,
			Repairable: false,
		}
	}

	if cfg.ListenPort == cfg.ControlPort {
		return HealthItem{
			Name:       "系统网络配置与模式",
			Status:     StatusError,
			Code:       "PORT_CONFIG_COLLISION",
			Message:    fmt.Sprintf("代理端口与控制面端口冲突: 均为 %d", cfg.ListenPort),
			Details:    details,
			Repairable: false, // Requires explicit user action to change port
		}
	}

	conflict := sysproxy.CheckProxyConflict(cfg.ListenPort)
	details["sysproxy_conflict"] = conflict
	if conflict != "" {
		return HealthItem{
			Name:       "系统网络配置与模式",
			Status:     StatusWarning,
			Code:       "SYSPROXY_CONFLICT",
			Message:    fmt.Sprintf("系统代理与其他程序冲突: %s", conflict),
			Details:    details,
			Repairable: false, // Requires explicit user action
		}
	}

	return HealthItem{
		Name:       "系统网络配置与模式",
		Status:     StatusOk,
		Code:       "CONFIG_OK",
		Message:    "网络配置参数有效",
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) checkLANFirewall(cfg config.Config) (item HealthItem) {
	defer c.enrichItem(&item)
	// Invariant: When AllowLAN == false, completely skip LAN IP, LAN listen, and firewall check.
	if !cfg.AllowLAN {
		return HealthItem{
			Name:    "局域网共享与防火墙",
			Status:  StatusOk,
			Code:    "LAN_DISABLED",
			Message: "局域网共享未启用 (跳过局域网与防火墙检查)",
			Details: map[string]interface{}{
				"allow_lan":         false,
				"lan_ip":            nil,
				"firewall_checked":  false,
			},
			Repairable: false,
		}
	}

	lanIP := config.GetLANIP()
	details := map[string]interface{}{
		"allow_lan":        true,
		"lan_ip":           lanIP,
		"firewall_checked": firewall.IsSupported(),
	}

	if lanIP == "" {
		return HealthItem{
			Name:       "局域网共享与防火墙",
			Status:     StatusWarning,
			Code:       "LAN_IP_NOT_FOUND",
			Message:    "局域网已启用但未检测到有效局域网 IP",
			Details:    details,
			Repairable: false,
		}
	}

	// When AllowLAN == true, check if Data Plane is bound to LAN or only loopback
	if c.proxySrv != nil && c.proxySrv.IsRunning() {
		addr := c.proxySrv.ListenerAddr()
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if host == "127.0.0.1" || host == "localhost" {
				return HealthItem{
					Name:    "局域网共享与防火墙",
					Status:  StatusWarning,
					Code:    "LAN_DATA_PLANE_NOT_BOUND",
					Message: fmt.Sprintf("局域网共享已启用，但数据面仅监听于回环地址 (%s)", addr),
					Details: details,
				}
			}
		}
	}

	if firewall.IsSupported() {
		statusFn := c.firewallStatusFn
		if statusFn == nil {
			statusFn = firewall.GetStatus
		}
		fwStatus, err := statusFn(cfg.ListenPort)
		details["firewall_status"] = fwStatus
		if err != nil || !fwStatus.Allowed {
			return HealthItem{
				Name:       "局域网共享与防火墙",
				Status:     StatusWarning,
				Code:       "FIREWALL_RULE_MISSING",
				Message:    fmt.Sprintf("局域网已启用，但 Windows 防火墙未放行端口 %d", cfg.ListenPort),
				Details:    details,
			}
		}

		return HealthItem{
			Name:       "局域网共享与防火墙",
			Status:     StatusOk,
			Code:       "LAN_FIREWALL_OK",
			Message:    fmt.Sprintf("局域网已启用且防火墙已放行 (IP: %s)", lanIP),
			Details:    details,
			Repairable: false,
		}
	}

	return HealthItem{
		Name:       "局域网共享与防火墙",
		Status:     StatusOk,
		Code:       "LAN_OK",
		Message:    fmt.Sprintf("局域网已启用 (IP: %s)", lanIP),
		Details:    details,
		Repairable: false,
	}
}

func (c *Checker) computeSummary(core CoreHealth) Summary {
	items := []HealthItem{
		core.Core,
		core.ControlPlane,
		core.DataPlane,
		core.PAC,
		core.RootCA,
		core.Cache,
		core.Config,
		core.LANFirewall,
	}

	summary := Summary{
		TotalChecks: len(items),
	}

	for _, item := range items {
		switch item.Status {
		case StatusOk:
			summary.Passed++
		case StatusWarning:
			summary.Warnings++
		case StatusError:
			summary.Errors++
		}
	}

	return summary
}

func (c *Checker) gatherRuntimeStatus(cfg config.Config) RuntimeStatus {
	effectiveProxy := ""
	if c.cfgMgr != nil {
		effectiveProxy = c.cfgMgr.GetEffectiveUpstreamProxy()
	}
	var upstreamStatus interface{}
	if c.proxySrv != nil {
		effectiveProxy = c.proxySrv.GetEffectiveUpstreamProxy()
		upstreamStatus = c.proxySrv.GetUpstreamStatus()
	}

	upstreamRoute := map[string]interface{}{
		"effective_proxy": effectiveProxy,
		"direct_mode":     cfg.DirectMode,
		"upstream_status": upstreamStatus,
	}

	var retries int64
	recentErrors := make([]RecentErrorLog, 0)
	trafficMetrics := map[string]interface{}{}

	if c.stats != nil {
		retries = c.stats.APIRetries.Load()

		logs := c.stats.GetLogs()
		for i := len(logs) - 1; i >= 0; i-- {
			entry := logs[i]
			lvl := strings.ToUpper(entry.Level)
			if lvl == "ERROR" || lvl == "CRITICAL" {
				recentErrors = append(recentErrors, RecentErrorLog{
					Time:  entry.Time,
					Level: entry.Level,
					Msg:   entry.Msg,
				})
				if len(recentErrors) >= 10 {
					break
				}
			}
		}

		reused := c.stats.ReusedConns.Load()
		newConns := c.stats.NewConns.Load()
		connTotal := reused + newConns
		reuseRate := 0.0
		if connTotal > 0 {
			reuseRate = math.Round((float64(reused)/float64(connTotal))*1000) / 10
		}

		trafficMetrics = map[string]interface{}{
			"total_apis":         c.stats.TotalAPIs.Load(),
			"total_assets":       c.stats.TotalAssets.Load(),
			"total_hits":         c.stats.TotalHits.Load(),
			"ram_hits":           c.stats.RAMHits.Load(),
			"disk_hits":          c.stats.DiskHits.Load(),
			"cache_misses":       c.stats.CacheMisses.Load(),
			"reused_connections": reused,
			"new_connections":    newConns,
			"reuse_rate_percent": reuseRate,
		}
	}

	return RuntimeStatus{
		UpstreamRoute:  upstreamRoute,
		APIRetries:     retries,
		RecentErrors:   recentErrors,
		TrafficMetrics: trafficMetrics,
	}
}
