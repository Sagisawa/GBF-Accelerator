package tarou

import (
	"strings"
	"testing"
	"time"
)

func TestManagerHeartbeatAndStatus(t *testing.T) {
	m := NewManager(true)
	if m.Status().Connected {
		t.Fatal("new manager must not report connected")
	}
	if err := m.Heartbeat(HeartbeatRequest{
		ProtocolVersion: ProtocolVersion,
		ExtensionVersion: "0.1.0",
		Capabilities: []string{"config", "page-bridge", "config"},
	}); err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	st := m.Status()
	if !st.Connected || st.ExtensionVersion != "0.1.0" {
		t.Fatalf("unexpected status: %+v", st)
	}
	if len(st.Capabilities) != 2 || st.Capabilities[0] != "config" || st.Capabilities[1] != "page-bridge" {
		t.Fatalf("unexpected capabilities: %#v", st.Capabilities)
	}
}

func TestManagerRejectsUnsupportedProtocol(t *testing.T) {
	m := NewManager(true)
	err := m.Heartbeat(HeartbeatRequest{ProtocolVersion: ProtocolVersion + 1})
	if err == nil || !strings.Contains(err.Error(), "unsupported integration protocol") {
		t.Fatalf("expected unsupported protocol error, got %v", err)
	}
	if m.Status().Connected {
		t.Fatal("rejected heartbeat must not connect manager")
	}
}

func TestManagerDisabledAndReenableRequiresFreshHeartbeat(t *testing.T) {
	m := NewManager(true)
	if err := m.Heartbeat(HeartbeatRequest{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	m.SetEnabled(false)
	if m.Status().Connected || m.Status().Enabled {
		t.Fatal("disabled manager should be disconnected and disabled")
	}
	m.SetEnabled(true)
	if m.Status().Connected {
		t.Fatal("reenabling must require a fresh heartbeat")
	}
}

func TestManagerExpiresConnection(t *testing.T) {
	m := NewManager(true)
	if err := m.Heartbeat(HeartbeatRequest{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.lastSeen = time.Now().Add(-HeartbeatTTL - time.Second)
	m.mu.Unlock()
	if m.Status().Connected {
		t.Fatal("stale heartbeat must be disconnected")
	}
}
