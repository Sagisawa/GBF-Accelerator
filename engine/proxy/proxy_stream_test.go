package proxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// TestStreamThrough_SmallAsset_BatchMode verifies that assets <= 16KB
// continue to use the fast batch path without streaming overhead.
func TestStreamThrough_SmallAsset_BatchMode(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 16*1024) // 16 KB == smallAssetThreshold (boundary)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"small-asset-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/small_item.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected 1 upstream hit, got %d", upstreamHits.Load())
	}

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch")
	}

	// Verify second request hits RAM cache
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM cache hit on subsequent access, got item=%v src=%s", item, src)
	}
}

type timeTrackingWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	firstWrite time.Time
	lastWrite  time.Time
}

func (w *timeTrackingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := time.Now()
	if w.firstWrite.IsZero() {
		w.firstWrite = now
	}
	w.lastWrite = now
	return w.buf.Write(p)
}

// TestStreamThrough_MediumAsset_StreamThrough verifies that assets > 16KB
// are streamed immediately to the client (low TTFB) and then admitted to RAM/Disk cache.
func TestStreamThrough_MediumAsset_StreamThrough(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const totalSize = 256 * 1024 // 256 KB
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"medium-asset-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Stream out in 64KB chunks with delay to measure TTFB vs full download
		chunkSize := 64 * 1024
		for i := 0; i < len(payload); i += chunkSize {
			end := i + chunkSize
			if end > len(payload) {
				end = len(payload)
			}
			time.Sleep(20 * time.Millisecond)
			_, _ = w.Write(payload[i:end])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/medium_item.png"

	tw := &timeTrackingWriter{}
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	start := time.Now()
	keepAlive := srv.handleStaticAssetLower(tw, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	ttfb := tw.firstWrite.Sub(start)
	totalTime := tw.lastWrite.Sub(start)

	// TTFB should be fast (before all 4 x 20ms = 80ms chunks finish)
	if ttfb >= totalTime {
		t.Errorf("expected TTFB (%v) to be less than total time (%v)", ttfb, totalTime)
	}

	resp, err := http.ReadResponse(bufio.NewReader(&tw.buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("streamed body mismatch")
	}

	// Verify admitted to RAM cache
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM hit on subsequent access, got item=%v src=%s", item, src)
	}
}

// TestStreamThrough_Threshold_LessThan16KB_Batch verifies that an asset smaller than
// the 16KB threshold (e.g. 8KB) uses the batch path, meaning the client receives nothing
// until the upstream response body is fully downloaded.
func TestStreamThrough_Threshold_LessThan16KB_Batch(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const size = 8 * 1024 // 8 KB < 16 KB threshold
	payload := make([]byte, size)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"batch-8k-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Send in 2 chunks with a 40ms delay in between
		half := len(payload) / 2
		_, _ = w.Write(payload[:half])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write(payload[half:])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/small_8k.png"

	tw := &timeTrackingWriter{}
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	start := time.Now()
	keepAlive := srv.handleStaticAssetLower(tw, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	ttfb := tw.firstWrite.Sub(start)
	writeDuration := tw.lastWrite.Sub(tw.firstWrite)

	// In batch mode, first write occurs AFTER the full download (>= 30ms),
	// and writeDuration is near instantaneous (< 20ms).
	if ttfb < 30*time.Millisecond {
		t.Errorf("expected TTFB >= 30ms for batch mode (<16KB), got %v", ttfb)
	}
	if writeDuration > 20*time.Millisecond {
		t.Errorf("expected batch write duration to be near instantaneous, got %v", writeDuration)
	}

	resp, err := http.ReadResponse(bufio.NewReader(&tw.buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch")
	}

	// Verify admitted to RAM cache
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM hit on subsequent access, got item=%v src=%s", item, src)
	}
}

// TestStreamThrough_Threshold_16KBBoundary_Batch verifies that an asset exactly at
// the 16KB threshold (16*1024 bytes) continues to use the batch path.
func TestStreamThrough_Threshold_16KBBoundary_Batch(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const size = 16 * 1024 // 16 KB == smallAssetThreshold
	payload := make([]byte, size)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"batch-16k-boundary-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		half := len(payload) / 2
		_, _ = w.Write(payload[:half])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write(payload[half:])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/boundary_16k.png"

	tw := &timeTrackingWriter{}
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	start := time.Now()
	keepAlive := srv.handleStaticAssetLower(tw, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	ttfb := tw.firstWrite.Sub(start)
	writeDuration := tw.lastWrite.Sub(tw.firstWrite)

	// Boundary 16KB is <= smallAssetThreshold, so batch path is used.
	if ttfb < 30*time.Millisecond {
		t.Errorf("expected TTFB >= 30ms for batch mode (16KB boundary), got %v", ttfb)
	}
	if writeDuration > 20*time.Millisecond {
		t.Errorf("expected batch write duration to be near instantaneous, got %v", writeDuration)
	}

	resp, err := http.ReadResponse(bufio.NewReader(&tw.buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch")
	}

	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM hit on subsequent access, got item=%v src=%s", item, src)
	}
}

// TestStreamThrough_Threshold_GreaterThan16KB_StreamThrough verifies that an asset > 16KB
// (including 16KB+1 boundary and 24KB) enters the stream-through path, delivering low TTFB
// before the upstream download finishes.
func TestStreamThrough_Threshold_GreaterThan16KB_StreamThrough(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}

	const host = "prd-game-a-granbluefantasy.akamaized.net"

	testCases := []struct {
		name string
		path string
		size int
	}{
		{
			name: "BoundaryPlusOne_16385B",
			path: "/assets/img/sp/stream_16k_plus_1.png",
			size: 16*1024 + 1,
		},
		{
			name: "MediumAsset_24KB",
			path: "/assets/img/sp/stream_24k.png",
			size: 24 * 1024,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := make([]byte, tc.size)
			copy(payload, []byte("\x89PNG\r\n\x1a\n"))

			ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("ETag", fmt.Sprintf(`"%s-etag"`, tc.name))
				w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
				w.WriteHeader(http.StatusOK)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}

				// First chunk: 8KB (exceeds initialChunk 4KB, triggers early stream)
				_, _ = w.Write(payload[:8*1024])
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(40 * time.Millisecond)
				_, _ = w.Write(payload[8*1024:])
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			})
			defer ts.Close()
			setTestAssetClient(srv, client)

			tw := &timeTrackingWriter{}
			req, _ := http.NewRequest(http.MethodGet, "https://"+host+tc.path, nil)
			req.RequestURI = tc.path
			req.Host = host

			start := time.Now()
			keepAlive := srv.handleStaticAssetLower(tw, req, host, tc.path)
			if !keepAlive {
				t.Fatal("expected keepAlive to be true")
			}

			ttfb := tw.firstWrite.Sub(start)
			totalTime := tw.lastWrite.Sub(start)

			// Stream-through must have TTFB significantly earlier than total download time
			if ttfb >= totalTime {
				t.Errorf("expected TTFB (%v) < total time (%v)", ttfb, totalTime)
			}
			if totalTime-ttfb < 20*time.Millisecond {
				t.Errorf("expected stream duration gap >= 20ms, got total=%v ttfb=%v gap=%v", totalTime, ttfb, totalTime-ttfb)
			}

			resp, err := http.ReadResponse(bufio.NewReader(&tw.buf), req)
			if err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read body: %v", err)
			}
			if !bytes.Equal(body, payload) {
				t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
			}

			// Verify admitted to RAM cache
			item, src := cacheMgr.GetWithNamespace("gbf", tc.path)
			if item == nil || src != "RAM" {
				t.Fatalf("expected RAM hit on subsequent access, got item=%v src=%s", item, src)
			}
		})
	}
}

