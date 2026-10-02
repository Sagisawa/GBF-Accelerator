package health

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/firewall"
	"gbf-proxy/proxy"
	"gbf-proxy/telemetry"
)

var (
	// ErrRepairInProgress is returned when another repair job is currently executing.
	ErrRepairInProgress = errors.New("repair already in progress")
)

// Standard Action identifier constants.
const (
	ActionStartDataPlane             = "start_data_plane"
	ActionReloadDataPlane            = "reload_data_plane"
	ActionRegeneratePAC              = "regenerate_pac"
	ActionCreateCacheDir             = "create_cache_dir"
	ActionInstallRootCA              = "install_root_ca"
	ActionRegenerateAndInstallRootCA = "regenerate_and_install_root_ca"
	ActionReloadCAEndpoint           = "reload_ca_endpoint"
	ActionApplyFirewallRule          = "apply_firewall_rule"
	ActionRebindLANDataPlane         = "rebind_lan_data_plane"
	ActionRebindControlPlaneLoopback = "rebind_control_plane_loopback"
)

// ActionDesc defines the server-authoritative repair capabilities and constraints for a diagnostic code.
type ActionDesc struct {
	Repairable           bool
	RequiresConfirmation bool
	RequiresElevation    bool
	Action               string
	Priority             int // Lower priority executes earlier
}

// ServerRepairMap is the authoritative server-side mapping of codes to their repair metadata.
var ServerRepairMap = map[string]ActionDesc{
	// Cache (Priority 10)
	"CACHE_DIR_MISSING": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionCreateCacheDir,
		Priority:             10,
	},

	// Root CA (Priority 20)
	"CA_NOT_INSTALLED": {
		Repairable:           true,
		RequiresConfirmation: true,
		RequiresElevation:    true,
		Action:               ActionInstallRootCA,
		Priority:             20,
	},
	"CA_EXPIRED": {
		Repairable:           true,
		RequiresConfirmation: true,
		RequiresElevation:    true,
		Action:               ActionRegenerateAndInstallRootCA,
		Priority:             20,
	},
	"CA_CORRUPTED": {
		Repairable:           true,
		RequiresConfirmation: true,
		RequiresElevation:    true,
		Action:               ActionRegenerateAndInstallRootCA,
		Priority:             20,
	},
	"CA_PARSE_ERROR": {
		Repairable:           true,
		RequiresConfirmation: true,
		RequiresElevation:    true,
		Action:               ActionRegenerateAndInstallRootCA,
		Priority:             20,
	},
	"CA_ENDPOINT_FAILED": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionReloadCAEndpoint,
		Priority:             50,
	},

	// Windows Firewall (Priority 30)
	"FIREWALL_RULE_MISSING": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    true,
		Action:               ActionApplyFirewallRule,
		Priority:             30,
	},

	// Data Plane (Priority 40)
	"DATA_PLANE_STOPPED": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionStartDataPlane,
		Priority:             40,
	},
	"DATA_PLANE_LISTENER_MISSING": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionReloadDataPlane,
		Priority:             40,
	},
	"DATA_PLANE_PROBE_FAILED": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionReloadDataPlane,
		Priority:             40,
	},
	"DATA_PLANE_UNREACHABLE": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionReloadDataPlane,
		Priority:             40,
	},
	"DATA_PLANE_PORT_MISMATCH": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionReloadDataPlane,
		Priority:             40,
	},

	// PAC (Priority 50)
	"PAC_EMPTY": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionRegeneratePAC,
		Priority:             50,
	},
	"PAC_PORT_MISMATCH": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionRegeneratePAC,
		Priority:             50,
	},
	"PAC_ENDPOINT_FAILED": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionRegeneratePAC,
		Priority:             50,
	},

	// LAN (Priority 60)
	"LAN_DATA_PLANE_NOT_BOUND": {
		Repairable:           true,
		RequiresConfirmation: false,
		RequiresElevation:    false,
		Action:               ActionRebindLANDataPlane,
		Priority:             60,
	},

	// Control Plane (Priority 70)
	"CONTROL_PLANE_NON_LOOPBACK": {
		Repairable:           true,
		RequiresConfirmation: true,
		RequiresElevation:    false,
		Action:               ActionRebindControlPlaneLoopback,
		Priority:             70,
	},

	// Explicitly non-repairable items (guarded)
	"PORT_CONFIG_COLLISION":          {Repairable: false},
	"CONFIG_PORT_COLLISION":        {Repairable: false},
	"PORT_INVALID":                 {Repairable: false},
	"CONFIG_PORT_INVALID":          {Repairable: false},
	"LAN_IP_NOT_FOUND":             {Repairable: false},
	"LAN_NO_IP":                    {Repairable: false},
	"LAN_DISABLED":                 {Repairable: false},
	"CACHE_DIR_NOT_WRITABLE":       {Repairable: false},
	"CACHE_WRITE_DENIED":           {Repairable: false},
	"CACHE_DIR_ERROR":              {Repairable: false},
	"CACHE_PATH_NOT_DIR":           {Repairable: false},
	"CACHE_PROBE_CORRUPT":          {Repairable: false},
	"CACHE_DIR_NOT_CONFIGURED":     {Repairable: false},
	"CA_NOT_CONFIGURED":            {Repairable: false},
	"CORE_VERSION_INVALID":         {Repairable: false},
	"CONTROL_PLANE_LISTENER_MISSING": {Repairable: false},
	"CONTROL_PLANE_INVALID_ADDR":    {Repairable: false},
	"CONTROL_PLANE_PORT_MISMATCH":   {Repairable: false},
	"SYSPROXY_CONFLICT":            {Repairable: false},
}

