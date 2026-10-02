package proxy

import (
	"bytes"
	"net"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func createTestProxyServer(t *testing.T, port int) (*ProxyServer, func()) {
	t.Helper()
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = port
		c.EnablePrefetch = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	stats := telemetry.NewStats()
	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)

	cleanup := func() {
		srv.Stop()
		stats.Close()
		cacheMgr.Close()
	}
	return srv, cleanup
}

func assertNoPrefetchWorkerLeak(t *testing.T) {
	t.Helper()
	// 1. Check Go 1.27+ goroutineleak profile if available
	if prof := pprof.Lookup("goroutineleak"); prof != nil {
		var buf bytes.Buffer
		if err := prof.WriteTo(&buf, 1); err != nil {
			t.Logf("failed to write goroutineleak profile: %v", err)
		} else {
			out := buf.String()
			if strings.Contains(out, "(*PrefetchEngine).discoveryWorker") || strings.Contains(out, "(*PrefetchEngine).fetchWorker") {
				t.Fatalf("detected leaked prefetch worker goroutines in goroutineleak profile:\n%s", out)
			}
		}
	}
	// 2. Also check general goroutine profile for any lingering active prefetch workers
	if prof := pprof.Lookup("goroutine"); prof != nil {
		var buf bytes.Buffer
		if err := prof.WriteTo(&buf, 1); err != nil {
			t.Logf("failed to write goroutine profile: %v", err)
		} else {
			out := buf.String()
			if strings.Contains(out, "(*PrefetchEngine).discoveryWorker") || strings.Contains(out, "(*PrefetchEngine).fetchWorker") {
				t.Fatalf("detected lingering active prefetch worker goroutines in goroutine profile:\n%s", out)
			}
		}
	}
}

// TestPrefetchLifecycle_New_StopWithoutStart verifies that calling Stop() on a
// ProxyServer that was instantiated with NewProxyServer() but never Start()ed
// cleanly terminates all PrefetchEngine background workers without leaking goroutines.
func TestPrefetchLifecycle_New_StopWithoutStart(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	if srv.prefetch == nil {
		t.Fatal("expected srv.prefetch to be initialized")
	}
	if srv.prefetch.IsStopped() {
		t.Fatal("expected srv.prefetch not to be stopped yet")
	}

	// Stop without ever calling Start()
	srv.Stop()

	if !srv.prefetch.IsStopped() {
		t.Fatal("expected srv.prefetch to be marked stopped after Stop()")
	}

	// Deterministic WaitGroup-based exit assertion: workers must cleanly exit within timeout
	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers did not exit within timeout after Stop() on unstarted server")
	}

	// Go 1.27.1 pprof goroutineleak verification
	assertNoPrefetchWorkerLeak(t)
}

// TestPrefetchLifecycle_StartFailure_ThenStop verifies that when Start() fails
// (e.g. port already bound), calling Stop() cleanly terminates all PrefetchEngine workers.
func TestPrefetchLifecycle_StartFailure_ThenStop(t *testing.T) {
	// Occupy a port to force Start() to fail
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on dummy port: %v", err)
	}
	defer ln.Close()

	occupiedPort := ln.Addr().(*net.TCPAddr).Port

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = occupiedPort
		c.EnablePrefetch = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	defer srv.Stop()

	// Start must fail due to port collision
	startErr := srv.Start()
	if startErr == nil {
		t.Fatalf("expected Start() to fail on occupied port %d, but succeeded", occupiedPort)
	}

	// Explicitly invoke Stop()
	srv.Stop()

	if !srv.prefetch.IsStopped() {
		t.Fatal("expected srv.prefetch to be stopped after Start() failure and Stop()")
	}

	// Deterministic assertion: workers must terminate cleanly
	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers did not exit within timeout after Start() failure and Stop()")
	}

	assertNoPrefetchWorkerLeak(t)
}

// TestPrefetchLifecycle_StartFailure_SelfCleanup verifies that when Start() fails,
// the Start() error path itself stops prefetch workers even if caller neglects to call Stop().
func TestPrefetchLifecycle_StartFailure_SelfCleanup(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on dummy port: %v", err)
	}
	defer ln.Close()

	occupiedPort := ln.Addr().(*net.TCPAddr).Port

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = occupiedPort
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)

	startErr := srv.Start()
	if startErr == nil {
		srv.Stop()
		t.Fatalf("expected Start() to fail on occupied port %d", occupiedPort)
	}

	// Do NOT call srv.Stop() here — verify Start()'s error path stopped workers
	if !srv.prefetch.IsStopped() {
		t.Fatal("expected srv.prefetch to be stopped by Start() failure handler")
	}

	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers did not exit within timeout after Start() failure")
	}

	assertNoPrefetchWorkerLeak(t)
}

