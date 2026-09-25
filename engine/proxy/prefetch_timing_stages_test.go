package proxy

import (
	"bytes"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

type timingStageResult struct {
	StageName            string
	DelayBeforeFg        time.Duration
	UpstreamRequests     int64
	FgElapsed            time.Duration
	PrefetchRemaining    time.Duration
	PrefetchTotalElapsed time.Duration
	DuplicateFetch       bool
	DeadlockOrStall      bool
	DataMatches          bool
}

func runSingleTimingStage(t *testing.T, stageName string, delayBeforeFg time.Duration) timingStageResult {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const downloadDuration = 100 * time.Millisecond
	payload := []byte("\x89PNG\r\n\x1a\n--SIMULATED-PAYLOAD-FOR-GBF-TIMING-TEST--")

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		time.Sleep(downloadDuration)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"stage-test-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)
	pe := newPrefetchEngine(srv)
	defer pe.Stop()

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	cleanPath := fmt.Sprintf("/assets/img/sp/timing_%s.png", stageName)

	var wg sync.WaitGroup
	wg.Add(2)

	prefetchStarted := make(chan time.Time, 1)
	var prefetchTotalElapsed time.Duration

	// 1. Launch prefetch
	go func() {
		defer wg.Done()
		t0 := time.Now()
		prefetchStarted <- t0
		pe.processFetchItem(prefetchItem{
			prio: 2,
			host: benchHost,
			path: cleanPath,
		})
		prefetchTotalElapsed = time.Since(t0)
	}()

	<-prefetchStarted

	// 2. Launch foreground request after specified delay
	var fgElapsed time.Duration
	var prefetchRemaining time.Duration
	var respBuf bytes.Buffer

	go func() {
		defer wg.Done()
		time.Sleep(delayBeforeFg)

		fgStart := time.Now()
		req, _ := http.NewRequest(http.MethodGet, "https://"+benchHost+cleanPath, nil)
		req.RequestURI = cleanPath
		req.Host = benchHost

		srv.handleStaticAssetLower(&respBuf, req, benchHost, cleanPath)
		fgElapsed = time.Since(fgStart)

		// Record the theoretical and actual remaining prefetch time at foreground entry
		if delayBeforeFg < downloadDuration {
			prefetchRemaining = downloadDuration - delayBeforeFg
		} else {
			prefetchRemaining = 0
		}
	}()

	// Wait with a watchdog timeout to detect deadlocks or stalls
	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	var deadlock bool
	select {
	case <-doneCh:
		deadlock = false
	case <-time.After(2 * time.Second):
		deadlock = true
	}

	hits := upstreamHits.Load()
	duplicate := hits > 1
	dataMatch := bytes.Contains(respBuf.Bytes(), payload)

	return timingStageResult{
		StageName:            stageName,
		DelayBeforeFg:        delayBeforeFg,
		UpstreamRequests:     hits,
		FgElapsed:            fgElapsed,
		PrefetchRemaining:    prefetchRemaining,
		PrefetchTotalElapsed: prefetchTotalElapsed,
		DuplicateFetch:       duplicate,
		DeadlockOrStall:      deadlock,
		DataMatches:          dataMatch,
	}
}

// TestTimingStages_PrefetchAndForegroundCoalescing tests the exact 3 stages required:
// 1. Prefetch just started (10ms of 100ms)
// 2. Prefetch halfway (50ms of 100ms)
// 3. Prefetch nearly finished (90ms of 100ms)
func TestTimingStages_PrefetchAndForegroundCoalescing(t *testing.T) {
	stages := []struct {
		name  string
		delay time.Duration
	}{
		{name: "JustStarted_10pct", delay: 10 * time.Millisecond},
		{name: "Halfway_50pct", delay: 50 * time.Millisecond},
		{name: "NearlyFinished_90pct", delay: 90 * time.Millisecond},
	}

	results := make([]timingStageResult, len(stages))
	for i, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			res := runSingleTimingStage(t, stage.name, stage.delay)
			results[i] = res

			t.Logf("[%s] Upstream Hits: %d, Fg Elapsed: %v, Prefetch Total: %v, Duplicate: %v, Deadlock: %v, DataMatch: %v",
				res.StageName, res.UpstreamRequests, res.FgElapsed, res.PrefetchTotalElapsed, res.DuplicateFetch, res.DeadlockOrStall, res.DataMatches)

			if res.DeadlockOrStall {
				t.Fatalf("[%s] DEADLOCK or stall detected!", res.StageName)
			}
			if res.UpstreamRequests != 1 {
				t.Fatalf("[%s] Expected 1 upstream request, got %d (duplicate fetch occurred!)", res.StageName, res.UpstreamRequests)
			}
			if !res.DataMatches {
				t.Fatalf("[%s] Foreground response data does not match payload", res.StageName)
			}
			// In all three stages, foreground should NOT wait longer than full download (100ms) + tolerance
			if res.FgElapsed > 150*time.Millisecond {
				t.Fatalf("[%s] Foreground waited abnormally long: %v", res.StageName, res.FgElapsed)
			}
		})
	}

	// Print summary table
	t.Logf("\n=================== 3 TIMING STAGES TIMING REPORT ===================")
	t.Logf("%-22s | %-12s | %-12s | %-10s | %-10s", "Stage", "Upstream Req", "Fg Elapsed", "Prefetch Rem", "Duplicate?")
	t.Logf("--------------------------------------------------------------------------------")
	for _, r := range results {
		t.Logf("%-22s | %-12d | %-12v | %-10v | %-10v",
			r.StageName, r.UpstreamRequests, r.FgElapsed, r.PrefetchRemaining, r.DuplicateFetch)
	}
	t.Logf("=====================================================================")
}