// TestStreamThrough_PreAllocatedBuffer_KnownAndUnknownContentLength tests P2 buffer preallocation:
// 1) Known Content-Length within maxRAMItemSize (e.g. 32KB and 64KB)
// 2) Unknown Content-Length (-1 chunked encoding) uses default 64KB capacity
func TestStreamThrough_PreAllocatedBuffer_KnownAndUnknownContentLength(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}

	const host = "prd-game-a-granbluefantasy.akamaized.net"

	testCases := []struct {
		name        string
		path        string
		size        int
		knownLength bool
	}{
		{
			name:        "KnownLength_32KB",
			path:        "/assets/img/sp/prealloc_32k.png",
			size:        32 * 1024,
			knownLength: true,
		},
		{
			name:        "KnownLength_64KB",
			path:        "/assets/img/sp/prealloc_64k.png",
			size:        64 * 1024,
			knownLength: true,
		},
		{
			name:        "UnknownLength_Chunked_32KB",
			path:        "/assets/img/sp/chunked_32k.png",
			size:        32 * 1024,
			knownLength: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload := make([]byte, tc.size)
			copy(payload, []byte("\x89PNG\r\n\x1a\n"))

			ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("ETag", fmt.Sprintf(`"%s-etag"`, tc.name))
				if tc.knownLength {
					w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
				}
				w.WriteHeader(http.StatusOK)

				chunkSize := 8 * 1024
				for i := 0; i < len(payload); i += chunkSize {
					end := i + chunkSize
					if end > len(payload) {
						end = len(payload)
					}
					_, _ = w.Write(payload[i:end])
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
			})
			defer ts.Close()
			setTestAssetClient(srv, client)

			var buf bytes.Buffer
			req, _ := http.NewRequest(http.MethodGet, "https://"+host+tc.path, nil)
			req.RequestURI = tc.path
			req.Host = host

			keepAlive := srv.handleStaticAssetLower(&buf, req, host, tc.path)
			if !keepAlive {
				t.Fatal("expected keepAlive to be true")
			}

			resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
			if err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("failed to read body: %v", err)
			}
			if !bytes.Equal(body, payload) {
				t.Fatalf("body length mismatch: got %d, want %d", len(body), len(payload))
			}

			// Verify cached in RAM
			item, src := cacheMgr.GetWithNamespace("gbf", tc.path)
			if item == nil || src != "RAM" {
				t.Fatalf("expected item cached in RAM, got item=%v src=%s", item, src)
			}
		})
	}
}

