package tarou

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	IntegrationID   = "chrome-extension-tarou"
	IntegrationName = "Chrome-Extension-Tarou"
	ProtocolVersion = 1
	SourceURL       = "https://github.com/Waaatanuki/Chrome-Extension-Tarou"
	Author          = "Waaatanuki"
	HeartbeatTTL    = 30 * time.Second
)

type HeartbeatRequest struct {
	ProtocolVersion  int      `json:"protocol_version"`
	ExtensionVersion string   `json:"extension_version,omitempty"`
	Capabilities     []string `json:"capabilities,omitempty"`
}

type Status struct {
	ID                       string   `json:"id"`
	Name                     string   `json:"name"`
	Enabled                  bool     `json:"enabled"`
	Connected                bool     `json:"connected"`
	ProtocolVersion          int      `json:"protocol_version"`
	SupportedProtocolVersion int      `json:"supported_protocol_version"`
	ExtensionVersion         string   `json:"extension_version,omitempty"`
	LastSeenAt               string   `json:"last_seen_at,omitempty"`
	Capabilities             []string `json:"capabilities"`
	SourceURL                string   `json:"source_url"`
	Author                   string   `json:"author"`
	Message                  string   `json:"message,omitempty"`
}

type Manager struct {
	mu               sync.RWMutex
	enabled          bool
	extensionVersion string
	capabilities     []string
	lastSeen         time.Time
}

func NewManager(enabled bool) *Manager {
	return &Manager{enabled: enabled}
}

func (m *Manager) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enabled == enabled {
		return
	}
	m.enabled = enabled
	if !enabled {
		m.extensionVersion = ""
		m.capabilities = nil
		m.lastSeen = time.Time{}
	}
}

func (m *Manager) Heartbeat(req HeartbeatRequest) error {
	if req.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported integration protocol version %d (supported: %d)", req.ProtocolVersion, ProtocolVersion)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled {
		return fmt.Errorf("Tarou integration is disabled")
	}

	m.extensionVersion = strings.TrimSpace(req.ExtensionVersion)
	m.capabilities = normalizeCapabilities(req.Capabilities)
	m.lastSeen = time.Now().UTC()
	return nil
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	connected := m.enabled && !m.lastSeen.IsZero() && time.Since(m.lastSeen) <= HeartbeatTTL
	lastSeen := ""
	if !m.lastSeen.IsZero() {
		lastSeen = m.lastSeen.UTC().Format(time.RFC3339)
	}
	caps := append([]string(nil), m.capabilities...)

	return Status{
		ID:                       IntegrationID,
		Name:                     IntegrationName,
		Enabled:                  m.enabled,
		Connected:                connected,
		ProtocolVersion:          ProtocolVersion,
		SupportedProtocolVersion: ProtocolVersion,
		ExtensionVersion:         m.extensionVersion,
		LastSeenAt:               lastSeen,
		Capabilities:             caps,
		SourceURL:                SourceURL,
		Author:                   Author,
	}
}

func normalizeCapabilities(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	set := make(map[string]struct{}, minInt(len(in), 32))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" || len(v) > 64 {
			continue
		}
		set[v] = struct{}{}
		if len(set) >= 32 {
			break
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
