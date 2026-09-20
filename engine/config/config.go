package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)


const AppVersion = "2.0.0"

type Config struct {
	ListenHost           string  `json:"listen_host"`
	ListenPort           int     `json:"listen_port"`
	ControlPort          int     `json:"control_port"`
	AllowLAN             bool    `json:"allow_lan"`
	UpstreamProxy        string  `json:"upstream_proxy"`
	DirectMode           bool    `json:"direct_mode"`
	CacheDir             string  `json:"cache_dir"`
	CleanZombies         bool    `json:"clean_zombies"`
	AutoSystemProxy      bool    `json:"auto_system_proxy"`
	EnableRAMCache       bool    `json:"enable_ram_cache"`
	RAMCacheMaxMB        int     `json:"ram_cache_max_mb"`
	EnableBrowserCache   bool    `json:"enable_browser_cache"`
	EnableAutoRepair     bool    `json:"enable_auto_repair"`
	EnablePrefetch       bool    `json:"enable_prefetch"`
	EnableRAMWarmup      bool    `json:"enable_ram_warmup"`
	RAMWarmupMaxItems    int     `json:"ram_warmup_max_items"`
	VerifyUpstreamTLS    bool    `json:"verify_upstream_tls"`
	ShimakazeMode        bool    `json:"shimakaze_mode"`
	AutoStart            bool    `json:"auto_start"`
	AutoCheckUpdate      bool    `json:"auto_check_update"`
	APIMaxConnections    int     `json:"api_max_connections"`
	APIMaxKeepalive      int     `json:"api_max_keepalive"`
	APIKeepaliveExpiry   float64 `json:"api_keepalive_expiry"`
	AssetMaxConnections  int     `json:"asset_max_connections"`
	AssetMaxKeepalive    int     `json:"asset_max_keepalive"`
	AssetKeepaliveExpiry float64 `json:"asset_keepalive_expiry"`
	EnableAPITelemetry   bool    `json:"enable_api_telemetry"`
}

type Manager struct {
	commitMu sync.Mutex
	mu       sync.RWMutex
	cfg      Config
	path     string
	onSave   []func(*Config)
}

func DefaultConfig() Config {
	return Config{
		ListenHost:           "127.0.0.1",
		ListenPort:           8124,
		ControlPort:          8125,
		AllowLAN:             false,
		UpstreamProxy:        "auto",
		DirectMode:           false,
		CacheDir:             filepath.Join("cache", "gbf", "https"),
		CleanZombies:         true,
		AutoSystemProxy:      false,
		EnableRAMCache:       true,
		RAMCacheMaxMB:        256,
		EnableBrowserCache:   true,
		EnableAutoRepair:     true,
		EnablePrefetch:       true,
		EnableRAMWarmup:      false,
		RAMWarmupMaxItems:    1500,
		VerifyUpstreamTLS:    true,
		ShimakazeMode:        false,
		AutoStart:            false,
		AutoCheckUpdate:      true,
		APIMaxConnections:    16,
		APIMaxKeepalive:      4,
		APIKeepaliveExpiry:   20.0,
		AssetMaxConnections:  32,
		AssetMaxKeepalive:    16,
		AssetKeepaliveExpiry: 60.0,
		EnableAPITelemetry:   true,
	}
}

func NewManager(cfgPath string) *Manager {
	m := &Manager{
		cfg:  DefaultConfig(),
		path: cfgPath,
	}
	if cfgPath != "" {
		_ = m.Load(cfgPath)
	}
	return m
}

func normalizeRAMCacheMaxMB(v int) int {
	const minMB = 16
	const maxMB = 8192
	if v < minMB {
		return minMB
	}
	if v > maxMB {
		return maxMB
	}
	return v
}

