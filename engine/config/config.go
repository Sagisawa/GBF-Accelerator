package config

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
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
		APIMaxConnections:    16,
		APIMaxKeepalive:      4,
		APIKeepaliveExpiry:   20.0,
		AssetMaxConnections:  100,
		AssetMaxKeepalive:    40,
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
	return os.WriteFile(m.path, data, 0644)
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

func NormalizeCacheDir(rawPath string) string {
	if rawPath == "" {
		return filepath.Join("cache", "gbf", "https")
	}
	abs, err := filepath.Abs(rawPath)
	if err != nil {
		return rawPath
	}

	// If directory contains "assets", it's the root
	if fi, err := os.Stat(filepath.Join(abs, "assets")); err == nil && fi.IsDir() {
		return abs
	}
	// If subpath https/assets exists
	if fi, err := os.Stat(filepath.Join(abs, "https", "assets")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "https")
	}
	// If subpath gbf/https exists
	if fi, err := os.Stat(filepath.Join(abs, "gbf", "https", "assets")); err == nil && fi.IsDir() {
		return filepath.Join(abs, "gbf", "https")
	}

	return abs
}
