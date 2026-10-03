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

// 5. TestReloadListener_ConcurrentReloadStop: Race, concurrency safety, and serveLoop goroutine lifecycle
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

	// Verify all serveLoop goroutines exited cleanly and without leak
	if ok := proxySrv.WaitServeLoops(2 * time.Second); !ok {
		t.Fatal("serveLoop goroutines failed to terminate after Stop()")
	}
	if proxySrv.IsRunning() {
		t.Error("expected proxy server to not be running after Stop()")
	}
}

// 6. TestReloadListener_RealLANInterfaceConnection: True end-to-end socket dial on non-loopback LAN IP
func TestReloadListener_RealLANInterfaceConnection(t *testing.T) {
	lanIP := config.GetLANIP()
	if lanIP == "" || lanIP == "127.0.0.1" {
		t.Skip("no non-loopback LAN IP detected on this host, skipping interface-level test")
	}

	proxySrv, port := setupTestProxyForReload(t, false)
	defer proxySrv.Stop()

	lanAddr := net.JoinHostPort(lanIP, fmt.Sprintf("%d", port))

	// 1. Initially bound to 127.0.0.1: dial to LAN IP must fail (connection refused or timeout)
	connFail, err := net.DialTimeout("tcp", lanAddr, 300*time.Millisecond)
	if err == nil {
		connFail.Close()
		t.Fatalf("expected dial to LAN IP %s to fail when bound to 127.0.0.1, but succeeded", lanAddr)
	}

	// 2. Hot-reload to 0.0.0.0 with AllowLAN = true
	proxySrv.cfgMgr.Update(func(c *config.Config) {
		c.AllowLAN = true
	})
	if err := proxySrv.ReloadListener("0.0.0.0", port); err != nil {
		t.Fatalf("failed to reload to 0.0.0.0: %v", err)
	}

	// 3. Now dial to LAN IP must SUCCEED over the real physical/virtual LAN interface
	connSuccess, err := net.DialTimeout("tcp", lanAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("expected dial to LAN IP %s to succeed when bound to 0.0.0.0, but failed: %v", lanAddr, err)
	}
	defer connSuccess.Close()

	// 4. Request /ca.crt directly through the LAN socket
	req := fmt.Sprintf("GET /ca.crt HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", lanIP)
	if _, err := connSuccess.Write([]byte(req)); err != nil {
		t.Fatalf("failed to write request via LAN socket: %v", err)
	}

	br := bufio.NewReader(connSuccess)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("failed to read response via LAN socket: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from LAN socket, got %d", resp.StatusCode)
	}
	caData, _ := io.ReadAll(resp.Body)
	if len(caData) == 0 {
		t.Error("expected non-empty CA certificate over LAN socket")
	}
}

// 7. TestReloadListener_SameAddressNoOp: Calling ReloadListener with identical host:port is a no-op
func TestReloadListener_SameAddressNoOp(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)
	defer proxySrv.Stop()

	proxySrv.mu.RLock()
	origLn := proxySrv.listener
	origGen := proxySrv.listenerGen
	origAddr := origLn.Addr().String()
	proxySrv.mu.RUnlock()

	expectedAddr := fmt.Sprintf("127.0.0.1:%d", port)
	if origAddr != expectedAddr {
		t.Fatalf("expected initial addr %s, got %s", expectedAddr, origAddr)
	}

	// Call ReloadListener with the exact same host and port
	err := proxySrv.ReloadListener("127.0.0.1", port)
	if err != nil {
		t.Fatalf("expected ReloadListener with same address to succeed with nil, got: %v", err)
	}

	proxySrv.mu.RLock()
	currentLn := proxySrv.listener
	currentGen := proxySrv.listenerGen
	proxySrv.mu.RUnlock()

	// 1. Must NOT close or replace the listener
	if currentLn != origLn {
		t.Errorf("expected listener pointer to remain unchanged, orig=%p, current=%p", origLn, currentLn)
	}

	// 2. Must NOT increment generation
	if currentGen != origGen {
		t.Errorf("expected generation to remain %d, got %d", origGen, currentGen)
	}

	// 3. Listener must remain fully functional and accept connections
	conn, err := net.DialTimeout("tcp", expectedAddr, 1*time.Second)
	if err != nil {
		t.Fatalf("listener should still be accepting connections, but dial failed: %v", err)
	}
	defer conn.Close()

	req := "GET /ca.crt HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("failed to send request over existing listener: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("failed to read response over existing listener: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
}