// TestStreamThrough_LargeAsset_DiskTempCommit verifies that assets exceeding
// RAM shard admission capacity are streamed to the client, spilled to a safe
// temporary file on disk, committed atomically to disk cache, and NOT placed in RAM cache.
func TestStreamThrough_LargeAsset_DiskTempCommit(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	// Create cache manager with 1MB RAM cache. With 16 shards, each shard limit is 64KB!
	cacheMgr := cache.NewManager(tempDir, 1)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 256KB payload > 64KB shard limit
	const totalSize = 256 * 1024
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"super-large-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/super_large.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("streamed body mismatch for super large file")
	}

	// Verify RAM cache does NOT contain the item, but Disk cache DOES
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit for asset exceeding RAM shard size, got item=%v, src=%s", item, src)
	}

	// Verify no temporary files left in cache directory
	matches, _ := filepath.Glob(filepath.Join(tempDir, "*.tmp.*"))
	if len(matches) > 0 {
		t.Fatalf("found leftover temporary files on disk: %v", matches)
	}
}

// TestStreamThrough_SingleFlight_LeaderFollower_NoDoubleWrite verifies that when
// multiple requests arrive concurrently for the same asset:
// 1. Upstream is contacted strictly once.
// 2. Leader streams to client without double write.
// 3. Follower receives the complete asset via SingleFlight.
func TestStreamThrough_SingleFlight_LeaderFollower_NoDoubleWrite(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const totalSize = 128 * 1024 // 128 KB
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"sf-coalesce-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)

		// Delay download so follower is guaranteed to arrive while download is in-flight
		time.Sleep(40 * time.Millisecond)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/sf_concurrent.png"

	var wg sync.WaitGroup
	wg.Add(2)

	var bufLeader bytes.Buffer
	var bufFollower bytes.Buffer

	// Launch Leader
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&bufLeader, req, host, path)
	}()

	// Launch Follower 5ms later
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&bufFollower, req, host, path)
	}()

	wg.Wait()

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected strictly 1 upstream hit, got %d", upstreamHits.Load())
	}

	// Verify Leader response
	reqL, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respL, err := http.ReadResponse(bufio.NewReader(&bufLeader), reqL)
	if err != nil {
		t.Fatalf("failed to read leader response: %v", err)
	}
	defer respL.Body.Close()
	bodyL, _ := io.ReadAll(respL.Body)
	if !bytes.Equal(bodyL, payload) {
		t.Fatalf("leader body mismatch: got %d bytes, want %d bytes", len(bodyL), len(payload))
	}

	// Verify Follower response
	reqF, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respF, err := http.ReadResponse(bufio.NewReader(&bufFollower), reqF)
	if err != nil {
		t.Fatalf("failed to read follower response: %v", err)
	}
	defer respF.Body.Close()
	bodyF, _ := io.ReadAll(respF.Body)
	if !bytes.Equal(bodyF, payload) {
		t.Fatalf("follower body mismatch: got %d bytes, want %d bytes", len(bodyF), len(payload))
	}
}

type disconnectWriter struct {
	writesBeforeFail int
	writes           int
}

func (dw *disconnectWriter) Write(p []byte) (int, error) {
	dw.writes++
	if dw.writes > dw.writesBeforeFail {
		return 0, errors.New("client connection aborted (broken pipe)")
	}
	return len(p), nil
}

