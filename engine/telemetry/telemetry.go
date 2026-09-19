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

	mu            sync.RWMutex
	logs          []LogEntry
	subMu         sync.RWMutex
	subscribers   []chan LogEntry
	prefetchMu    sync.RWMutex
	prefetchSaved map[string]struct{}
	logChan       chan LogEntry
}

var GlobalStats = NewStats()

func NewStats() *Stats {
	s := &Stats{
		StartTime:     time.Now(),
		logs:          make([]LogEntry, 0, 1000),
		subscribers:   make([]chan LogEntry, 0),
		prefetchSaved: make(map[string]struct{}),
		logChan:       make(chan LogEntry, 1024),
	}
	go s.logWorker()
	return s
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

func (s *Stats) IncPrefetchRequest() {
	atomic.AddInt64(&s.PrefetchRequests, 1)
}

func (s *Stats) IncPrefetchSuccess() {
	atomic.AddInt64(&s.PrefetchSuccesses, 1)
}

func (s *Stats) IncPrefetchReused() {
	atomic.AddInt64(&s.PrefetchReused, 1)
}

func (s *Stats) MarkPrefetchSaved(path string) {
	s.prefetchMu.Lock()
	defer s.prefetchMu.Unlock()
	s.prefetchSaved[path] = struct{}{}
	if len(s.prefetchSaved) > 2000 {
		for k := range s.prefetchSaved {
			delete(s.prefetchSaved, k)
			if len(s.prefetchSaved) <= 1500 {
				break
			}
		}
	}
}

func (s *Stats) CheckAndRecordPrefetchReused(path string) {
	s.prefetchMu.Lock()
	_, ok := s.prefetchSaved[path]
	if ok {
		delete(s.prefetchSaved, path)
	}
	s.prefetchMu.Unlock()
	if ok {
		atomic.AddInt64(&s.PrefetchReused, 1)
	}
}

func (s *Stats) Log(level, msg string) {
	entry := LogEntry{
		Time:  time.Now().Format("15:04:05"),
		Level: level,
		Msg:   msg,
	}
	select {
	case s.logChan <- entry:
	default:
		// Drop log entry when queue is congested to protect proxy datapath latency
	}
}

func (s *Stats) logWorker() {
	for entry := range s.logChan {
		s.mu.Lock()
		s.logs = append(s.logs, entry)
		if len(s.logs) > 1000 {
			s.logs = s.logs[len(s.logs)-1000:]
		}
		s.mu.Unlock()

		// Fan-out to SSE subscribers
		s.subMu.RLock()
		for _, ch := range s.subscribers {
			select {
			case ch <- entry:
			default:
			}
		}
		s.subMu.RUnlock()
	}
}

func (s *Stats) SubscribeLogs() (chan LogEntry, func()) {
	ch := make(chan LogEntry, 100)
	s.subMu.Lock()
	s.subscribers = append(s.subscribers, ch)
	s.subMu.Unlock()

	unsubscribe := func() {
		s.subMu.Lock()
		defer s.subMu.Unlock()
		for i, sub := range s.subscribers {
			if sub == ch {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}
	return ch, unsubscribe
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
