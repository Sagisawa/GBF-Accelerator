package telemetry

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestTelemetryAsyncLog(t *testing.T) {
	s := NewStats()

	subCh, unsub := s.SubscribeLogs()
	defer unsub()

	s.Log("INFO", "test message 1")
	s.Log("WARN", "test message 2")

	// Wait for async log worker to deliver to subscriber
	select {
	case entry := <-subCh:
		if entry.Msg != "test message 1" {
			t.Errorf("expected test message 1, got %s", entry.Msg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for log entry on subscriber")
	}

	// Verify buffer has the logs after background processing
	time.Sleep(50 * time.Millisecond)
	logs := s.GetLogs()
	if len(logs) < 2 {
		t.Errorf("expected at least 2 logs in buffer, got %d", len(logs))
	}
}

func TestRecordConnReuseAndProtocol(t *testing.T) {
	s := NewStats()

	s.RecordConnReuse(true)
	s.RecordConnReuse(true)
	s.RecordConnReuse(false)
	s.RecordProtocol("HTTP/2.0")
	s.RecordProtocol("HTTP/1.1")
	s.RecordProtocol("HTTP/1.1")

	if got := s.ReusedConns.Load(); got != 2 {
		t.Errorf("expected 2 reused conns, got %d", got)
	}
	if got := s.NewConns.Load(); got != 1 {
		t.Errorf("expected 1 new conn, got %d", got)
	}
	if got := s.ProtoH2.Load(); got != 1 {
		t.Errorf("expected 1 HTTP/2, got %d", got)
	}
	if got := s.ProtoH1.Load(); got != 2 {
		t.Errorf("expected 2 HTTP/1.1, got %d", got)
	}
}

func TestLatencySnapshotEmpty(t *testing.T) {
	s := NewStats()
	lat := s.LatencySnapshot()
	if lat.Samples != 0 {
		t.Errorf("expected 0 samples on empty buffer, got %d", lat.Samples)
	}
}

func TestLatencySnapshotPercentiles(t *testing.T) {
	s := NewStats()
	// 100 samples: 1ms .. 100ms
	for i := 1; i <= 100; i++ {
		s.RecordLatency(float64(i))
	}
	lat := s.LatencySnapshot()
	if lat.Samples != 100 {
		t.Fatalf("expected 100 samples, got %d", lat.Samples)
	}
	if lat.Min != 1 || lat.Max != 100 {
		t.Errorf("expected min=1 max=100, got min=%v max=%v", lat.Min, lat.Max)
	}
	if lat.Avg < 50.4 || lat.Avg > 50.6 {
		t.Errorf("expected avg ~50.5, got %v", lat.Avg)
	}
	// p95 of 1..100 should be near 95
	if lat.P95 < 94 || lat.P95 > 96 {
		t.Errorf("expected p95 ~95, got %v", lat.P95)
	}
	if lat.P50 < 49 || lat.P50 > 51 {
		t.Errorf("expected p50 ~50, got %v", lat.P50)
	}
}

func TestLatencyRingBufferCap(t *testing.T) {
	s := NewStats()
	// Overflow the ring buffer beyond its cap; samples must stay bounded.
	for i := 0; i < latencySampleCap*3; i++ {
		s.RecordLatency(float64(i))
	}
	lat := s.LatencySnapshot()
	if lat.Samples != latencySampleCap {
		t.Errorf("expected samples capped at %d, got %d", latencySampleCap, lat.Samples)
	}
}

func TestLatencyConcurrentSafety(t *testing.T) {
	s := NewStats()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base float64) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.RecordLatency(base + float64(i))
				s.RecordConnReuse(i%2 == 0)
				s.RecordProtocol("HTTP/2.0")
			}
		}(float64(g * 1000))
	}
	wg.Wait()
	lat := s.LatencySnapshot()
	if lat.Samples == 0 || lat.Samples > latencySampleCap {
		t.Errorf("concurrent recording produced invalid sample count %d", lat.Samples)
	}
	if got := s.ProtoH2.Load(); got != 8*200 {
		t.Errorf("expected %d HTTP/2 records, got %d", 8*200, got)
	}
}

func TestCloseIsIdempotentAndSafe(t *testing.T) {
	s := NewStats()
	s.Log("INFO", "before close")
	s.Close()
	s.Close() // must not panic (idempotent)
	// Logging after close must be dropped safely, not panic.
	s.Log("INFO", "after close")
}