// TestStreamThrough_ClientDisconnect_CompletesCache verifies that if the client
// disconnects halfway through streaming, the proxy continues reading the upstream
// stream in the background, completes validation, and commits the full asset to cache.
func TestStreamThrough_ClientDisconnect_CompletesCache(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const totalSize = 128 * 1024 // 128 KB
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"disconnect-test-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)

		// Send chunks with small delay
		chunkSize := 32 * 1024
		for i := 0; i < len(payload); i += chunkSize {
			end := i + chunkSize
			if end > len(payload) {
				end = len(payload)
			}
			time.Sleep(10 * time.Millisecond)
			_, _ = w.Write(payload[i:end])
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/client_disconnect.png"

	dw := &disconnectWriter{writesBeforeFail: 1} // Fails after writing headers
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	// When client write fails, handleStaticAssetLower returns false to signal
	// connection closure to handleConnect loop.
	keepAlive := srv.handleStaticAssetLower(dw, req, host, path)
	if keepAlive {
		t.Fatal("expected keepAlive to be false when client disconnected")
	}

	// Verify that despite client disconnect, the full asset was committed to cache
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil {
		t.Fatalf("expected asset to be cached despite client disconnect, got nil")
	}
	if !bytes.Equal(item.Data, payload) {
		t.Fatalf("cached data mismatch after client disconnect: len=%d, want=%d", len(item.Data), len(payload))
	}
	if src != "RAM" {
		t.Fatalf("expected RAM cache hit, got %s", src)
	}
}

// TestStreamThrough_UpstreamTruncation_NoPartialCache verifies that if the upstream
// connection is terminated before all bytes are delivered, no partial/half-baked
// data enters the formal cache and any temporary file is deleted.
func TestStreamThrough_UpstreamTruncation_NoPartialCache(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		// Claim 256KB but only write 32KB and then abruptly close connection
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"truncated-etag"`)
		w.Header().Set("Content-Length", "262144")
		w.WriteHeader(http.StatusOK)
		partial := make([]byte, 32*1024)
		copy(partial, []byte("\x89PNG\r\n\x1a\n"))
		_, _ = w.Write(partial)
		// Close underlying connection via Hijacker
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/truncated_asset.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if keepAlive {
		t.Fatal("expected keepAlive to be false when upstream was truncated")
	}

	// Verify NO partial data exists in cache
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item != nil {
		t.Fatalf("partial corrupted data was leaked into cache: %+v", item)
	}

	// Verify no temporary files exist
	matches, _ := filepath.Glob(filepath.Join(tempDir, "*.tmp.*"))
	if len(matches) > 0 {
		t.Fatalf("temporary files were not cleaned up: %v", matches)
	}
}

// TestStreamThrough_EarlyReject_HTML200 verifies that an upstream 200 response
// containing an HTML error page is fast-rejected and never streamed to the client as 200 OK.
func TestStreamThrough_EarlyReject_HTML200(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	htmlError := []byte("<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body>502 Bad Gateway</body></html>")

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png") // Upstream disguised as image/png
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(htmlError)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/error_page.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	srv.handleStaticAssetLower(&buf, req, host, path)

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()

	// Client MUST receive 502 Bad Gateway, NOT 200 OK!
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway for HTML error page disguised as PNG, got %d", resp.StatusCode)
	}

	// Verify nothing was cached
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item != nil {
		t.Fatalf("HTML error page was stored in cache: %+v", item)
	}
}

// TestStreamThrough_ConditionalAndHead_NoStream verifies that HEAD and conditional
// requests (If-None-Match) maintain their existing semantics and are not converted to Stream-Through.
func TestStreamThrough_ConditionalAndHead_NoStream(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 128*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"etag-v1"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/cached_hero.png"

	// 1. Initial GET to populate cache
	var buf1 bytes.Buffer
	req1, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req1.RequestURI = path
	req1.Host = host
	srv.handleStaticAssetLower(&buf1, req1, host, path)

	// 2. HEAD request: must return 200 OK headers with empty body
	var bufHead bytes.Buffer
	reqHead, _ := http.NewRequest(http.MethodHead, "https://"+host+path, nil)
	reqHead.RequestURI = path
	reqHead.Host = host
	srv.handleStaticAssetLower(&bufHead, reqHead, host, path)

	respHead, err := http.ReadResponse(bufio.NewReader(&bufHead), reqHead)
	if err != nil {
		t.Fatalf("failed to read HEAD response: %v", err)
	}
	defer respHead.Body.Close()

	if respHead.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for HEAD, got %d", respHead.StatusCode)
	}
	headBody, _ := io.ReadAll(respHead.Body)
	if len(headBody) != 0 {
		t.Fatalf("expected empty body for HEAD, got %d bytes", len(headBody))
	}

	// 3. Conditional GET with matching If-None-Match: must return 304 Not Modified
	var bufCond bytes.Buffer
	reqCond, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	reqCond.RequestURI = path
	reqCond.Host = host
	reqCond.Header.Set("If-None-Match", `"etag-v1"`)
	srv.handleStaticAssetLower(&bufCond, reqCond, host, path)

	respCond, err := http.ReadResponse(bufio.NewReader(&bufCond), reqCond)
	if err != nil {
		t.Fatalf("failed to read 304 response: %v", err)
	}
	defer respCond.Body.Close()

	if respCond.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got %d", respCond.StatusCode)
	}
	condBody, _ := io.ReadAll(respCond.Body)
	if len(condBody) != 0 {
		t.Fatalf("expected empty body for 304, got %d bytes", len(condBody))
	}
}

// TestStreamThrough_SingleFlight_MultipleFollowers_DiskSpill verifies that when
// multiple followers wait on a leader streaming a large file that spills to disk:
// 1. No data race occurs when followers concurrently access and copy the CacheItem.
// 2. All followers receive the complete file payload read from disk.
func TestStreamThrough_SingleFlight_MultipleFollowers_DiskSpill(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	// 1 MB RAM cache -> 64 KB per shard
	cacheMgr := cache.NewManager(tempDir, 1)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 200 KB > 64 KB shard limit (triggers disk spill)
	const totalSize = 200 * 1024
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"sf-multi-disk-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)

		// Delay writing to allow multiple followers to queue in SingleFlight
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/multi_follower_disk.png"

	const followerCount = 4
	var wg sync.WaitGroup
	wg.Add(1 + followerCount)

	var leaderBuf bytes.Buffer
	followerBufs := make([]bytes.Buffer, followerCount)

	// Launch Leader
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&leaderBuf, req, host, path)
	}()

	// Launch Followers with slight delay to ensure leader starts first
	for i := 0; i < followerCount; i++ {
		idx := i
		go func() {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
			req.RequestURI = path
			req.Host = host
			srv.handleStaticAssetLower(&followerBufs[idx], req, host, path)
		}()
	}

	wg.Wait()

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected strictly 1 upstream hit, got %d", upstreamHits.Load())
	}

	// Verify Leader received full payload
	reqL, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respL, err := http.ReadResponse(bufio.NewReader(&leaderBuf), reqL)
	if err != nil {
		t.Fatalf("failed to read leader response: %v", err)
	}
	defer respL.Body.Close()
	bodyL, _ := io.ReadAll(respL.Body)
	if !bytes.Equal(bodyL, payload) {
		t.Fatalf("leader body mismatch: got %d, want %d", len(bodyL), len(payload))
	}

	// Verify all Followers received full payload
	for i := 0; i < followerCount; i++ {
		reqF, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		respF, err := http.ReadResponse(bufio.NewReader(&followerBufs[i]), reqF)
		if err != nil {
			t.Fatalf("follower %d failed to read response: %v", i, err)
		}
		bodyF, _ := io.ReadAll(respF.Body)
		_ = respF.Body.Close()
		if !bytes.Equal(bodyF, payload) {
			t.Fatalf("follower %d body mismatch: got %d, want %d", i, len(bodyF), len(payload))
		}
	}
}

// TestStreamThrough_ChunkedSmallAsset_UnexpectedEOF verifies that a small chunked
// asset (< 4KB) with unknown Content-Length correctly finishes early reading via
// io.ErrUnexpectedEOF, stores in RAM, and serves cleanly without error.
func TestStreamThrough_ChunkedSmallAsset_UnexpectedEOF(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 2048) // 2 KB < 4 KB
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"chunked-small-etag"`)
		// DO NOT set Content-Length (forces chunked / unknown length)
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/chunked_small.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch for chunked small asset")
	}

	// Verify cached in RAM
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM cache hit for chunked small asset, got item=%v, src=%s", item, src)
	}
}