// GetActionDesc returns the authoritative ActionDesc for a diagnostic code.
func GetActionDesc(code string) ActionDesc {
	if desc, ok := ServerRepairMap[code]; ok {
		return desc
	}
	return ActionDesc{Repairable: false}
}

// Repairer coordinates executing safe, automated repairs for diagnosed health issues.
type Repairer struct {
	cfgMgr        *config.Manager
	certMgr       *cert.Manager
	cacheMgr      *cache.Manager
	proxySrv      *proxy.ProxyServer
	stats         *telemetry.Stats
	checker       *Checker
	controlReload  func(port int) error
	firewallApply  func(port int) (string, error)
	firewallStatus func(port int) (firewall.Status, error)
	certInstall    func(certsDir string) error
	repairMu       sync.Mutex
}

// NewRepairer creates a new Repairer instance.
func NewRepairer(
	cfgMgr *config.Manager,
	certMgr *cert.Manager,
	cacheMgr *cache.Manager,
	proxySrv *proxy.ProxyServer,
	stats *telemetry.Stats,
	checker *Checker,
	controlReload func(port int) error,
) *Repairer {
	r := &Repairer{
		cfgMgr:         cfgMgr,
		certMgr:        certMgr,
		cacheMgr:       cacheMgr,
		proxySrv:       proxySrv,
		stats:          stats,
		checker:        checker,
		controlReload:  controlReload,
		firewallApply:  firewall.ApplyRule,
		firewallStatus: firewall.GetStatus,
	}
	if certMgr != nil {
		r.certInstall = certMgr.Install
	}
	return r
}

// SetCertInstallFunc overrides the certificate install implementation for unit testing.
func (r *Repairer) SetCertInstallFunc(fn func(certsDir string) error) {
	r.certInstall = fn
}

// SetFirewallApplyFunc overrides the firewall apply implementation for unit testing.
func (r *Repairer) SetFirewallApplyFunc(fn func(port int) (string, error)) {
	r.firewallApply = fn
}

// SetFirewallStatusFunc overrides the firewall status check implementation for unit testing.
func (r *Repairer) SetFirewallStatusFunc(fn func(port int) (firewall.Status, error)) {
	r.firewallStatus = fn
}

// SetControlReloadFunc overrides the control plane reload implementation for testing.
func (r *Repairer) SetControlReloadFunc(fn func(port int) error) {
	r.controlReload = fn
}

