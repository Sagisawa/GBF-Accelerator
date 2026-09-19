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


const AppVersion = "1.8.0"

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
	mu     sync.RWMutex
	cfg    Config
	path   string
	onSave []func(*Config)
}

func DefaultConfig() Config {
	return Config{
		ListenHost:           "127.0.0.1",
		ListenPort:           8124,
		ControlPort:          8125,
		AllowLAN:             false,
		UpstreamProxy:        "",
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

	// Merge into defaults
	if loaded.ListenHost != "" {
		m.cfg.ListenHost = loaded.ListenHost
	}
	if loaded.ListenPort > 0 {
		m.cfg.ListenPort = loaded.ListenPort
	}
	if loaded.ControlPort > 0 {
		m.cfg.ControlPort = loaded.ControlPort
	}
	m.cfg.AllowLAN = loaded.AllowLAN
	m.cfg.UpstreamProxy = loaded.UpstreamProxy
	m.cfg.DirectMode = loaded.DirectMode
	if loaded.CacheDir != "" {
		m.cfg.CacheDir = NormalizeCacheDir(loaded.CacheDir)
	}
	m.cfg.CleanZombies = loaded.CleanZombies
	m.cfg.AutoSystemProxy = loaded.AutoSystemProxy
	m.cfg.EnableRAMCache = loaded.EnableRAMCache
	if loaded.RAMCacheMaxMB > 0 {
		m.cfg.RAMCacheMaxMB = loaded.RAMCacheMaxMB
	}
	m.cfg.EnableBrowserCache = loaded.EnableBrowserCache
	m.cfg.EnableAutoRepair = loaded.EnableAutoRepair
	m.cfg.EnablePrefetch = loaded.EnablePrefetch
	m.cfg.EnableRAMWarmup = loaded.EnableRAMWarmup
	if loaded.RAMWarmupMaxItems > 0 {
		m.cfg.RAMWarmupMaxItems = loaded.RAMWarmupMaxItems
	}
	m.cfg.VerifyUpstreamTLS = loaded.VerifyUpstreamTLS
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
	m.cfg.ShimakazeMode = loaded.ShimakazeMode
	m.cfg.AutoStart = loaded.AutoStart
	m.cfg.AutoCheckUpdate = loaded.AutoCheckUpdate
	m.cfg.EnableAPITelemetry = loaded.EnableAPITelemetry
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

func (m *Manager) Update(fn func(c *Config)) Config {
	m.mu.Lock()
	fn(&m.cfg)
	updated := m.cfg
	callbacks := make([]func(*Config), len(m.onSave))
	copy(callbacks, m.onSave)
	m.mu.Unlock()

	_ = m.Save()
	for _, cb := range callbacks {
		cb(&updated)
	}
	return updated
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

func (m *Manager) GetEffectiveListenHost() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.AllowLAN {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (m *Manager) GetEffectiveUpstreamProxy() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.DirectMode {
		return ""
	}
	return m.cfg.UpstreamProxy
}

func GetLANIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// GetBaseDir returns the base directory for writable user data (config.json, certs, cache).
// On macOS, if the executable is inside a .app bundle (e.g. .../GBF_Accelerator.app/Contents/MacOS/...),
// writable user data must live in ~/Library/Application Support/GBF-Accelerator to satisfy Gatekeeper.
func GetBaseDir() string {
	if runtime.GOOS == "darwin" {
		if exe, err := os.Executable(); err == nil {
			exeLower := strings.ToLower(filepath.ToSlash(exe))
			if strings.Contains(exeLower, ".app/contents/macos") {
				if home, err := os.UserHomeDir(); err == nil {
					appSupport := filepath.Join(home, "Library", "Application Support", "GBF-Accelerator")
					_ = os.MkdirAll(appSupport, 0755)
					return appSupport
				}
			}
		}
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