// TestStreamThrough_DiskSpill_CorruptedTail_CleansTempFile verifies that if an asset
// spills to disk but post-stream validation fails, no temporary file is leaked on disk.
func TestStreamThrough_DiskSpill_CorruptedTail_CleansTempFile(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 1) // 64 KB shard limit
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 128 KB payload: starts with valid PNG magic so early stream check passes,
	// but ends with HTML error content which fails final validation.
	payload := make([]byte, 128*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)
		// Upstream abruptly truncates after 60KB
		_, _ = w.Write(payload[:60*1024])
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/truncated_spill.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	srv.handleStaticAssetLower(&buf, req, host, path)

	// Verify NO temporary files remain anywhere on disk
	matches, _ := filepath.Glob(filepath.Join(tempDir, "**", "*.tmp.*"))
	matches2, _ := filepath.Glob(filepath.Join(tempDir, "*.tmp.*"))
	allMatches := append(matches, matches2...)
	if len(allMatches) > 0 {
		t.Fatalf("expected all temp files to be removed, found: %v", allMatches)
	}

	// Verify nothing was cached
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item != nil {
		t.Fatalf("corrupted stream was stored in cache: %+v", item)
	}
}

// TestStreamThrough_LargeAsset_ClientDisconnect_CompletesDiskCache verifies that
// if the client disconnects while streaming an oversized asset that spills to disk,
// the proxy continues downloading upstream and successfully commits the file to disk.
func TestStreamThrough_LargeAsset_ClientDisconnect_CompletesDiskCache(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 1) // 64 KB shard limit
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const totalSize = 256 * 1024 // 256 KB > 64 KB shard limit
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"disconnect-large-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)

		chunkSize := 32 * 1024
		for i := 0; i < len(payload); i += chunkSize {
			end := i + chunkSize
			if end > len(payload) {
				end = len(payload)
			}
			time.Sleep(10 * time.Millisecond)
			_, _ = w.Write(payload[i:end])
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/client_disconnect_large.png"

	dw := &disconnectWriter{writesBeforeFail: 2} // Fails after writing headers + first chunk
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(dw, req, host, path)
	if keepAlive {
		t.Fatal("expected keepAlive to be false when client disconnected")
	}

	// Verify asset was successfully committed to DISK cache
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK cache hit after large client disconnect, got item=%v, src=%s", item, src)
	}
	if !bytes.Equal(item.Data, payload) {
		t.Fatalf("cached data mismatch on disk after client disconnect")
	}
}

