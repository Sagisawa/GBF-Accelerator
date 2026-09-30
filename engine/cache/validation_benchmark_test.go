package cache

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"
)

func makeGzipData(payload []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(payload)
	_ = gw.Close()
	return buf.Bytes()
}

func BenchmarkIsValidCacheContentCases(b *testing.B) {
	// 1. Small PNG (5 KB)
	smallPng := makeFakePNG(5 * 1024)
	// 2. Large PNG (250 KB)
	largePng := makeFakePNG(250 * 1024)
	// 3. Plain JS (30 KB)
	plainJS := makeFakeJS(30 * 1024)
	// 4. Plain JS (200 KB)
	largePlainJS := makeFakeJS(200 * 1024)
	// 5. Gzipped JS (100 KB uncompressed -> ~25 KB compressed)
	rawJS := makeFakeJS(100 * 1024)
	gzippedJS := makeGzipData(rawJS)
	// 6. Gzipped JSON (300 KB uncompressed -> ~50 KB compressed)
	rawJSON := []byte(`{"response":{"data":{"quest_id":12345,"title":"test_quest","items":[` + string(makeFakeJS(200*1024)) + `]}}}`)
	gzippedJSON := makeGzipData(rawJSON)

	testCases := []struct {
		name        string
		path        string
		contentType string
		data        []byte
	}{
		{"SmallPNG_5KB", "/assets/img/sp/icon.png", "image/png", smallPng},
		{"LargePNG_250KB", "/assets/img/sp/bg_large.png", "image/png", largePng},
		{"PlainJS_30KB", "/assets/js/bundle.js", "application/javascript", plainJS},
		{"PlainJS_200KB", "/assets/js/engine.js", "application/javascript", largePlainJS},
		{"GzipJS_25KB", "/assets/js/model/manifest.js", "application/javascript", gzippedJS},
		{"GzipJSON_50KB", "/assets/data/quest.json", "application/json", gzippedJSON},
	}

	for _, tc := range testCases {
		tc := tc
		// Measure single validation call
		b.Run(fmt.Sprintf("Single_%s", tc.name), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ok := IsValidCacheContent(tc.path, tc.contentType, tc.data)
				if !ok {
					b.Fatal("expected valid")
				}
			}
		})

		// Measure current code behavior: DUPLICATE validation (proxy.go + saveRAMInternal)
		b.Run(fmt.Sprintf("Duplicate_%s", tc.name), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Call 1 in proxy.go
				ok1 := IsValidCacheContent(tc.path, tc.contentType, tc.data)
				// Call 2 in saveRAMInternal
				ok2 := IsValidCacheContent(tc.path, tc.contentType, tc.data)
				if !ok1 || !ok2 {
					b.Fatal("expected valid")
				}
			}
		})
	}
}
