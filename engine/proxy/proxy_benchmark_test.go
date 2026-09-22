package proxy

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func BenchmarkWriteHTTPResponse(b *testing.B) {
	hdr := make(http.Header)
	hdr.Set("Content-Type", "image/png")
	hdr.Set("ETag", "\"12345678\"")
	hdr.Set("Cache-Control", "max-age=31536000, immutable")
	hdr.Set("Access-Control-Allow-Origin", "*")
	hdr.Add("Set-Cookie", "session=abc123; Path=/; Secure; HttpOnly")
	hdr.Add("Set-Cookie", "pref=dark; Path=/")

	body := []byte("\x89PNG\r\n\x1a\nbenchmark_image_data_payload_bytes_here")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writeHTTPResponse(io.Discard, http.StatusOK, hdr, body, false, false)
	}
}

func BenchmarkExtractAssetRefs(b *testing.B) {
	pe := &PrefetchEngine{}
	sampleJS := []byte(`
		var manifest = {
			img1: "assets/img/sp/quest/scene/character/body/3040001000.png",
			sound1: '/sound/se/se_100.mp3',
			cjs: "sp/cjs/npc_3040001000.png",
			font1: "font/main.woff2",
			css1: "css/common.css"
		};
	`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pe.extractAssetRefs("/assets/js/bundle.js", sampleJS, "prd-game-a-granbluefantasy.akamaized.net")
	}
}

func BenchmarkExtractAssetRefs_With500KBJS(b *testing.B) {
	pe := &PrefetchEngine{}
	chunk := []byte(`
		var manifest = {
			img1: "assets/img/sp/quest/scene/character/body/3040001000.png",
			sound1: '/sound/se/se_100.mp3',
			cjs: "sp/cjs/npc_3040001000.png",
			font1: "font/main.woff2",
			css1: "css/common.css"
		};
		// padding text padding text padding text padding text padding text
	`)
	var largeJS []byte
	for len(largeJS) < 500*1024 {
		largeJS = append(largeJS, chunk...)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pe.extractAssetRefs("/assets/js/bundle.js", largeJS, "prd-game-a-granbluefantasy.akamaized.net")
	}
}

// benchCachedAssetPayload builds a deterministic payload of n bytes.
func benchCachedAssetPayload(n int) []byte {
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	return payload
}

// benchCachedAssetItem builds a representative RAM cache item for the given payload size.
func benchCachedAssetItem(size int) *cache.CacheItem {
	return &cache.CacheItem{
		Key:             "assets/js/1000/bench.js",
		Data:            benchCachedAssetPayload(size),
		ContentType:     "application/javascript",
		ContentEncoding: "gzip",
		ETag:            `"bench-etag-v1"`,
		LastModified:    "Wed, 21 Oct 2026 07:28:00 GMT",
		Size:            int64(size),
	}
}

func newBenchmarkSerializationServer(b *testing.B) *ProxyServer {
	b.Helper()
	tempDir := b.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	return &ProxyServer{cfgMgr: cfgMgr}
}

const benchAssetPath = "/assets/js/1000/bench.js"

// BenchmarkCachedAssetResponseLegacy measures the pre-optimization response path, where
// sendAssetResponse rebuilds a fresh http.Header for every RAM hit. Sizes cover both
// write strategies: a payload that fits next to the head in the pooled buffer and one
// that is scattered with net.Buffers.
func BenchmarkCachedAssetResponseLegacy(b *testing.B) {
	srv := newBenchmarkSerializationServer(b)
	for _, size := range []int{8 * 1024, 256 * 1024} {
		item := benchCachedAssetItem(size)
		b.Run(fmt.Sprintf("payload_%dKiB", size/1024), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				srv.sendAssetResponse(io.Discard, http.StatusOK, item, false, benchAssetPath, false)
			}
		})
	}
}

// BenchmarkCachedAssetResponseFast measures the cached-asset fast path, which serializes
// straight from the CacheItem fields without constructing an http.Header.
func BenchmarkCachedAssetResponseFast(b *testing.B) {
	srv := newBenchmarkSerializationServer(b)
	for _, size := range []int{8 * 1024, 256 * 1024} {
		item := benchCachedAssetItem(size)
		b.Run(fmt.Sprintf("payload_%dKiB", size/1024), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				srv.sendCachedAssetResponseFast(io.Discard, item, false, benchAssetPath, false)
			}
		})
	}
}

// BenchmarkRAMHitCachedAssetHandler measures the complete RAM hit handler (cache lookup,
// browser cache policy lookup and serialization) with the fast path wired in. The
// pre-optimization end-to-end cost equals this benchmark plus the per-hit serializer
// delta reported by BenchmarkCachedAssetResponseLegacy vs
// BenchmarkCachedAssetResponseFast.
func BenchmarkRAMHitCachedAssetHandler(b *testing.B) {
	tempDir := b.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := &ProxyServer{cfgMgr: cfgMgr, cacheMgr: cacheMgr, stats: stats}

	payload := benchCachedAssetPayload(8 * 1024)
	if _, ok := cacheMgr.SaveRAM(benchAssetPath, map[string]string{"content-type": "application/javascript"}, payload); !ok {
		b.Fatalf("failed to seed the RAM cache for %s", benchAssetPath)
	}

	const benchHost = "prd-game-a-granbluefantasy.akamaized.net"
	req, err := http.NewRequest(http.MethodGet, "https://"+benchHost+benchAssetPath, nil)
	if err != nil {
		b.Fatalf("failed to build the benchmark request: %v", err)
	}
	req.RequestURI = benchAssetPath

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		srv.handleStaticAssetLower(io.Discard, req, benchHost, benchAssetPath)
	}
}