// TestStreamThrough_CacheMiss_HeadAndConditional verifies HEAD and conditional GET
// requests directly on a Cache Miss without prior cache population.
func TestStreamThrough_CacheMiss_HeadAndConditional(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 128*1024)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"upstream-etag-1"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/miss_head_asset.png"

	// 1. Direct HEAD request on Cache Miss
	var bufHead bytes.Buffer
	reqHead, _ := http.NewRequest(http.MethodHead, "https://"+host+path, nil)
	reqHead.RequestURI = path
	reqHead.Host = host
	srv.handleStaticAssetLower(&bufHead, reqHead, host, path)

	respHead, err := http.ReadResponse(bufio.NewReader(&bufHead), reqHead)
	if err != nil {
		t.Fatalf("failed to read HEAD response: %v", err)
	}
	defer respHead.Body.Close()

	if respHead.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for HEAD, got %d", respHead.StatusCode)
	}
	headBody, _ := io.ReadAll(respHead.Body)
	if len(headBody) != 0 {
		t.Fatalf("expected empty body for HEAD, got %d bytes", len(headBody))
	}

	// Asset should now be cached
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil {
		t.Fatal("expected HEAD request on cache miss to populate cache")
	}

	// 2. Direct conditional GET with matching ETag on different asset (cache miss)
	const path2 = "/assets/img/sp/miss_cond_asset.png"
	var bufCond bytes.Buffer
	reqCond, _ := http.NewRequest(http.MethodGet, "https://"+host+path2, nil)
	reqCond.RequestURI = path2
	reqCond.Host = host
	reqCond.Header.Set("If-None-Match", `"upstream-etag-1"`)
	srv.handleStaticAssetLower(&bufCond, reqCond, host, path2)

	respCond, err := http.ReadResponse(bufio.NewReader(&bufCond), reqCond)
	if err != nil {
		t.Fatalf("failed to read conditional response: %v", err)
	}
	defer respCond.Body.Close()

	if respCond.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified for matching ETag on cache miss, got %d", respCond.StatusCode)
	}
}

// TestStreamThrough_SingleFlight_Follower_HeadRequest_DiskSpill verifies that when
// a follower sends a HEAD request while a leader is streaming a large file that spills to disk:
// 1. Follower receives 200 OK with correct Content-Length header equal to total file size.
// 2. Follower receives an empty body without loading the entire disk file into memory.
// 3. Concurrent GET follower still receives the full payload.
func TestStreamThrough_SingleFlight_Follower_HeadRequest_DiskSpill(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	// 1 MB RAM cache -> 64 KB per shard limit
	cacheMgr := cache.NewManager(tempDir, 1)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 200 KB > 64 KB shard limit (triggers disk spill)
	const totalSize = 200 * 1024
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"sf-follower-head-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)

		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/follower_head_disk.png"

	var wg sync.WaitGroup
	wg.Add(3)

	var leaderBuf bytes.Buffer
	var headFollowerBuf bytes.Buffer
	var getFollowerBuf bytes.Buffer

	// Launch Leader (GET)
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&leaderBuf, req, host, path)
	}()

	// Launch Follower 1 (HEAD)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodHead, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&headFollowerBuf, req, host, path)
	}()

	// Launch Follower 2 (GET)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&getFollowerBuf, req, host, path)
	}()

	wg.Wait()

	if upstreamHits.Load() != 1 {
		t.Fatalf("expected strictly 1 upstream hit, got %d", upstreamHits.Load())
	}

	// Verify Leader received full payload
	reqL, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respL, err := http.ReadResponse(bufio.NewReader(&leaderBuf), reqL)
	if err != nil {
		t.Fatalf("failed to read leader response: %v", err)
	}
	defer respL.Body.Close()
	bodyL, _ := io.ReadAll(respL.Body)
	if !bytes.Equal(bodyL, payload) {
		t.Fatalf("leader body mismatch: got %d, want %d", len(bodyL), len(payload))
	}

	// Verify HEAD Follower: 200 OK, empty body, Content-Length == totalSize
	reqHead, _ := http.NewRequest(http.MethodHead, "https://"+host+path, nil)
	respHead, err := http.ReadResponse(bufio.NewReader(&headFollowerBuf), reqHead)
	if err != nil {
		t.Fatalf("failed to read HEAD follower response: %v", err)
	}
	defer respHead.Body.Close()
	if respHead.StatusCode != http.StatusOK {
		t.Fatalf("HEAD follower expected 200 OK, got %d", respHead.StatusCode)
	}
	if respHead.ContentLength != int64(totalSize) {
		t.Fatalf("HEAD follower ContentLength mismatch: got %d, want %d", respHead.ContentLength, totalSize)
	}
	bodyHead, _ := io.ReadAll(respHead.Body)
	if len(bodyHead) != 0 {
		t.Fatalf("HEAD follower expected empty body, got %d bytes", len(bodyHead))
	}

	// Verify GET Follower: 200 OK, full payload
	reqGet, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respGet, err := http.ReadResponse(bufio.NewReader(&getFollowerBuf), reqGet)
	if err != nil {
		t.Fatalf("failed to read GET follower response: %v", err)
	}
	defer respGet.Body.Close()
	bodyGet, _ := io.ReadAll(respGet.Body)
	if !bytes.Equal(bodyGet, payload) {
		t.Fatalf("GET follower body mismatch: got %d, want %d", len(bodyGet), len(payload))
	}
}

