package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func setupTestProxyForReload(t *testing.T, initialAllowLAN bool) (*ProxyServer, int) {
	t.Helper()
	tempDir := t.TempDir()

	cfgMgr := config.NewManager("")
	cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = initialAllowLAN
		c.ListenPort = 0 // dynamically allocate
		c.DirectMode = true
		c.CacheDir = tempDir
	})

	certMgr, err := cert.NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	cacheMgr := cache.NewManager(tempDir, 16)
	stats := telemetry.NewStats()

	// Find free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = port
	})

	proxySrv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	if err := proxySrv.Start(); err != nil {
		t.Fatalf("failed to start proxy server: %v", err)
	}

	t.Cleanup(func() {
		proxySrv.Stop()
	})

	return proxySrv, port
}

// 1. TestReloadListener_LoopbackToLAN: 127.0.0.1 -> 0.0.0.0
func TestReloadListener_LoopbackToLAN(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)

	// Verify initially loopback
	initialAddr := proxySrv.ListenerAddr()
	if initialAddr != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("expected initial addr 127.0.0.1:%d, got %s", port, initialAddr)
	}

	// Reload to 0.0.0.0
	err := proxySrv.ReloadListener("0.0.0.0", port)
	if err != nil {
		t.Fatalf("failed to reload listener to 0.0.0.0: %v", err)
	}

	// Verify listener address
	newAddr := proxySrv.ListenerAddr()
	if newAddr != fmt.Sprintf("0.0.0.0:%d", port) && newAddr != fmt.Sprintf("[::]:%d", port) {
		t.Errorf("expected 0.0.0.0:%d or [::]:%d, got %s", port, port, newAddr)
	}

	// Verify dial still succeeds
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial after reload: %v", err)
	}
	_ = conn.Close()
}

// 2. TestReloadListener_LANToLoopback: 0.0.0.0 -> 127.0.0.1
func TestReloadListener_LANToLoopback(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, true)

	// Update config to false
	proxySrv.cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = false
	})

	// Reload to 127.0.0.1
	err := proxySrv.ReloadListener("127.0.0.1", port)
	if err != nil {
		t.Fatalf("failed to reload listener to 127.0.0.1: %v", err)
	}

	// Verify 127.0.0.1 dial succeeds
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("expected loopback dial to succeed, got: %v", err)
	}
	_ = conn.Close()

	// Verify LAN ACL blocks non-loopback clients
	fakeLANAddr := &net.TCPAddr{IP: net.ParseIP("192.168.1.100"), Port: 54321}
	if proxySrv.isClientAllowed(fakeLANAddr) {
		t.Errorf("expected isClientAllowed to return false for LAN IP %v when AllowLAN is false", fakeLANAddr)
	}

	// Loopback client must still be allowed
	fakeLoopbackAddr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}
	if !proxySrv.isClientAllowed(fakeLoopbackAddr) {
		t.Errorf("expected isClientAllowed to return true for loopback IP %v", fakeLoopbackAddr)
	}
}

// 3. TestReloadListener_InvalidTargetKeepsOld: Rollback on occupied port
func TestReloadListener_InvalidTargetKeepsOld(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)

	// Occupy a target port
	conflictLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind conflict port: %v", err)
	}
	conflictPort := conflictLn.Addr().(*net.TCPAddr).Port
	defer conflictLn.Close()

	// Try reload to conflict port
	err = proxySrv.ReloadListener("127.0.0.1", conflictPort)
	if err == nil {
		t.Fatalf("expected ReloadListener to fail on occupied port, but got nil")
	}

	// Verify original listener was rolled back and still works
	conn, dErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if dErr != nil {
		t.Fatalf("expected original listener to remain accessible after rollback, got error: %v", dErr)
	}
	_ = conn.Close()
}

// 4. TestReloadListener_ExistingConnectionSurvives: Keep-Alive connection survives rebind
func TestReloadListener_ExistingConnectionSurvives(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)

	// Establish connection A before reload
	connA, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to establish connection A: %v", err)
	}
	defer connA.Close()

	// Send HTTP request 1 on connection A (request /ca.crt)
	req1 := "GET /ca.crt HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: keep-alive\r\n\r\n"
	if _, err := connA.Write([]byte(req1)); err != nil {
		t.Fatalf("failed to write req1: %v", err)
	}
	brA := bufio.NewReader(connA)
	resp1, err := http.ReadResponse(brA, nil)
	if err != nil {
		t.Fatalf("failed to read resp1: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp1.StatusCode)
	}

	// Trigger listener reload (127.0.0.1 -> 0.0.0.0)
	err = proxySrv.ReloadListener("0.0.0.0", port)
	if err != nil {
		t.Fatalf("ReloadListener failed: %v", err)
	}

	// Send HTTP request 2 on the SAME existing connection A
	req2 := "GET /proxy.pac HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"
	if _, err := connA.Write([]byte(req2)); err != nil {
		t.Fatalf("failed to write req2 on existing connection: %v", err)
	}
	resp2, err := http.ReadResponse(brA, nil)
	if err != nil {
		t.Fatalf("existing connection failed after reload: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 on resp2, got %d", resp2.StatusCode)
	}

	// New connection B established after reload operates properly
	connB, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("failed to establish connection B: %v", err)
	}
	defer connB.Close()

	reqB := "GET /ca.crt HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"
	if _, err := connB.Write([]byte(reqB)); err != nil {
		t.Fatalf("failed to write reqB: %v", err)
	}
	brB := bufio.NewReader(connB)
	respB, err := http.ReadResponse(brB, nil)
	if err != nil {
		t.Fatalf("failed to read respB: %v", err)
	}
	_ = respB.Body.Close()
	if respB.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 on respB, got %d", respB.StatusCode)
	}
}

// 5. TestReloadListener_ConcurrentReloadStop: Race and concurrency safety
func TestReloadListener_ConcurrentReloadStop(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)

	var wg sync.WaitGroup
	hosts := []string{"127.0.0.1", "0.0.0.0"}

	// Launch concurrent reloads
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			targetHost := hosts[idx%len(hosts)]
			_ = proxySrv.ReloadListener(targetHost, port)
		}(i)
	}

	// Stop concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		proxySrv.Stop()
	}()

	wg.Wait()
}
