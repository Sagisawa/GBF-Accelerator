package proxy

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func newTestProxyForNotification(t *testing.T, enableNotification bool) (*ProxyServer, *config.Manager) {
	t.Helper()
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.EnablePrefetch = false
		c.EnableUpstreamFailover = true
		c.UpstreamProxy = "http://127.0.0.1:8080"
		c.BackupUpstreamProxy = "http://127.0.0.1:8081"
		c.UpstreamFailoverThresholdMS = 2000
		c.UpstreamFailoverConsecutiveFailures = 1
		c.UpstreamFailoverCooldownSeconds = 60
		c.UpstreamFailoverAutoRecover = true
		c.UpstreamFailoverNotification = enableNotification
	})

	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	t.Cleanup(func() { cacheMgr.Close() })
	stats := telemetry.NewStats()
	t.Cleanup(func() { stats.Close() })

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)
	t.Cleanup(func() {
		srv.Stop()
		srv.WaitPrefetchWorkers(2 * time.Second)
	})
	return srv, cfgMgr
}

func TestFailoverNotification_PrimaryToBackup(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)

	var notifyCount atomic.Int32
	var receivedTitle, receivedMsg string
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	srv.SetFailoverNotifyFunc(func(title, message string) {
		mu.Lock()
		receivedTitle = title
		receivedMsg = message
		mu.Unlock()
		notifyCount.Add(1)
		select {
		case done <- struct{}{}:
		default:
		}
	})

	// Trigger a hard connection error from Primary to Backup
	srv.observeUpstream(failoverRoutePrimary, false, 50*time.Millisecond, errors.New("connection refused"))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failover notification callback")
	}

	if notifyCount.Load() != 1 {
		t.Fatalf("expected exactly 1 notification, got %d", notifyCount.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if receivedTitle != FailoverNotifyTitle {
		t.Fatalf("expected title %q, got %q", FailoverNotifyTitle, receivedTitle)
	}
	if receivedMsg != FailoverNotifyMessage {
		t.Fatalf("expected message %q, got %q", FailoverNotifyMessage, receivedMsg)
	}
}

func TestFailoverNotification_BackupToPrimaryNoNotification(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)

	var notifyCount atomic.Int32
	srv.SetFailoverNotifyFunc(func(title, message string) {
		notifyCount.Add(1)
	})

	// 1. First trigger Primary -> Backup (transition 1)
	srv.handleFailoverTransition(&failoverTransition{
		from:   failoverRoutePrimary,
		to:     failoverRouteBackup,
		reason: "connection_error",
	})

	time.Sleep(50 * time.Millisecond)
	if notifyCount.Load() != 1 {
		t.Fatalf("expected 1 notification on failover, got %d", notifyCount.Load())
	}

	// 2. Recovery: Backup -> Primary (must NOT notify)
	srv.handleFailoverTransition(&failoverTransition{
		from:   failoverRouteBackup,
		to:     failoverRoutePrimary,
		reason: "primary_recovered",
	})

	time.Sleep(50 * time.Millisecond)
	if notifyCount.Load() != 1 {
		t.Fatalf("expected still exactly 1 notification after recovery, got %d", notifyCount.Load())
	}
}

func TestFailoverNotification_DisabledSwitchNoNotification(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, false) // Notification disabled

	var notifyCount atomic.Int32
	srv.SetFailoverNotifyFunc(func(title, message string) {
		notifyCount.Add(1)
	})

	srv.handleFailoverTransition(&failoverTransition{
		from:   failoverRoutePrimary,
		to:     failoverRouteBackup,
		reason: "connection_error",
	})

	time.Sleep(50 * time.Millisecond)
	if notifyCount.Load() != 0 {
		t.Fatalf("expected 0 notifications when switch is disabled, got %d", notifyCount.Load())
	}
}