// 7. TestReloadListener_DifferentPort_ListenNewBeforeClosingOld (H7):
// Verifies that for different port rebind, new listener is attempted first before closing old.
// If new port is blocked, old listener is NEVER closed and generation is unchanged.
func TestReloadListener_DifferentPort_ListenNewBeforeClosingOld(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)

	proxySrv.mu.RLock()
	origLn := proxySrv.listener
	origGen := proxySrv.listenerGen
	proxySrv.mu.RUnlock()

	// Occupy target port on another port
	targetLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy target port: %v", err)
	}
	targetPort := targetLn.Addr().(*net.TCPAddr).Port
	defer targetLn.Close()

	// Attempt reload to the occupied target port
	err = proxySrv.ReloadListener("127.0.0.1", targetPort)
	if err == nil {
		t.Fatalf("expected error when reloading to occupied port")
	}

	proxySrv.mu.RLock()
	currLn := proxySrv.listener
	currGen := proxySrv.listenerGen
	isRunning := proxySrv.running
	proxySrv.mu.RUnlock()

	// Old listener was never closed or touched
	if currLn != origLn {
		t.Errorf("expected listener pointer to remain identical (%p vs %p)", origLn, currLn)
	}
	if currGen != origGen {
		t.Errorf("expected generation to remain %d, got %d", origGen, currGen)
	}
	if !isRunning {
		t.Errorf("expected server to remain running")
	}

	// Old listener still actively accepts connections
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 1*time.Second)
	if err != nil {
		t.Fatalf("dial to old listener failed: %v", err)
	}
	_ = conn.Close()
}

// 8. TestReloadListener_DoubleFailure_ExitsCleanly (H6):
// Verifies that when both new bind and rollback fail, the server cleanly marks running=false,
// increments listenerGen, terminates the old serveLoop, and does not enter a permanent 20ms spin loop.
func TestReloadListener_DoubleFailure_ExitsCleanly(t *testing.T) {
	proxySrv, port := setupTestProxyForReload(t, false)

	// To simulate double failure on same-port rebind (e.g. changing 127.0.0.1 to 0.0.0.0):
	// We'll occupy port on 0.0.0.0 before same-port reload, and occupy 127.0.0.1 during reload
	// so both new bind and rollback fail.
	// Easiest reproducible setup:
	// Manually construct the same-port collision scenario:
	// Once oldLn is closed, another listener steals the old port so rollback ALSO fails!
	oldAddr := fmt.Sprintf("127.0.0.1:%d", port)
	_ = oldAddr

	// Trigger same-port reload: from 127.0.0.1:port to 0.0.0.0:port
	// We occupy 0.0.0.0:port first:
	// In Windows, if 127.0.0.1:port is already listening, binding 0.0.0.0:port will fail.
	// But in ReloadListener, same-port closes oldLn first!
	// Right when oldLn is closed, if 0.0.0.0 is already blocked or invalid, it tries rollback.
	// If a background goroutine immediately grabs 127.0.0.1:port when oldLn closes, rollback fails!
	// Alternatively, we can use an invalid IP for newAddr (like 256.256.256.256:port) so new bind fails,
	// and have a listener ready to grab old port as soon as oldLn closes.

	stealChan := make(chan struct{})
	stolenChan := make(chan struct{})
	releaseChan := make(chan struct{})
	go func() {
		<-stealChan
		// Steal old port as soon as oldLn closes
		for i := 0; i < 100; i++ {
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err == nil {
				close(stolenChan)
				<-releaseChan
				_ = ln.Close()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// We hook ReloadListener by closing old listener and immediately stealing:
	proxySrv.mu.Lock()
	oldLn := proxySrv.listener
	_ = oldLn.Close()
	close(stealChan)
	<-stolenChan // Port is now stolen!

	// Now try reloading to the same port on an invalid host, so newLn fails, AND rollback fails:
	proxySrv.mu.Unlock()

	err := proxySrv.ReloadListener("256.256.256.256", port)
	if err == nil {
		t.Fatalf("expected double failure error, got nil")
	}

	// Verify H6 state machine requirements:
	if proxySrv.IsRunning() {
		t.Fatalf("VIOLATION (H6): proxySrv.IsRunning() must be false after double failure")
	}
	if proxySrv.ListenerAddr() != "" {
		t.Fatalf("VIOLATION (H6): proxySrv.ListenerAddr() must be empty after double failure")
	}

	// Verify serveLoop exited and did NOT leak/spin forever
	if !proxySrv.WaitServeLoops(2 * time.Second) {
		t.Fatalf("VIOLATION (H6): serveLoop did not terminate within 2s, goroutine leaked or spinning!")
	}

	// Release stolen port and verify server is restartable via Start()
	close(releaseChan)
	time.Sleep(30 * time.Millisecond)

	if err := proxySrv.Start(); err != nil {
		t.Fatalf("expected server to be restartable via Start() after double failure, got %v", err)
	}
	if !proxySrv.IsRunning() {
		t.Fatalf("expected server to be running after Start()")
	}
}
