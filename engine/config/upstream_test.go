package config

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func TestProbePortsContainsACGPower(t *testing.T) {
	found := false
	for _, p := range probePorts {
		if p.port == 8123 {
			found = true
			if p.name != "ACGPower (HTTP)" {
				t.Errorf("expected port 8123 name to be 'ACGPower (HTTP)', got '%s'", p.name)
			}
		}
	}
	if !found {
		t.Error("expected probePorts to include port 8123 (ACGPower)")
	}
}

func TestIsPortOpen(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on dynamic port: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	if !IsPortOpen("127.0.0.1", port, 500*time.Millisecond) {
		t.Errorf("expected port %d to be open", port)
	}

	// Close listener and verify port is closed
	_ = ln.Close()
	time.Sleep(50 * time.Millisecond)
	if IsPortOpen("127.0.0.1", port, 100*time.Millisecond) {
		t.Errorf("expected port %d to be closed after listener stopped", port)
	}
}

func TestDetectUpstreamProxiesWithMockACGPower(t *testing.T) {
	// If 8123 is already open (e.g. real ACGPower running in test environment), verify it is detected.
	if IsPortOpen("127.0.0.1", 8123, 100*time.Millisecond) {
		candidates := DetectUpstreamProxies()
		var found bool
		for _, cand := range candidates {
			if cand.URL == "http://127.0.0.1:8123" {
				found = true
				if cand.Name != "ACGPower (HTTP)" {
					t.Errorf("expected candidate name 'ACGPower (HTTP)', got '%s'", cand.Name)
				}
				break
			}
		}
		if !found {
			t.Errorf("expected 127.0.0.1:8123 to be detected among candidates: %+v", candidates)
		}
		return
	}

	// Otherwise, attempt to bind 127.0.0.1:8123 for testing
	ln, err := net.Listen("tcp", "127.0.0.1:8123")
	if err != nil {
		t.Logf("port 8123 bind error (%v), skipping mock listener detection", err)
		return
	}
	defer ln.Close()

	candidates := DetectUpstreamProxies()
	var acgpCandidate *ProxyCandidate
	for i := range candidates {
		if candidates[i].URL == "http://127.0.0.1:8123" {
			acgpCandidate = &candidates[i]
			break
		}
	}

	if acgpCandidate == nil {
		t.Errorf("expected DetectUpstreamProxies to find http://127.0.0.1:8123, got: %+v", candidates)
	} else {
		if acgpCandidate.Name != "ACGPower (HTTP)" {
			t.Errorf("expected candidate name 'ACGPower (HTTP)', got '%s'", acgpCandidate.Name)
		}
	}
}

func TestAutoDetectUpstreamProxyFallback(t *testing.T) {
	// Verify that AutoDetectUpstreamProxy returns a non-empty URL
	u := AutoDetectUpstreamProxy()
	if u == "" {
		t.Error("expected AutoDetectUpstreamProxy to return non-empty URL")
	}
	// URL must start with http:// or socks5://
	if len(u) < 7 || (u[:7] != "http://" && u[:8] != "socks5://") {
		t.Errorf("expected valid proxy URL scheme, got %s", u)
	}
	_ = fmt.Sprintf("%s", u)
}
