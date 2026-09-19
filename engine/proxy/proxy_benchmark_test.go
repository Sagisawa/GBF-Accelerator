package proxy

import (
	"io"
	"net/http"
	"testing"
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