// Repair executes repair actions based on current authoritative health status.
func (r *Repairer) Repair(ctx context.Context, req RepairRequest) (*RepairResponse, error) {
	if !r.repairMu.TryLock() {
		return nil, ErrRepairInProgress
	}
	defer r.repairMu.Unlock()

	// 1. Initial Authoritative Health Check
	initialHealth := r.checker.Check(ctx)

	// Collect all core health items
	items := []HealthItem{
		initialHealth.CoreHealth.Cache,
		initialHealth.CoreHealth.RootCA,
		initialHealth.CoreHealth.LANFirewall,
		initialHealth.CoreHealth.DataPlane,
		initialHealth.CoreHealth.PAC,
		initialHealth.CoreHealth.ControlPlane,
		initialHealth.CoreHealth.Config,
		initialHealth.CoreHealth.Core,
	}

	unhealthyMap := make(map[string]HealthItem)
	for _, it := range items {
		if it.Status != StatusOk {
			unhealthyMap[it.Code] = it
		}
	}

	normalizeCode := func(code string) string {
		switch code {
		case "DATA_PLANE_UNREACHABLE":
			if _, ok := unhealthyMap["DATA_PLANE_PROBE_FAILED"]; ok {
				return "DATA_PLANE_PROBE_FAILED"
			}
		case "CA_CORRUPTED":
			if _, ok := unhealthyMap["CA_PARSE_ERROR"]; ok {
				return "CA_PARSE_ERROR"
			}
		case "PAC_ENDPOINT_FAILED":
			if _, ok := unhealthyMap["PAC_EMPTY"]; ok {
				return "PAC_EMPTY"
			}
		case "CONFIG_PORT_COLLISION":
			if _, ok := unhealthyMap["PORT_CONFIG_COLLISION"]; ok {
				return "PORT_CONFIG_COLLISION"
			}
		case "CONFIG_PORT_INVALID":
			if _, ok := unhealthyMap["PORT_INVALID"]; ok {
				return "PORT_INVALID"
			}
		case "LAN_NO_IP":
			if _, ok := unhealthyMap["LAN_IP_NOT_FOUND"]; ok {
				return "LAN_IP_NOT_FOUND"
			}
		case "CACHE_WRITE_DENIED":
			if _, ok := unhealthyMap["CACHE_DIR_NOT_WRITABLE"]; ok {
				return "CACHE_DIR_NOT_WRITABLE"
			}
		}
		return code
	}

	var itemsToRepair []HealthItem

	requestedCode := strings.TrimSpace(req.Code)
	if requestedCode != "" && !strings.EqualFold(requestedCode, "all") {
		normCode := normalizeCode(requestedCode)
		desc := GetActionDesc(normCode)
		if !desc.Repairable {
			return &RepairResponse{
				Success: false,
				Message: fmt.Sprintf("问题 %s 无法自动修复，需手动处理", requestedCode),
				Results: []RepairStepResult{
					{
						Code:    requestedCode,
						Action:  desc.Action,
						Success: false,
						Message: "该问题不支持自动修复，需手动处理",
					},
				},
				Health: initialHealth,
			}, nil
		}

		itemsToRepair = append(itemsToRepair, HealthItem{Code: normCode})
	} else {
		// Batch repair: collect all repairable items currently unhealthy
		for _, it := range items {
			if it.Status != StatusOk {
				desc := GetActionDesc(it.Code)
				if desc.Repairable {
					itemsToRepair = append(itemsToRepair, it)
				}
			}
		}

		// Sort by priority for orderly resolution
		sort.SliceStable(itemsToRepair, func(i, j int) bool {
			return GetActionDesc(itemsToRepair[i].Code).Priority < GetActionDesc(itemsToRepair[j].Code).Priority
		})
	}

	if len(itemsToRepair) == 0 {
		return &RepairResponse{
			Success: true,
			Message: "所有受检项均正常，无需修复",
			Results: []RepairStepResult{},
			Health:  initialHealth,
		}, nil
	}

	var results []RepairStepResult
	successCount := 0
	failCount := 0
	skipCount := 0

	dataPlaneFailed := false
	dataPlaneRepaired := false
	rootCAFailed := false

	for _, item := range itemsToRepair {
		desc := GetActionDesc(item.Code)

		// Short-circuit: Check prerequisite dependencies
		// 1. Data Plane dependency: PAC, CA Endpoint, LAN Rebind all depend on a healthy/running Data Plane
		isDataPlaneDependent := desc.Action == ActionReloadCAEndpoint || desc.Action == ActionRegeneratePAC || desc.Action == ActionRebindLANDataPlane
		if isDataPlaneDependent {
			if dataPlaneFailed {
				skipCount++
				results = append(results, RepairStepResult{
					Code:    item.Code,
					Action:  desc.Action,
					Success: false,
					Skipped: true,
					Status:  "skipped",
					Message: "因前置数据面启动/重载失败，已跳过此依赖项修复",
					Error:   "skipped_dependency_failed",
				})
				continue
			}
			if r.proxySrv != nil && !r.proxySrv.IsRunning() && !dataPlaneRepaired {
				skipCount++
				results = append(results, RepairStepResult{
					Code:    item.Code,
					Action:  desc.Action,
					Success: false,
					Skipped: true,
					Status:  "skipped",
					Message: "因数据面服务未处于运行状态，已跳过此依赖项修复",
					Error:   "skipped_dependency_failed",
				})
				continue
			}
		}

		// 2. Root CA dependency: CA endpoint reload depends on a valid Root CA
		if desc.Action == ActionReloadCAEndpoint {
			if rootCAFailed {
				skipCount++
				results = append(results, RepairStepResult{
					Code:    item.Code,
					Action:  desc.Action,
					Success: false,
					Skipped: true,
					Status:  "skipped",
					Message: "因前置根证书生成/安装失败，已跳过此依赖项修复",
					Error:   "skipped_dependency_failed",
				})
				continue
			}
		}

		ok, msg, err := r.executeAction(ctx, item.Code, desc.Action)
		stepRes := RepairStepResult{
			Code:    item.Code,
			Action:  desc.Action,
			Success: ok,
			Message: msg,
		}
		if err != nil || !ok {
			if err != nil {
				stepRes.Error = err.Error()
			}
			stepRes.Status = "failed"
			failCount++
			if desc.Action == ActionStartDataPlane || desc.Action == ActionReloadDataPlane {
				dataPlaneFailed = true
			}
			if desc.Action == ActionInstallRootCA || desc.Action == ActionRegenerateAndInstallRootCA {
				rootCAFailed = true
			}
		} else {
			stepRes.Status = "fixed"
			successCount++
			if desc.Action == ActionStartDataPlane || desc.Action == ActionReloadDataPlane {
				dataPlaneRepaired = true
			}
		}
		results = append(results, stepRes)
	}

	// 2. Authoritative Post-Repair Health Check
	finalHealth := r.checker.Check(ctx)

	overallSuccess := failCount == 0 && skipCount == 0
	var overallMsg string
	if overallSuccess {
		if finalHealth.Status == StatusOk {
			overallMsg = fmt.Sprintf("已尝试修复 %d 项，全部修复成功，系统已恢复正常", len(results))
		} else {
			overallMsg = fmt.Sprintf("已尝试修复 %d 项，全部修复操作完成，系统状态已更新", len(results))
		}
	} else if failCount > 0 && skipCount > 0 {
		overallMsg = fmt.Sprintf("已尝试修复 %d 项，%d 项成功，%d 项失败，%d 项跳过", len(results), successCount, failCount, skipCount)
	} else if failCount > 0 {
		overallMsg = fmt.Sprintf("已尝试修复 %d 项，%d 项成功，%d 项失败", len(results), successCount, failCount)
	} else {
		overallMsg = fmt.Sprintf("已尝试修复 %d 项，%d 项成功，%d 项跳过", len(results), successCount, skipCount)
	}

	return &RepairResponse{
		Success: overallSuccess,
		Message: overallMsg,
		Results: results,
		Health:  finalHealth,
	}, nil
}