func (m *Manager) Load(cfgPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.path = cfgPath

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}

	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return err
	}

	// Merge into defaults; only overwrite fields whose keys are actually present.
	// This preserves defaults for older or hand-edited partial configuration files.
	if loaded.ListenHost != "" {
		m.cfg.ListenHost = loaded.ListenHost
	}
	if loaded.ListenPort > 0 {
		m.cfg.ListenPort = loaded.ListenPort
	}
	if loaded.ControlPort > 0 {
		m.cfg.ControlPort = loaded.ControlPort
	}
	if _, ok := present["allow_lan"]; ok {
		m.cfg.AllowLAN = loaded.AllowLAN
	}
	if _, ok := present["upstream_proxy"]; ok {
		m.cfg.UpstreamProxy = loaded.UpstreamProxy
	}
	if _, ok := present["direct_mode"]; ok {
		m.cfg.DirectMode = loaded.DirectMode
	}
	if loaded.CacheDir != "" {
		m.cfg.CacheDir = NormalizeCacheDir(loaded.CacheDir)
	}
	if _, ok := present["clean_zombies"]; ok {
		m.cfg.CleanZombies = loaded.CleanZombies
	}
	if _, ok := present["auto_system_proxy"]; ok {
		m.cfg.AutoSystemProxy = loaded.AutoSystemProxy
	}
	if _, ok := present["enable_ram_cache"]; ok {
		m.cfg.EnableRAMCache = loaded.EnableRAMCache
	}
	if _, ok := present["ram_cache_max_mb"]; ok {
		m.cfg.RAMCacheMaxMB = normalizeRAMCacheMaxMB(loaded.RAMCacheMaxMB)
	}
	if _, ok := present["enable_browser_cache"]; ok {
		m.cfg.EnableBrowserCache = loaded.EnableBrowserCache
	}
	if _, ok := present["enable_auto_repair"]; ok {
		m.cfg.EnableAutoRepair = loaded.EnableAutoRepair
	}
	if _, ok := present["enable_prefetch"]; ok {
		m.cfg.EnablePrefetch = loaded.EnablePrefetch
	}
	if _, ok := present["enable_ram_warmup"]; ok {
		m.cfg.EnableRAMWarmup = loaded.EnableRAMWarmup
	}
	if loaded.RAMWarmupMaxItems > 0 {
		m.cfg.RAMWarmupMaxItems = loaded.RAMWarmupMaxItems
	}
	if _, ok := present["verify_upstream_tls"]; ok {
		m.cfg.VerifyUpstreamTLS = loaded.VerifyUpstreamTLS
	}
	if loaded.APIMaxConnections > 0 {
		m.cfg.APIMaxConnections = loaded.APIMaxConnections
	}
	if loaded.APIMaxKeepalive > 0 {
		m.cfg.APIMaxKeepalive = loaded.APIMaxKeepalive
	}
	if loaded.AssetMaxConnections > 0 {
		m.cfg.AssetMaxConnections = loaded.AssetMaxConnections
	}
	if loaded.AssetMaxKeepalive > 0 {
		m.cfg.AssetMaxKeepalive = loaded.AssetMaxKeepalive
	}
	if _, ok := present["shimakaze_mode"]; ok {
		m.cfg.ShimakazeMode = loaded.ShimakazeMode
	}
	if _, ok := present["auto_start"]; ok {
		m.cfg.AutoStart = loaded.AutoStart
	}
	if _, ok := present["auto_check_update"]; ok {
		m.cfg.AutoCheckUpdate = loaded.AutoCheckUpdate
	}
	if _, ok := present["enable_api_telemetry"]; ok {
		m.cfg.EnableAPITelemetry = loaded.EnableAPITelemetry
	}
	if loaded.APIKeepaliveExpiry > 0 {
		m.cfg.APIKeepaliveExpiry = loaded.APIKeepaliveExpiry
	}
	if loaded.AssetKeepaliveExpiry > 0 {
		m.cfg.AssetKeepaliveExpiry = loaded.AssetKeepaliveExpiry
	}
	return nil
}

