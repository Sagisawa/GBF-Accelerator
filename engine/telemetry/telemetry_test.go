package telemetry

import (
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

	if got := s.ReusedConns; got != 2 {
		t.Errorf("expected 2 reused conns, got %d", got)
	}
	if got := s.NewConns; got != 1 {
		t.Errorf("expected 1 new conn, got %d", got)
	}
	if got := s.ProtoH2; got != 1 {
		t.Errorf("expected 1 HTTP/2, got %d", got)
	}
	if got := s.ProtoH1; got != 2 {
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
	if got := s.ProtoH2; got != 8*200 {
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