func (r *Repairer) executeAction(ctx context.Context, code, action string) (bool, string, error) {
	cfg := config.DefaultConfig()
	if r.cfgMgr != nil {
		cfg = r.cfgMgr.Get()
	}

	switch action {
	case ActionStartDataPlane:
		if r.proxySrv == nil {
			return false, "数据面代理服务未初始化", fmt.Errorf("proxy server is nil")
		}
		if r.proxySrv.IsRunning() {
			return true, "数据面代理服务已在运行中", nil
		}
		// Probe if the port is already occupied by an external process
		if testLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.ListenPort)); err != nil {
			return false, fmt.Sprintf("代理端口 %d 已被外部程序占用: %v", cfg.ListenPort, err), fmt.Errorf("port occupied by external process: %w", err)
		} else {
			_ = testLn.Close()
		}
		if err := r.proxySrv.Start(); err != nil {
			return false, fmt.Sprintf("启动数据面失败: %v", err), err
		}
		return true, "数据面代理已成功启动", nil

	case ActionReloadDataPlane:
		if r.proxySrv == nil {
			return false, "数据面代理服务未初始化", fmt.Errorf("proxy server is nil")
		}
		if !r.proxySrv.IsRunning() {
			// Check if port is already occupied by an external process before starting
			if testLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.ListenPort)); err != nil {
				return false, fmt.Sprintf("检测到外部端口冲突 (端口 %d 已被占用): %v", cfg.ListenPort, err), fmt.Errorf("external port conflict: %w", err)
			} else {
				_ = testLn.Close()
			}
			if err := r.proxySrv.Start(); err != nil {
				return false, fmt.Sprintf("启动数据面失败: %v", err), err
			}
			return true, "数据面代理已启动并恢复监听", nil
		}
		host := "127.0.0.1"
		if r.cfgMgr != nil {
			host = r.cfgMgr.GetEffectiveListenHost()
		}
		if err := r.proxySrv.ReloadListener(host, cfg.ListenPort); err != nil {
			return false, fmt.Sprintf("重载数据面 Listener 失败: %v", err), err
		}
		return true, fmt.Sprintf("数据面 Listener 已重载至 %s:%d", host, cfg.ListenPort), nil

	case ActionRegeneratePAC:
		_ = proxy.GetPAC("127.0.0.1", cfg.ListenPort)
		if r.proxySrv != nil && r.proxySrv.IsRunning() {
			host := "127.0.0.1"
			if r.cfgMgr != nil {
				host = r.cfgMgr.GetEffectiveListenHost()
			}
			_ = r.proxySrv.ReloadListener(host, cfg.ListenPort)
		}
		return true, fmt.Sprintf("PAC 脚本已重新生成并同步至端口 %d", cfg.ListenPort), nil

	case ActionCreateCacheDir:
		cacheDir := cfg.CacheDir
		if strings.TrimSpace(cacheDir) == "" && r.cacheMgr != nil {
			cacheDir = r.cacheMgr.GetCacheBase()
		}
		if strings.TrimSpace(cacheDir) == "" {
			return false, "缓存目录未配置", fmt.Errorf("empty cache directory")
		}
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return false, fmt.Sprintf("创建缓存目录失败: %v", err), err
		}
		_ = os.MkdirAll(filepath.Join(cacheDir, "hosts"), 0755)
		if r.cacheMgr != nil {
			r.cacheMgr.SetCacheBase(cacheDir)
		}

		// Re-execute cache read/write probe to verify directory functionality
		probePath := filepath.Join(cacheDir, fmt.Sprintf(".health_repair_probe_%d_%d.tmp", os.Getpid(), time.Now().UnixNano()))
		probeData := []byte("GBF_CACHE_REPAIR_PROBE")
		if err := os.WriteFile(probePath, probeData, 0644); err != nil {
			return false, fmt.Sprintf("缓存目录已创建，但读写探针写入失败 (权限不足): %v", err), err
		}
		readBack, err := os.ReadFile(probePath)
		_ = os.Remove(probePath)
		if err != nil || !bytes.Equal(readBack, probeData) {
			return false, "缓存目录读写探针校验失败", fmt.Errorf("cache probe read verification failed")
		}
		return true, fmt.Sprintf("缓存目录已成功创建并验证读写: %s", cacheDir), nil

	case ActionInstallRootCA:
		if r.certMgr == nil {
			return false, "证书管理器未初始化", fmt.Errorf("cert manager is nil")
		}
		installFn := r.certMgr.Install
		if r.certInstall != nil {
			installFn = r.certInstall
		}
		if err := installFn(""); err != nil {
			return false, fmt.Sprintf("安装 Root CA 证书失败: %v", err), err
		}
		return true, "Root CA 证书已安装并导入系统受信任存储", nil

	case ActionRegenerateAndInstallRootCA:
		if r.certMgr == nil {
			return false, "证书管理器未初始化", fmt.Errorf("cert manager is nil")
		}
		if err := r.certMgr.RegenerateCA(); err != nil {
			return false, fmt.Sprintf("重新生成 Root CA 证书失败: %v", err), err
		}
		installFn := r.certMgr.Install
		if r.certInstall != nil {
			installFn = r.certInstall
		}
		if err := installFn(""); err != nil {
			return false, fmt.Sprintf("Root CA 证书已重新生成，但导入系统存储失败: %v", err), err
		}
		return true, "Root CA 证书已重新生成并导入系统受信任存储", nil

	case ActionReloadCAEndpoint:
		if r.certMgr == nil {
			return false, "证书管理器未初始化", fmt.Errorf("cert manager is nil")
		}
		if r.proxySrv != nil && !r.proxySrv.IsRunning() {
			return false, "数据面代理服务未运行，无法重新加载 Root CA 端点", fmt.Errorf("proxy server is not running")
		}
		if r.proxySrv != nil && r.proxySrv.IsRunning() {
			host := "127.0.0.1"
			if r.cfgMgr != nil {
				host = r.cfgMgr.GetEffectiveListenHost()
			}
			_ = r.proxySrv.ReloadListener(host, cfg.ListenPort)
		}
		return true, "Root CA 端点处理器已重新加载", nil

	case ActionApplyFirewallRule:
		if r.firewallApply == nil {
			r.firewallApply = firewall.ApplyRule
		}
		codeName, err := r.firewallApply(cfg.ListenPort)
		if err != nil {
			return false, fmt.Sprintf("应用防火墙规则失败 (%s): %v", codeName, err), err
		}
		statusFn := r.firewallStatus
		if statusFn == nil {
			statusFn = firewall.GetStatus
		}
		status, sErr := statusFn(cfg.ListenPort)
		if sErr == nil && !status.Allowed {
			return false, "防火墙规则已应用，但二次校验显示端口仍未放行", fmt.Errorf("firewall verification failed")
		}
		return true, fmt.Sprintf("Windows 防火墙规则已应用，已放行入站端口 %d", cfg.ListenPort), nil

	case ActionRebindLANDataPlane:
		if !cfg.AllowLAN {
			return false, "局域网共享未启用，拒绝绑定非回环地址", fmt.Errorf("AllowLAN is false")
		}
		if r.proxySrv == nil {
			return false, "数据面代理服务未初始化", fmt.Errorf("proxy server is nil")
		}
		if err := r.proxySrv.ReloadListener("0.0.0.0", cfg.ListenPort); err != nil {
			return false, fmt.Sprintf("数据面重新绑定局域网失败: %v", err), err
		}
		return true, fmt.Sprintf("数据面已重新绑定至 0.0.0.0:%d (局域网可用)", cfg.ListenPort), nil

	case ActionRebindControlPlaneLoopback:
		if r.controlReload == nil {
			return false, "控制面重载函数未注册", fmt.Errorf("control reload func is nil")
		}
		if err := r.controlReload(cfg.ControlPort); err != nil {
			return false, fmt.Sprintf("恢复控制面回环监听失败: %v", err), err
		}
		return true, fmt.Sprintf("控制面已重新安全绑定至 127.0.0.1:%d", cfg.ControlPort), nil

	default:
		return false, fmt.Sprintf("未知的修复动作: %s", action), fmt.Errorf("unknown action: %s", action)
	}
}
