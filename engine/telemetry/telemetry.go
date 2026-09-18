package telemetry

import (
	"sync"
	"sync/atomic"
	"time"
)

type LogEntry struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

type Stats struct {
	StartTime              time.Time
	TotalAPIs              int64
	TotalAssets            int64
	TotalHits              int64
	RAMHits                int64
	DiskHits               int64
	CacheMisses            int64
	PrefetchRequests       int64
	PrefetchSuccesses      int64
	PrefetchReused         int64
	APIRetries             int64
	ActiveAPICount         int32
	ActiveForegroundAssets int32
	LastError              string

	mu   sync.RWMutex
	logs []LogEntry
}

var GlobalStats = NewStats()

func NewStats() *Stats {
	return &Stats{
		StartTime: time.Now(),
		logs:      make([]LogEntry, 0, 1000),
	}
}

func (s *Stats) IncAPI() {
	atomic.AddInt64(&s.TotalAPIs, 1)
}

func (s *Stats) IncAsset() {
	atomic.AddInt64(&s.TotalAssets, 1)
}

func (s *Stats) IncRAMHit() {
	atomic.AddInt64(&s.TotalHits, 1)
	atomic.AddInt64(&s.RAMHits, 1)
}

func (s *Stats) IncDiskHit() {
	atomic.AddInt64(&s.TotalHits, 1)
	atomic.AddInt64(&s.DiskHits, 1)
}

func (s *Stats) IncMiss() {
	atomic.AddInt64(&s.CacheMisses, 1)
}

func (s *Stats) IncAPIRetry() {
	atomic.AddInt64(&s.APIRetries, 1)
}

func (s *Stats) AddActiveAPI(delta int32) {
	atomic.AddInt32(&s.ActiveAPICount, delta)
}

func (s *Stats) AddActiveFG(delta int32) {
	atomic.AddInt32(&s.ActiveForegroundAssets, delta)
}

func (s *Stats) Log(level, msg string) {
	entry := LogEntry{
		Time:  time.Now().Format("15:04:05"),
		Level: level,
		Msg:   msg,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > 1000 {
		s.logs = s.logs[len(s.logs)-1000:]
	}
}

func (s *Stats) GetLogs() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]LogEntry, len(s.logs))
	copy(res, s.logs)
	return res
}

func (s *Stats) RequestsMap() map[string]interface{} {
	return map[string]interface{}{
		"total_apis":        atomic.LoadInt64(&s.TotalAPIs),
		"total_assets":      atomic.LoadInt64(&s.TotalAssets),
		"total_hits":        atomic.LoadInt64(&s.TotalHits),
		"ram_hits":          atomic.LoadInt64(&s.RAMHits),
		"disk_hits":         atomic.LoadInt64(&s.DiskHits),
		"cache_misses":      atomic.LoadInt64(&s.CacheMisses),
		"prefetch_requests": atomic.LoadInt64(&s.PrefetchRequests),
		"prefetch_reused":   atomic.LoadInt64(&s.PrefetchReused),
		"api_retries":       atomic.LoadInt64(&s.APIRetries),
	}
}
