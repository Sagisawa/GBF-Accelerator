package proxy

import (
	"errors"
	"net"
	"sync"
	"time"
)

type failoverRoute uint8

const (
	failoverRoutePrimary failoverRoute = iota
	failoverRouteBackup
)

func (r failoverRoute) String() string {
	if r == failoverRouteBackup { return "backup" }
	return "primary"
}

type failoverTransition struct {
	from failoverRoute
	to failoverRoute
	reason string
}

type UpstreamRuntimeStatus struct {
	Enabled bool `json:"enabled"`
	BackupConfigured bool `json:"backup_configured"`
	Active string `json:"active"`
	FailureCount int `json:"failure_count"`
	LastSwitchAt *time.Time `json:"last_switch_at,omitempty"`
	Reason string `json:"reason,omitempty"`
	AutoRecover bool `json:"auto_recover"`
	ThresholdMS int `json:"threshold_ms"`
	ConsecutiveFailures int `json:"consecutive_failures"`
	CooldownSeconds int `json:"cooldown_seconds"`
}

type failoverManager struct {
	mu sync.Mutex
	enabled bool
	backupConfigured bool
	threshold time.Duration
	consecutive int
	cooldown time.Duration
	autoRecover bool
	active failoverRoute
	failureCount int
	lastSwitchAt time.Time
	lastFailureAt time.Time
	trialInFlight bool
	reason string
}

func newFailoverManager() *failoverManager {
	return &failoverManager{
		active: failoverRoutePrimary,
		threshold: 2 * time.Second,
		consecutive: 3,
		cooldown: 60 * time.Second,
		autoRecover: true,
	}
}

func (m *failoverManager) configure(enabled, backupConfigured bool, threshold time.Duration, consecutive int, cooldown time.Duration, autoRecover bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enabled = enabled && backupConfigured
	m.backupConfigured = backupConfigured
	if threshold < 0 { threshold = 0 }
	if consecutive < 1 { consecutive = 1 }
	if cooldown < time.Second { cooldown = 60 * time.Second }
	m.threshold, m.consecutive, m.cooldown, m.autoRecover = threshold, consecutive, cooldown, autoRecover
	m.trialInFlight = false

	if !m.enabled {
		m.active, m.failureCount, m.reason = failoverRoutePrimary, 0, ""
		return
	}
	if m.active != failoverRouteBackup {
		m.active, m.failureCount = failoverRoutePrimary, 0
	}
}

func (m *failoverManager) thresholdDuration() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.threshold
}

func (m *failoverManager) activeRoute() failoverRoute {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

func (m *failoverManager) routeForRequest(allowRecovery bool, now time.Time) (failoverRoute, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.active != failoverRouteBackup || !m.enabled || !m.backupConfigured ||
		!m.autoRecover || !allowRecovery || m.lastSwitchAt.IsZero() ||
		m.trialInFlight || now.Sub(m.lastSwitchAt) < m.cooldown {
		return m.active, false
	}
	m.trialInFlight = true
	return failoverRoutePrimary, true
}

func (m *failoverManager) observe(route failoverRoute, trial bool, elapsed time.Duration, err error, now time.Time) *failoverTransition {
	m.mu.Lock()
	defer m.mu.Unlock()

	bad := err != nil || (m.threshold > 0 && elapsed >= m.threshold)

	if trial {
		if m.active != failoverRouteBackup || !m.trialInFlight { return nil }
		m.trialInFlight = false
		if bad {
			m.lastFailureAt, m.lastSwitchAt = now, now
			m.reason = "primary_recovery_failed"
			return nil
		}
		from := m.active
		m.active, m.failureCount = failoverRoutePrimary, 0
		m.lastSwitchAt = now
		m.reason = "primary_recovered"
		return &failoverTransition{from: from, to: m.active, reason: m.reason}
	}

	if !m.enabled || !m.backupConfigured || route != m.active || route != failoverRoutePrimary {
		return nil
	}
	if !bad {
		m.failureCount = 0
		return nil
	}

	m.failureCount++
	m.lastFailureAt = now
	if m.failureCount < m.consecutive { return nil }

	from := m.active
	m.active, m.failureCount = failoverRouteBackup, 0
	m.lastSwitchAt = now
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			m.reason = "timeout"
		} else {
			m.reason = "connection_error"
		}
	} else {
		m.reason = "latency_threshold"
	}
	return &failoverTransition{from: from, to: m.active, reason: m.reason}
}

func (m *failoverManager) status() UpstreamRuntimeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	var lastSwitchAt *time.Time
	if !m.lastSwitchAt.IsZero() {
		t := m.lastSwitchAt
		lastSwitchAt = &t
	}
	return UpstreamRuntimeStatus{
		Enabled: m.enabled,
		BackupConfigured: m.backupConfigured,
		Active: m.active.String(),
		FailureCount: m.failureCount,
		LastSwitchAt: lastSwitchAt,
		Reason: m.reason,
		AutoRecover: m.autoRecover,
		ThresholdMS: int(m.threshold / time.Millisecond),
		ConsecutiveFailures: m.consecutive,
		CooldownSeconds: int(m.cooldown / time.Second),
	}
}