// TestPrefetchLifecycle_NormalStartStop verifies that normal Start() -> Stop()
// continues to operate cleanly and all prefetch workers exit.
func TestPrefetchLifecycle_NormalStartStop(t *testing.T) {
	// Find a free port
	testLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	freePort := testLn.Addr().(*net.TCPAddr).Port
	_ = testLn.Close()

	srv, cleanup := createTestProxyServer(t, freePort)
	defer cleanup()

	if err := srv.Start(); err != nil {
		t.Fatalf("expected Start() to succeed, got: %v", err)
	}

	if !srv.IsRunning() {
		t.Fatal("expected server to be running")
	}
	if srv.prefetch.IsStopped() {
		t.Fatal("expected prefetch engine to be running")
	}

	srv.Stop()

	if srv.IsRunning() {
		t.Fatal("expected server to be stopped")
	}
	if !srv.prefetch.IsStopped() {
		t.Fatal("expected prefetch engine to be stopped")
	}

	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers did not exit within timeout after normal Stop()")
	}
	if !srv.WaitServeLoops(2 * time.Second) {
		t.Fatal("serve loop did not exit within timeout after normal Stop()")
	}

	assertNoPrefetchWorkerLeak(t)
}

// TestPrefetchLifecycle_RestartAfterStop verifies that if a server was stopped
// prior to Start() or after a failed Start(), subsequent successful Start() re-initializes
// the PrefetchEngine cleanly.
func TestPrefetchLifecycle_RestartAfterStop(t *testing.T) {
	testLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	freePort := testLn.Addr().(*net.TCPAddr).Port
	_ = testLn.Close()

	srv, cleanup := createTestProxyServer(t, freePort)
	defer cleanup()

	// Stop before start
	srv.Stop()
	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers failed to exit after pre-start Stop()")
	}

	// Start now — should detect stopped prefetch engine and recreate workers
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server after stop: %v", err)
	}
	if !srv.IsRunning() {
		t.Fatal("expected server to be running after restart")
	}
	if srv.prefetch.IsStopped() {
		t.Fatal("expected recreated prefetch engine to be active")
	}

	srv.Stop()
	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("recreated prefetch workers failed to exit after final Stop()")
	}

	assertNoPrefetchWorkerLeak(t)
}

// TestPrefetchLifecycle_IdempotentConcurrentStop verifies that calling Stop()
// concurrently on unstarted or running server is completely safe without panics or leaks.
func TestPrefetchLifecycle_IdempotentConcurrentStop(t *testing.T) {
	srv, cleanup := createTestProxyServer(t, 19876)
	defer cleanup()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.Stop()
		}()
	}
	wg.Wait()

	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers did not exit cleanly after concurrent Stop() calls")
	}
	assertNoPrefetchWorkerLeak(t)
}

// TestPrefetchLifecycle_ConcurrentStartStopChurn verifies that rapid churn of alternating
// Start() (with and without port collisions), Stop(), and concurrent reads to PrefetchQueueLen()
// executes cleanly without deadlocks, lock inversions, or lingering worker goroutines.
func TestPrefetchLifecycle_ConcurrentStartStopChurn(t *testing.T) {
	lnConflict, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind dummy listener: %v", err)
	}
	defer lnConflict.Close()
	conflictPort := lnConflict.Addr().(*net.TCPAddr).Port

	// Get a free port for successful start attempts
	lnFree, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	freePort := lnFree.Addr().(*net.TCPAddr).Port
	_ = lnFree.Close()

	srv, cleanup := createTestProxyServer(t, conflictPort)
	defer cleanup()

	var wg sync.WaitGroup
	stopSignal := make(chan struct{})

	// Goroutine reading PrefetchQueueLen
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopSignal:
				return
			default:
				_ = srv.PrefetchQueueLen()
				time.Sleep(1 * time.Millisecond)
			}
		}
	}()

	// Churn starts and stops
	for i := 0; i < 20; i++ {
		// Alternate between conflicting port and free port
		if i%2 == 0 {
			srv.cfgMgr.Update(func(c *config.Config) {
				c.ListenPort = conflictPort
			})
			_ = srv.Start() // expected to fail or succeed depending on timing
		} else {
			srv.cfgMgr.Update(func(c *config.Config) {
				c.ListenPort = freePort
			})
			_ = srv.Start()
		}

		if i%3 == 0 {
			srv.Stop()
		}
	}

	close(stopSignal)
	wg.Wait()

	// Final Stop and verification
	srv.Stop()
	if !srv.WaitPrefetchWorkers(2 * time.Second) {
		t.Fatal("prefetch workers failed to terminate after concurrent churn")
	}
	assertNoPrefetchWorkerLeak(t)
}