func TestLogRingBuffer_ChronologicalOrderAndCap(t *testing.T) {
	s := NewStats()
	defer s.Close()

	// Use subscriber channel to synchronously verify worker consumption per message,
	// preventing queue drops from tight loop bursting and guaranteeing deterministic ingestion.
	subCh, unsub := s.SubscribeLogs()
	defer unsub()

	const total = 1500
	for i := 0; i < total; i++ {
		s.Log("INFO", fmt.Sprintf("msg-%04d", i))
		select {
		case <-subCh:
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for log %d to be processed by worker", i)
		}
	}

	logs := s.GetLogs()
	if len(logs) != maxLogEntries {
		t.Fatalf("expected exactly %d logs, got %d", maxLogEntries, len(logs))
	}

	if logs[len(logs)-1].Msg != "msg-1499" {
		t.Errorf("expected newest log to be msg-1499, got %s", logs[len(logs)-1].Msg)
	}
	if logs[0].Msg != "msg-0500" {
		t.Errorf("expected oldest log to be msg-0500, got %s", logs[0].Msg)
	}

	// Verify sequential monotonicity throughout the buffer
	for i := 1; i < len(logs); i++ {
		if logs[i-1].Msg >= logs[i].Msg {
			t.Errorf("expected monotonic message sequence, got %s >= %s at index %d", logs[i-1].Msg, logs[i].Msg, i)
		}
	}
}

func TestLogDrop_NonBlockingUnderCongestion(t *testing.T) {
	// Construct an isolated Stats instance with full channel and no consumer worker,
	// verifying that Log() drops cleanly without blocking the caller datapath.
	s := &Stats{
		logChan: make(chan LogEntry, 1024),
	}
	// Pre-fill the buffer completely
	for i := 0; i < 1024; i++ {
		s.logChan <- LogEntry{Level: "INFO", Msg: "fill"}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Log("INFO", "overflow-entry")
	}()

	select {
	case <-done:
		// Succeeded immediately via non-blocking default branch
	case <-time.After(200 * time.Millisecond):
		t.Fatal("s.Log() blocked when channel was full; violates datapath non-blocking contract")
	}
}

func TestWaitForegroundIdle_EventNotification(t *testing.T) {
	s := NewStats()
	defer s.Close()

	if !s.IsForegroundIdle() {
		t.Fatal("expected initially idle")
	}

	s.AddActiveAPI(1)
	if s.IsForegroundIdle() {
		t.Fatal("expected not idle when ActiveAPICount=1")
	}

	wokeUp := make(chan bool, 1)
	go func() {
		ok := s.WaitForegroundIdle(nil)
		wokeUp <- ok
	}()

	select {
	case <-wokeUp:
		t.Fatal("WaitForegroundIdle returned prematurely while active API > 0")
	case <-time.After(50 * time.Millisecond):
	}

	s.AddActiveAPI(-1)

	select {
	case ok := <-wokeUp:
		if !ok {
			t.Errorf("expected true from WaitForegroundIdle, got false")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for WaitForegroundIdle to unblock on idle notification")
	}
}

func TestWaitForegroundIdle_CloseUnblocks(t *testing.T) {
	s := NewStats()
	s.AddActiveAPI(1) // not idle

	done := make(chan bool, 1)
	go func() {
		ok := s.WaitForegroundIdle(nil)
		done <- ok
	}()

	time.Sleep(20 * time.Millisecond)
	s.Close()

	select {
	case ok := <-done:
		if ok {
			t.Errorf("expected false when WaitForegroundIdle is terminated by Close()")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WaitForegroundIdle did not unblock upon s.Close() (likely busy-loop or deadlock)")
	}
}

func TestWaitForegroundIdle_ConcurrentInterleaved(t *testing.T) {
	s := NewStats()
	defer s.Close()

	s.AddActiveAPI(1)
	s.AddActiveFG(1)

	wokeUp := make(chan bool, 1)
	go func() {
		ok := s.WaitForegroundIdle(nil)
		wokeUp <- ok
	}()

	// Decrement API first; still active FG, so worker must stay blocked
	s.AddActiveAPI(-1)
	select {
	case <-wokeUp:
		t.Fatal("worker woke up while FG asset was still active")
	case <-time.After(30 * time.Millisecond):
	}

	// Decrement FG; now both are 0, worker must wake up
	s.AddActiveFG(-1)
	select {
	case ok := <-wokeUp:
		if !ok {
			t.Errorf("expected true, got false")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for worker to wake up after both API and FG completed")
	}
}