func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Candidate() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Commit(candidate Config) error {
	candidate.RAMCacheMaxMB = normalizeRAMCacheMaxMB(candidate.RAMCacheMaxMB)
	m.commitMu.Lock()

	m.mu.Lock()
	oldCfg := m.cfg
	m.cfg = candidate
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		m.mu.Lock()
		m.cfg = oldCfg
		m.mu.Unlock()
		m.commitMu.Unlock()
		return fmt.Errorf("failed to save config: %w", err)
	}

	m.mu.RLock()
	updated := m.cfg
	callbacks := make([]func(*Config), len(m.onSave))
	copy(callbacks, m.onSave)
	m.mu.RUnlock()

	// Release commitMu before invoking callbacks to avoid re-entrant deadlock
	// if a callback initiates a nested config commit or query.
	m.commitMu.Unlock()

	for _, cb := range callbacks {
		cb(&updated)
	}
	return nil
}

func (m *Manager) Update(fn func(c *Config)) Config {
	updated, _ := m.UpdateWithError(fn)
	return updated
}

func (m *Manager) UpdateWithError(fn func(c *Config)) (Config, error) {
	m.commitMu.Lock()

	m.mu.Lock()
	oldCfg := m.cfg
	fn(&m.cfg)
	m.cfg.RAMCacheMaxMB = normalizeRAMCacheMaxMB(m.cfg.RAMCacheMaxMB)
	updated := m.cfg
	callbacks := make([]func(*Config), len(m.onSave))
	copy(callbacks, m.onSave)
	m.mu.Unlock()

	if err := m.Save(); err != nil {
		m.mu.Lock()
		m.cfg = oldCfg
		m.mu.Unlock()
		m.commitMu.Unlock()
		return oldCfg, fmt.Errorf("failed to save config: %w", err)
	}
	m.commitMu.Unlock()

	for _, cb := range callbacks {
		cb(&updated)
	}
	return updated, nil
}

func (m *Manager) OnUpdate(cb func(*Config)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSave = append(m.onSave, cb)
}

func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", m.path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	var renameErr error
	for i := 0; i < 3; i++ {
		renameErr = os.Rename(tmpPath, m.path)
		if renameErr == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = os.Remove(tmpPath)
	return renameErr
}

func (c Config) GetEffectiveListenHost() string {
	if c.AllowLAN {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (c Config) GetEffectiveUpstreamProxy() string {
	if c.DirectMode {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(c.UpstreamProxy), "auto") {
		return AutoDetectUpstreamProxy()
	}
	return c.UpstreamProxy
}

func (m *Manager) GetEffectiveListenHost() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.GetEffectiveListenHost()
}

func (m *Manager) GetEffectiveUpstreamProxy() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.GetEffectiveUpstreamProxy()
}

func isPrivateRFC1918(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if ip4[0] == 10 {
		return true
	}
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	return false
}

func GetLANIP() string {
	// 1. Prefer enumerating active, non-virtual physical interfaces
	if ifaces, err := net.Interfaces(); err == nil {
		var fallbackIP string
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			nameLower := strings.ToLower(iface.Name)
			// Skip known TUN/TAP/virtual interfaces (e.g. Clash, Mihomo, WinTun, Tailscale, ZeroTier, Docker)
			if strings.Contains(nameLower, "tun") || strings.Contains(nameLower, "tap") ||
				strings.Contains(nameLower, "wintun") || strings.Contains(nameLower, "mihomo") ||
				strings.Contains(nameLower, "clash") || strings.Contains(nameLower, "tailscale") ||
				strings.Contains(nameLower, "zerotier") || strings.Contains(nameLower, "docker") ||
				strings.Contains(nameLower, "vethernet") {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip == nil || ip.IsLoopback() || ip.To4() == nil {
					continue
				}
				ip4 := ip.To4()
				if isPrivateRFC1918(ip4) {
					return ip4.String()
				}
				if fallbackIP == "" && !ip4.IsLinkLocalUnicast() && !strings.HasPrefix(ip4.String(), "198.18.") && !strings.HasPrefix(ip4.String(), "198.19.") {
					fallbackIP = ip4.String()
				}
			}
		}
		if fallbackIP != "" {
			return fallbackIP
		}
	}

	// 2. Fallback to UDP routing socket
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	ipStr := localAddr.IP.String()
	// Disallow virtual benchmark / fake-IP subnet (RFC 2544, 198.18.0.0/15)
	if strings.HasPrefix(ipStr, "198.18.") || strings.HasPrefix(ipStr, "198.19.") {
		return ""
	}
	return ipStr
}