// TestStreamThrough_InitialReadPrematureEOF_TruncatedNotCached covers Problem 1:
// When Content-Length > 4096 is announced by upstream, but upstream sends < 4096 bytes
// before unexpected EOF, the partial payload must NOT be treated as a complete small asset
// and must NOT be written to cache. The proxy must return 502 Bad Gateway to the client.
func TestStreamThrough_InitialReadPrematureEOF_TruncatedNotCached(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		// Claim 100KB (> 4KB), but only write 1500 bytes (< 4KB) and immediately close connection
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"premature-eof-etag"`)
		w.Header().Set("Content-Length", "102400")
		w.WriteHeader(http.StatusOK)

		partial := make([]byte, 1500)
		copy(partial, []byte("\x89PNG\r\n\x1a\n"))
		_, _ = w.Write(partial)

		// Abruptly close underlying TCP connection via Hijack
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/premature_eof.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	srv.handleStaticAssetLower(&buf, req, host, path)

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()

	// Must return 502 Bad Gateway, NEVER 200 OK!
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 Bad Gateway for truncated initial read, got %d", resp.StatusCode)
	}

	// Must NOT be cached
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item != nil {
		t.Fatalf("truncated payload was incorrectly saved to cache: %+v", item)
	}

	// Verify no temporary files exist
	matches, _ := filepath.Glob(filepath.Join(tempDir, "*.tmp.*"))
	if len(matches) > 0 {
		t.Fatalf("temporary files were not cleaned up: %v", matches)
	}
}

// TestStreamThrough_CacheSinkError_ClientReceivesFullResponse covers Problem 2:
// When stream-through starts sending 200 and data to the client, but creating
// the temporary cache file fails (e.g. disk sink failure), the proxy must continue
// streaming upstream data to the client without truncating or closing the connection.
func TestStreamThrough_CacheSinkError_ClientReceivesFullResponse(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	// Manager with 1MB RAM -> MaxRAMItemSize is 1MB / 16 = 64KB
	cacheMgr := cache.NewManager(tempDir, 1)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 256KB payload > 64KB MaxRAMItemSize -> triggers CreateDiskTempFile
	const totalSize = 256 * 1024
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"cache-sink-error-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/sink_error.png"

	// Intentionally cause CreateDiskTempFile to fail by creating a file where the directory should be
	dirToBlock := filepath.Join(tempDir, "assets", "img", "sp")
	if err := os.MkdirAll(filepath.Dir(dirToBlock), 0755); err != nil {
		t.Fatalf("failed to setup test dir: %v", err)
	}
	if err := os.WriteFile(dirToBlock, []byte("blocker"), 0644); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true despite cache sink error")
	}

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
	}

	// Verify nothing was cached
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item != nil {
		t.Fatalf("expected no cache item after sink error, got: %+v", item)
	}
}

// TestStreamThrough_DynamicSpillError_ClientReceivesFullResponse covers Problem 2:
// When an asset starts accumulating in RAM, but spills to disk after exceeding
// MaxRAMItemSize, and disk temp file creation fails during spill, stream-through
// must continue reading upstream and forwarding all data to the client cleanly.
func TestStreamThrough_DynamicSpillError_ClientReceivesFullResponse(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	// Manager with 1MB RAM -> MaxRAMItemSize is 1MB / 16 = 64KB
	cacheMgr := cache.NewManager(tempDir, 1)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	// 128KB payload > 64KB MaxRAMItemSize, served with chunked encoding (Content-Length = -1)
	const totalSize = 128 * 1024
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"dynamic-spill-error-etag"`)
		// Chunked transfer (no Content-Length header)
		w.WriteHeader(http.StatusOK)

		chunkSize := 16 * 1024
		for i := 0; i < len(payload); i += chunkSize {
			end := i + chunkSize
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write(payload[i:end])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/chunked_spill_error.png"

	// Intentionally cause CreateDiskTempFile on spill to fail by creating a file where the directory should be
	dirToBlock := filepath.Join(tempDir, "assets", "img", "sp")
	if err := os.MkdirAll(filepath.Dir(dirToBlock), 0755); err != nil {
		t.Fatalf("failed to setup test dir: %v", err)
	}
	if err := os.WriteFile(dirToBlock, []byte("blocker"), 0644); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true despite dynamic spill error")
	}

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch: got %d bytes, want %d bytes", len(body), len(payload))
	}

	// Verify nothing was cached
	item, _ := cacheMgr.GetWithNamespace("gbf", path)
	if item != nil {
		t.Fatalf("expected no cache item after dynamic spill error, got: %+v", item)
	}
}

