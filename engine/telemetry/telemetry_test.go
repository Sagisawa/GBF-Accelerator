package telemetry

import (
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