func TestFailoverNotification_Debounce60Seconds(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)

	var notifyCount atomic.Int32
	srv.SetFailoverNotifyFunc(func(title, message string) {
		notifyCount.Add(1)
	})

	// First failover: triggers notification
	srv.notifyFailoverTransition(&failoverTransition{
		from:   failoverRoutePrimary,
		to:     failoverRouteBackup,
		reason: "connection_error",
	})

	time.Sleep(50 * time.Millisecond)
	if notifyCount.Load() != 1 {
		t.Fatalf("expected 1 notification on first failover, got %d", notifyCount.Load())
	}

	// Second failover immediately within 60s: must be debounced
	srv.notifyFailoverTransition(&failoverTransition{
		from:   failoverRoutePrimary,
		to:     failoverRouteBackup,
		reason: "connection_error",
	})

	time.Sleep(50 * time.Millisecond)
	if notifyCount.Load() != 1 {
		t.Fatalf("expected second notification to be debounced, got %d", notifyCount.Load())
	}

	// Advance lastNotifyAt to 61 seconds in the past to simulate elapsed time
	srv.notifyMu.Lock()
	srv.lastNotifyAt = time.Now().Add(-61 * time.Second)
	srv.notifyMu.Unlock()

	// Third failover after 60s: triggers notification
	srv.notifyFailoverTransition(&failoverTransition{
		from:   failoverRoutePrimary,
		to:     failoverRouteBackup,
		reason: "connection_error",
	})

	time.Sleep(50 * time.Millisecond)
	if notifyCount.Load() != 2 {
		t.Fatalf("expected third notification to succeed after 60s debounce, got %d", notifyCount.Load())
	}
}

func TestFailoverNotification_NilCallbackNoPanic(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)
	srv.SetFailoverNotifyFunc(nil)

	// Must not panic with nil callback
	srv.handleFailoverTransition(&failoverTransition{
		from:   failoverRoutePrimary,
		to:     failoverRouteBackup,
		reason: "connection_error",
	})
	time.Sleep(20 * time.Millisecond)
}

func TestFailoverNotification_CoversObserve(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)

	var notifyCount atomic.Int32
	srv.SetFailoverNotifyFunc(func(title, message string) {
		notifyCount.Add(1)
	})

	// Call observeUpstream with hard connection error
	srv.observeUpstream(failoverRoutePrimary, false, 10*time.Millisecond, errors.New("dial tcp connection refused"))
	time.Sleep(50 * time.Millisecond)

	if notifyCount.Load() != 1 {
		t.Fatalf("expected observeUpstream to trigger notification callback, got %d", notifyCount.Load())
	}
}

func TestFailoverNotification_CoversObserveInFlightTimeout(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)

	var notifyCount atomic.Int32
	srv.SetFailoverNotifyFunc(func(title, message string) {
		notifyCount.Add(1)
	})

	// Consecutive failures is 1 in newTestProxyForNotification, so observeInFlightTimeout triggers transition
	srv.observeInFlightTimeout(failoverRoutePrimary, false)
	time.Sleep(50 * time.Millisecond)

	if notifyCount.Load() != 1 {
		t.Fatalf("expected observeInFlightTimeout to trigger notification callback, got %d", notifyCount.Load())
	}
}

func TestFailoverNotification_ConcurrentDebounce(t *testing.T) {
	srv, _ := newTestProxyForNotification(t, true)

	var notifyCount atomic.Int32
	srv.SetFailoverNotifyFunc(func(title, message string) {
		notifyCount.Add(1)
	})

	const numGoroutines = 50
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			srv.notifyFailoverTransition(&failoverTransition{
				from:   failoverRoutePrimary,
				to:     failoverRouteBackup,
				reason: "connection_error",
			})
		}()
	}

	close(start)
	wg.Wait()

	time.Sleep(50 * time.Millisecond)
	if count := notifyCount.Load(); count != 1 {
		t.Fatalf("expected exactly 1 notification under concurrent barrage, got %d", count)
	}
}