// TestStreamThrough_InitialReadUnknownLength_SmallAssetCached verifies that when
// Content-Length < 0 (unknown/chunked), a small asset <= 4KB that ends naturally
// with EOF is treated as a complete response and properly cached in RAM.
func TestStreamThrough_InitialReadUnknownLength_SmallAssetCached(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	payload := make([]byte, 1500)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"small-chunked-etag"`)
		// No Content-Length (Content-Length < 0)
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

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/small_chunked.png"

	var buf bytes.Buffer
	req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	req.RequestURI = path
	req.Host = host

	keepAlive := srv.handleStaticAssetLower(&buf, req, host, path)
	if !keepAlive {
		t.Fatal("expected keepAlive to be true")
	}

	resp, err := http.ReadResponse(bufio.NewReader(&buf), req)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for complete chunked small asset, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Fatalf("body mismatch")
	}

	// Verify cached in RAM
	item, src := cacheMgr.GetWithNamespace("gbf", path)
	if item == nil || src != "RAM" {
		t.Fatalf("expected RAM cache hit for complete chunked small asset, got item=%v src=%s", item, src)
	}
}

// TestStreamThrough_CacheSinkError_LeaderCompletesFollowerFailsGracefully verifies that
// when the leader's cache write fails, the leader still gets the full 200 OK response,
// and any concurrent follower waiting on SingleFlight receives 502 Bad Gateway without hanging or racing.
func TestStreamThrough_CacheSinkError_LeaderCompletesFollowerFailsGracefully(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 1) // 64KB RAM limit
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	const totalSize = 256 * 1024
	payload := make([]byte, totalSize)
	copy(payload, []byte("\x89PNG\r\n\x1a\n"))

	var upstreamHits atomic.Int64
	ts, client := newMockAssetServer(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"concurrent-sink-error-etag"`)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		w.WriteHeader(http.StatusOK)

		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(payload)
	})
	defer ts.Close()

	srv := &ProxyServer{
		cfgMgr:   cfgMgr,
		cacheMgr: cacheMgr,
		stats:    stats,
	}
	setTestAssetClient(srv, client)

	const host = "prd-game-a-granbluefantasy.akamaized.net"
	const path = "/assets/img/sp/concurrent_sink_error.png"

	// Intentionally cause CreateDiskTempFile to fail by creating a blocker file
	dirToBlock := filepath.Join(tempDir, "assets", "img", "sp")
	if err := os.MkdirAll(filepath.Dir(dirToBlock), 0755); err != nil {
		t.Fatalf("failed to setup test dir: %v", err)
	}
	if err := os.WriteFile(dirToBlock, []byte("blocker"), 0644); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}

	var leaderBuf, followerBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)

	// Leader
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&leaderBuf, req, host, path)
	}()

	// Follower
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.RequestURI = path
		req.Host = host
		srv.handleStaticAssetLower(&followerBuf, req, host, path)
	}()

	wg.Wait()

	// Leader must receive 200 OK with full payload
	reqL, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respL, err := http.ReadResponse(bufio.NewReader(&leaderBuf), reqL)
	if err != nil {
		t.Fatalf("failed to read leader response: %v", err)
	}
	defer respL.Body.Close()
	if respL.StatusCode != http.StatusOK {
		t.Fatalf("leader expected 200 OK, got %d", respL.StatusCode)
	}
	bodyL, _ := io.ReadAll(respL.Body)
	if !bytes.Equal(bodyL, payload) {
		t.Fatalf("leader body mismatch: got %d, want %d", len(bodyL), len(payload))
	}

	// Follower must receive 502 Bad Gateway (since no cache was stored)
	reqF, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
	respF, err := http.ReadResponse(bufio.NewReader(&followerBuf), reqF)
	if err != nil {
		t.Fatalf("failed to read follower response: %v", err)
	}
	defer respF.Body.Close()
	if respF.StatusCode != http.StatusBadGateway {
		t.Fatalf("follower expected 502 Bad Gateway, got %d", respF.StatusCode)
	}
}