// GetBaseDir returns the base directory for writable user data (config.json, certs, cache).
// On macOS, if the executable is inside a .app bundle (e.g. .../GBF_Accelerator.app/Contents/MacOS/...),
// writable user data must live in ~/Library/Application Support/GBF-Accelerator to satisfy Gatekeeper.
func GetBaseDir() string {
	// Released portable builds keep writable data beside the executable.
	// macOS .app bundles are the exception: bundle contents may be read-only,
	// so writable data is stored in the user's Application Support directory.
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if runtime.GOOS == "darwin" {
			exeLower := strings.ToLower(filepath.ToSlash(exe))
			if strings.Contains(exeLower, ".app/contents/macos") {
				if home, err := os.UserHomeDir(); err == nil {
					appSupport := filepath.Join(home, "Library", "Application Support", "GBF-Accelerator")
					_ = os.MkdirAll(appSupport, 0755)
					return appSupport
				}
			}
		}
		if abs, err := filepath.Abs(exeDir); err == nil {
			return abs
		}
		return exeDir
	}
	return "."
}

func NormalizeCacheDir(rawPath string) string {
	if rawPath == "" {
		return filepath.Join("cache", "gbf", "https")
	}
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		abs = rawPath
	}

	// 0. If user selected 'assets' folder directly, its parent is the actual cache root
	if strings.EqualFold(filepath.Base(abs), "assets") {
		parent := filepath.Dir(abs)
		if fi, err := os.Stat(filepath.Join(parent, "assets")); err == nil && fi.IsDir() {
			return parent
		}
	}

	// 1. If directory contains "assets", it is already the exact target root
	if fi, err := os.Stat(filepath.Join(abs, "assets")); err == nil && fi.IsDir() {
		return abs
	}

	// 2. Check .../https/assets
	if fi, err := os.Stat(filepath.Join(abs, "https", "assets")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "https")
	}
	if strings.EqualFold(filepath.Base(abs), "gbf") {
		if fi, err := os.Stat(filepath.Join(abs, "https")); err == nil && fi.IsDir() {
			return filepath.Join(abs, "https")
		}
	}

	// 3. Check .../gbf/https/assets (or .../gbf/https)
	if fi, err := os.Stat(filepath.Join(abs, "gbf", "https", "assets")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "gbf", "https")
	}
	if strings.EqualFold(filepath.Base(abs), "cache") {
		if fi, err := os.Stat(filepath.Join(abs, "gbf", "https")); err == nil && fi.IsDir() {
			return filepath.Join(abs, "gbf", "https")
		}
	}

	// 4. Check ACGPower root: .../cache/gbf/https/assets (or .../cache/gbf/https)
	if fi, err := os.Stat(filepath.Join(abs, "cache", "gbf", "https", "assets")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "cache", "gbf", "https")
	}
	if fi, err := os.Stat(filepath.Join(abs, "cache", "gbf", "https")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "cache", "gbf", "https")
	}

	// 5. Check Mac ACGPower: .../cache/gbf/assets
	if fi, err := os.Stat(filepath.Join(abs, "cache", "gbf", "assets")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "cache", "gbf")
	}

	return abs
}

