package telemetry

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// latencySampleCap bounds the in-memory ring buffer of upstream request latencies.
// Only the most recent samples are kept for percentile computation; this keeps the
// proxy datapath memory footprint constant regardless of total request volume.
const latencySampleCap = 512

// LatencyPercentiles summarizes the recently observed upstream request latencies.
// All values are in milliseconds. Samples is the number of data points used.
type LatencyPercentiles struct {
	P50     float64
	P95     float64
	P99     float64
	Avg     float64
	Min     float64
	Max     float64
	Samples int
}

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

	// Real connection-pool behaviour, fed from httptrace.GotConn hooks.
	ReusedConns int64
	NewConns    int64
	ProtoH1     int64
	ProtoH2     int64

	mu            sync.RWMutex
	logs          []LogEntry
	subMu         sync.RWMutex
	subscribers   []chan LogEntry
	prefetchMu    sync.RWMutex
	prefetchSaved map[string]struct{}
	logChan       chan LogEntry

	// Fixed-capacity ring buffer of upstream request latencies (milliseconds).
	latMu    sync.Mutex
	latBuf   []float64
	latIdx   int
	latCount int

	closeOnce sync.Once
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
	// Guard against send-on-closed-channel during shutdown: recover and drop.
	defer func() { _ = recover() }()
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

// Close shuts down the background log worker, allowing its goroutine to exit.
// It is idempotent and safe to call during process shutdown. The global
// telemetry.GlobalStats instance is process-lifetime and is not closed.
func (s *Stats) Close() {
	s.closeOnce.Do(func() {
		close(s.logChan)
	})
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

// RecordConnReuse records whether an upstream connection acquisition (observed via
// an httptrace.GotConn hook) came from the keep-alive pool or was newly dialed.
func (s *Stats) RecordConnReuse(reused bool) {
	if reused {
		atomic.AddInt64(&s.ReusedConns, 1)
	} else {
		atomic.AddInt64(&s.NewConns, 1)
	}
}

// RecordProtocol records the negotiated application protocol of an upstream response
// (resp.Proto), e.g. "HTTP/1.1" or "HTTP/2.0".
func (s *Stats) RecordProtocol(proto string) {
	if strings.HasPrefix(proto, "HTTP/2") {
		atomic.AddInt64(&s.ProtoH2, 1)
	} else {
		atomic.AddInt64(&s.ProtoH1, 1)
	}
}

// RecordLatency appends one upstream request latency (milliseconds) to the ring buffer.
func (s *Stats) RecordLatency(ms float64) {
	if ms < 0 {
		ms = 0
	}
	s.latMu.Lock()
	if s.latBuf == nil {
		s.latBuf = make([]float64, latencySampleCap)
	}
	s.latBuf[s.latIdx] = ms
	s.latIdx = (s.latIdx + 1) % latencySampleCap
	if s.latCount < latencySampleCap {
		s.latCount++
	}
	s.latMu.Unlock()
}

// LatencySnapshot returns percentile statistics over the most recent samples.
func (s *Stats) LatencySnapshot() LatencyPercentiles {
	s.latMu.Lock()
	n := s.latCount
	if n == 0 {
		s.latMu.Unlock()
		return LatencyPercentiles{}
	}
	data := make([]float64, n)
	copy(data, s.latBuf[:n])
	s.latMu.Unlock()

	sort.Float64s(data)
	var sum float64
	for _, v := range data {
		sum += v
	}
	pick := func(q float64) float64 {
		if n == 1 {
			return data[0]
		}
		idx := int(q*float64(n-1) + 0.5)
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return data[idx]
	}
	return LatencyPercentiles{
		P50:     pick(0.50),
		P95:     pick(0.95),
		P99:     pick(0.99),
		Avg:     sum / float64(n),
		Min:     data[0],
		Max:     data[n-1],
		Samples: n,
	}
}
