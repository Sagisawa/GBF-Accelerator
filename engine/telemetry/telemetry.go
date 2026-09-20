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

const maxLogEntries = 1000

type Stats struct {
	StartTime              time.Time
	TotalAPIs              atomic.Int64
	TotalAssets            atomic.Int64
	TotalHits              atomic.Int64
	RAMHits                atomic.Int64
	DiskHits               atomic.Int64
	CacheMisses            atomic.Int64
	PrefetchRequests       atomic.Int64
	PrefetchSuccesses      atomic.Int64
	PrefetchReused         atomic.Int64
	APIRetries             atomic.Int64
	ActiveAPICount         atomic.Int32
	ActiveForegroundAssets atomic.Int32
	LastError              string

	// Real connection-pool behaviour, fed from httptrace.GotConn hooks.
	ReusedConns atomic.Int64
	NewConns    atomic.Int64
	ProtoH1     atomic.Int64
	ProtoH2     atomic.Int64

	mu            sync.RWMutex
	logBuf        []LogEntry
	logIdx        int
	logCount      int
	subMu         sync.RWMutex
	subscribers   []chan LogEntry
	prefetchMu    sync.RWMutex
	prefetchSaved map[string]struct{}
	logChan       chan LogEntry

	idleMu     sync.Mutex
	idleChan   chan struct{}
	closedChan chan struct{}
	closed     bool

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
		logBuf:        make([]LogEntry, maxLogEntries),
		subscribers:   make([]chan LogEntry, 0),
		prefetchSaved: make(map[string]struct{}),
		logChan:       make(chan LogEntry, 1024),
		idleChan:      make(chan struct{}),
		closedChan:    make(chan struct{}),
	}
	go s.logWorker()
	return s
}

func (s *Stats) IncAPI() {
	s.TotalAPIs.Add(1)
}

func (s *Stats) IncAsset() {
	s.TotalAssets.Add(1)
}

func (s *Stats) IncRAMHit() {
	s.TotalHits.Add(1)
	s.RAMHits.Add(1)
}

func (s *Stats) IncDiskHit() {
	s.TotalHits.Add(1)
	s.DiskHits.Add(1)
}

func (s *Stats) IncMiss() {
	s.CacheMisses.Add(1)
}

func (s *Stats) IncAPIRetry() {
	s.APIRetries.Add(1)
}

func (s *Stats) AddActiveAPI(delta int32) {
	s.ActiveAPICount.Add(delta)
	if delta < 0 && s.IsForegroundIdle() {
		s.NotifyForegroundIdle()
	}
}

func (s *Stats) AddActiveFG(delta int32) {
	s.ActiveForegroundAssets.Add(delta)
	if delta < 0 && s.IsForegroundIdle() {
		s.NotifyForegroundIdle()
	}
}

func (s *Stats) IsForegroundIdle() bool {
	return s.ActiveAPICount.Load() <= 0 && s.ActiveForegroundAssets.Load() <= 0
}

func (s *Stats) NotifyForegroundIdle() {
	if !s.IsForegroundIdle() {
		return
	}
	s.idleMu.Lock()
	defer s.idleMu.Unlock()
	if s.closed {
		return
	}
	if !s.IsForegroundIdle() {
		return
	}
	if s.idleChan != nil {
		select {
		case <-s.idleChan:
		default:
			close(s.idleChan)
		}
		s.idleChan = make(chan struct{})
	}
}

func (s *Stats) WaitForegroundIdle(stopChan <-chan struct{}) bool {
	for !s.IsForegroundIdle() {
		s.idleMu.Lock()
		if s.closed {
			s.idleMu.Unlock()
			return false
		}
		if s.closedChan == nil {
			s.closedChan = make(chan struct{})
		}
		closedCh := s.closedChan
		if s.idleChan == nil {
			s.idleChan = make(chan struct{})
		}
		idleCh := s.idleChan
		s.idleMu.Unlock()

		if s.IsForegroundIdle() {
			return true
		}

		select {
		case <-stopChan:
			return false
		case <-closedCh:
			return false
		case <-idleCh:
		}
	}
	return true
}

func (s *Stats) IncPrefetchRequest() {
	s.PrefetchRequests.Add(1)
}

func (s *Stats) IncPrefetchSuccess() {
	s.PrefetchSuccesses.Add(1)
}

func (s *Stats) IncPrefetchReused() {
	s.PrefetchReused.Add(1)
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
		s.PrefetchReused.Add(1)
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
		if s.logBuf == nil {
			s.logBuf = make([]LogEntry, maxLogEntries)
		}
		s.logBuf[s.logIdx] = entry
		s.logIdx = (s.logIdx + 1) % maxLogEntries
		if s.logCount < maxLogEntries {
			s.logCount++
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
		s.idleMu.Lock()
		s.closed = true
		if s.closedChan != nil {
			select {
			case <-s.closedChan:
			default:
				close(s.closedChan)
			}
		}
		if s.idleChan != nil {
			select {
			case <-s.idleChan:
			default:
				close(s.idleChan)
			}
		}
		s.idleMu.Unlock()
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
	n := s.logCount
	if n == 0 {
		return []LogEntry{}
	}
	res := make([]LogEntry, n)
	if n < maxLogEntries {
		copy(res, s.logBuf[:n])
	} else {
		tail := copy(res, s.logBuf[s.logIdx:])
		copy(res[tail:], s.logBuf[:s.logIdx])
	}
	return res
}

func (s *Stats) RequestsMap() map[string]interface{} {
	return map[string]interface{}{
		"total_apis":        s.TotalAPIs.Load(),
		"total_assets":      s.TotalAssets.Load(),
		"total_hits":        s.TotalHits.Load(),
		"ram_hits":          s.RAMHits.Load(),
		"disk_hits":         s.DiskHits.Load(),
		"cache_misses":      s.CacheMisses.Load(),
		"prefetch_requests": s.PrefetchRequests.Load(),
		"prefetch_reused":   s.PrefetchReused.Load(),
		"api_retries":       s.APIRetries.Load(),
	}
}

// RecordConnReuse records whether an upstream connection acquisition (observed via
// an httptrace.GotConn hook) came from the keep-alive pool or was newly dialed.
func (s *Stats) RecordConnReuse(reused bool) {
	if reused {
		s.ReusedConns.Add(1)
	} else {
		s.NewConns.Add(1)
	}
}

// RecordProtocol records the negotiated application protocol of an upstream response
// (resp.Proto), e.g. "HTTP/1.1" or "HTTP/2.0".
func (s *Stats) RecordProtocol(proto string) {
	if strings.HasPrefix(proto, "HTTP/2") {
		s.ProtoH2.Add(1)
	} else {
		s.ProtoH1.Add(1)
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
