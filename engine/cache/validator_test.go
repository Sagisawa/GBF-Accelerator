package cache

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func TestIsValidCacheContent(t *testing.T) {
	// 1. Valid PNG
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if !IsValidCacheContent("/assets/img/hero.png", "image/png", validPng) {
		t.Error("expected valid PNG to pass validation")
	}

	// 2. Corrupted PNG
	corruptPng := []byte("not_a_png_file")
	if IsValidCacheContent("/assets/img/hero.png", "image/png", corruptPng) {
		t.Error("expected corrupt PNG to fail validation")
	}

	// 3. HTML error page returned for PNG
	htmlErr := []byte("<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body>502</body></html>")
	if IsValidCacheContent("/assets/img/hero.png", "text/html", htmlErr) {
		t.Error("expected HTML error page for PNG to fail validation")
	}

	// 4. HTML error page with image/png content-type header (upstream misconfiguration)
	if IsValidCacheContent("/assets/img/hero.png", "image/png", htmlErr) {
		t.Error("expected HTML body to fail even with image/png content-type")
	}

	// 5. Gzip-compressed HTML error page
	gzippedHtml := gzipBytes(htmlErr)
	if IsValidCacheContent("/assets/img/hero.png", "image/png", gzippedHtml) {
		t.Error("expected gzipped HTML error page to fail validation")
	}

	// 6. Valid JPEG
	validJpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF")
	if !IsValidCacheContent("/assets/img/photo.jpg", "image/jpeg", validJpg) {
		t.Error("expected valid JPEG to pass")
	}

	// 7. Valid WebP
	validWebp := append([]byte("RIFF1234WEBP"), []byte("VP8 ")...)
	if !IsValidCacheContent("/assets/img/banner.webp", "image/webp", validWebp) {
		t.Error("expected valid WebP to pass")
	}

	// 8. Valid GIF
	validGif := []byte("GIF89a\x01\x00\x01\x00")
	if !IsValidCacheContent("/assets/img/anim.gif", "image/gif", validGif) {
		t.Error("expected valid GIF to pass")
	}

	// 9. Valid WAV
	validWav := append([]byte("RIFF1234WAVE"), []byte("fmt ")...)
	if !IsValidCacheContent("/assets/sound/bgm.wav", "audio/wav", validWav) {
		t.Error("expected valid WAV to pass")
	}

	// 10. Valid WOFF2
	validWoff2 := []byte("wOF2\x00\x01\x00\x00")
	if !IsValidCacheContent("/assets/font/main.woff2", "font/woff2", validWoff2) {
		t.Error("expected valid WOFF2 to pass")
	}

	// 11. Valid JS script
	validJs := []byte("console.log('hello world'); function test() { return 42; }")
	if !IsValidCacheContent("/assets/js/bundle.js", "application/javascript", validJs) {
		t.Error("expected valid JS to pass")
	}

	// 12. 502 HTML error page disguised as JS
	if IsValidCacheContent("/assets/js/bundle.js", "text/html", htmlErr) {
		t.Error("expected 502 HTML page disguised as JS to fail")
	}

	// 13. Raw 502 text error for JS
	raw502 := []byte("502 Bad Gateway\nnginx/1.18.0")
	if IsValidCacheContent("/assets/js/bundle.js", "text/plain", raw502) {
		t.Error("expected raw 502 text to fail validation")
	}

	// 14. Zero length data
	if IsValidCacheContent("/assets/test.png", "image/png", []byte{}) {
		t.Error("expected empty data to fail")
	}

	// 15. Valid WASM
	validWasm := []byte("\x00asm\x01\x00\x00\x00")
	if !IsValidCacheContent("/assets/wasm/engine.wasm", "application/wasm", validWasm) {
		t.Error("expected valid WASM to pass")
	}

	// 16. Corrupted WASM
	if IsValidCacheContent("/assets/wasm/engine.wasm", "application/wasm", []byte("invalid_wasm")) {
		t.Error("expected invalid WASM to fail")
	}

	// 17. Valid OGG
	validOgg := []byte("OggS\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00")
	if !IsValidCacheContent("/assets/sound/bgm.ogg", "audio/ogg", validOgg) {
		t.Error("expected valid OGG to pass")
	}

	// 18. Corrupted OGG
	if IsValidCacheContent("/assets/sound/bgm.ogg", "audio/ogg", []byte("corrupt_ogg_bytes")) {
		t.Error("expected invalid OGG to fail")
	}

	// 19. M4A HTML error page rejection
	if IsValidCacheContent("/assets/sound/voice.m4a", "text/html", htmlErr) {
		t.Error("expected HTML error page for M4A to fail")
	}

	// 20. PNG extension with JPEG bytes (upstream GBF base_character_empty.png anomaly)
	exifJpgDisguisedAsPng := []byte("\xff\xd8\xff\xe1\x00\x18Exif\x00\x00II*\x00\x08\x00\x00\x00")
	if !IsValidCacheContent("/assets/img/sp/arcarum3/dungeon/base_character_empty.png", "image/png", exifJpgDisguisedAsPng) {
		t.Error("expected JPEG disguised as PNG to pass validation")
	}

	// 21. PNG extension with WebP bytes
	if !IsValidCacheContent("/assets/img/sp/test.png", "image/png", validWebp) {
		t.Error("expected WebP disguised as PNG to pass validation")
	}

	// 22. JPG extension with PNG bytes
	if !IsValidCacheContent("/assets/img/sp/test.jpg", "image/jpeg", validPng) {
		t.Error("expected PNG disguised as JPG to pass validation")
	}
}

func TestCanEarlyStream(t *testing.T) {
	// 1. Valid PNG chunk (at least 8 bytes)
	validPngChunk := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if !CanEarlyStream("/assets/img/hero.png", "image/png", validPngChunk) {
		t.Error("expected valid PNG chunk to pass early stream check")
	}

	// 2. Corrupted PNG chunk (bad magic)
	if CanEarlyStream("/assets/img/hero.png", "image/png", []byte("bad_png_magic_bytes")) {
		t.Error("expected bad PNG magic bytes to fail early stream check")
	}

	// 3. HTML 502 error page returned with 200 status for PNG
	htmlErr := []byte("<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body>502</body></html>")
	if CanEarlyStream("/assets/img/hero.png", "image/png", htmlErr) {
		t.Error("expected HTML error chunk to fail early stream check for PNG")
	}

	// 4. HTML error content-type rejection
	if CanEarlyStream("/assets/js/bundle.js", "text/html", []byte("console.log('test');")) {
		t.Error("expected text/html content-type to be rejected")
	}

	// 5. Valid JS script chunk
	validJsChunk := []byte("var a = 1; window.Game = {};")
	if !CanEarlyStream("/assets/js/bundle.js", "application/javascript", validJsChunk) {
		t.Error("expected valid JS chunk to pass early stream check")
	}

	// 6. Valid gzip-compressed JS chunk
	validGzipJs := gzipBytes([]byte("console.log('hello world from gzipped js bundle');"))
	if !CanEarlyStream("/assets/js/bundle.js", "application/javascript", validGzipJs) {
		t.Error("expected valid complete gzip JS to pass early stream check")
	}

	// 7. Gzipped HTML error page (should be rejected)
	gzippedHtml := gzipBytes(htmlErr)
	if CanEarlyStream("/assets/js/bundle.js", "application/javascript", gzippedHtml) {
		t.Error("expected gzipped HTML error to be rejected by early stream check")
	}

	// 8. Truncated / malformed gzip chunk (conservative fallback: should return false)
	truncatedGzip := []byte{0x1f, 0x8b, 0x08, 0x00}
	if CanEarlyStream("/assets/js/bundle.js", "application/javascript", truncatedGzip) {
		t.Error("expected truncated gzip chunk to return false for conservative fallback")
	}

	// 9. Zero-length chunk
	if CanEarlyStream("/assets/img/hero.png", "image/png", nil) {
		t.Error("expected empty chunk to return false")
	}

	// 10. Non-asset extension (e.g. custom file) passes early stream
	if !CanEarlyStream("/unknown/resource.custom", "application/octet-stream", []byte("any_data")) {
		t.Error("expected non-asset extension to pass")
	}

	// 11. PNG extension with JPEG magic bytes passes early stream (GBF upstream anomaly)
	exifJpgChunk := []byte("\xff\xd8\xff\xe1\x00\x18Exif\x00\x00II*\x00\x08\x00\x00\x00")
	if !CanEarlyStream("/assets/img/sp/arcarum3/dungeon/base_character_empty.png", "image/png", exifJpgChunk) {
		t.Error("expected JPEG chunk disguised as PNG to pass early stream check")
	}
}

func gzipBytesWithLargeHeader(data []byte, extraSize int) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Header.Name = "large_header_asset.bin"
	w.Header.Comment = string(bytes.Repeat([]byte("C"), 200))
	w.Header.Extra = bytes.Repeat([]byte("E"), extraSize)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}

func TestGzipFullVsSampleValidationAndAuditNoFalseDelete(t *testing.T) {
	// Construct valid uncompressed payloads (> 4KB so compressed stream has multiple chunks)
	rawPng := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), bytes.Repeat([]byte("PNGDATA-BLOCK-"), 512)...)
	rawWoff2 := append([]byte("wOF2\x00\x01\x00\x00"), bytes.Repeat([]byte("FONT-GLYPH-TABLE-"), 512)...)
	rawTtf := append([]byte("\x00\x01\x00\x00\x00\x0c\x00\x80"), bytes.Repeat([]byte("TTF-OUTLINE-"), 512)...)
	rawJs := bytes.Repeat([]byte("window.Game = window.Game || {}; console.log('ok');\n"), 200)
	htmlErr := []byte("<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head><body>502 Bad Gateway</body></html>")

	gzPng := gzipBytes(rawPng)
	gzWoff2 := gzipBytes(rawWoff2)
	gzTtf := gzipBytes(rawTtf)
	gzJs := gzipBytes(rawJs)
	gzHtml := gzipBytes(htmlErr)

	// Gzip with > 1KB header so the first 512 compressed bytes cannot produce any decompressed magic bytes
	gzPngLargeHdr := gzipBytesWithLargeHeader(rawPng, 600)
	gzWoff2LargeHdr := gzipBytesWithLargeHeader(rawWoff2, 600)
	gzTtfLargeHdr := gzipBytesWithLargeHeader(rawTtf, 600)

	// 1. Complete valid Gzip resources (images, fonts, JS, including >512B gzip headers) must pass full validation
	if !IsValidCacheContent("/assets/img/hero.png", "image/png", gzPng) {
		t.Fatal("expected valid gzipped PNG to pass IsValidCacheContent")
	}
	if !IsValidCacheContent("/assets/img/hero_hdr.png", "image/png", gzPngLargeHdr) {
		t.Fatal("expected valid gzipped PNG with >512B gzip header to pass IsValidCacheContent")
	}
	if !IsValidCacheContent("/assets/font/game.woff2", "font/woff2", gzWoff2) {
		t.Fatal("expected valid gzipped WOFF2 to pass IsValidCacheContent")
	}
	if !IsValidCacheContent("/assets/font/game_hdr.woff2", "font/woff2", gzWoff2LargeHdr) {
		t.Fatal("expected valid gzipped WOFF2 with >512B gzip header to pass IsValidCacheContent")
	}
	if !IsValidCacheContent("/assets/font/ui.ttf", "font/ttf", gzTtfLargeHdr) {
		t.Fatal("expected valid gzipped TTF with >512B gzip header to pass IsValidCacheContent")
	}
	if !IsValidCacheContent("/assets/js/app.js", "application/javascript", gzJs) {
		t.Fatal("expected valid gzipped JS to pass IsValidCacheContent")
	}

	// 2. Gzipped HTML error pages must be rejected in both full and sample modes
	if IsValidCacheContent("/assets/img/hero.png", "image/png", gzHtml) {
		t.Fatal("expected gzipped HTML error page for PNG to fail IsValidCacheContent")
	}
	if IsValidCacheContent("/assets/js/app.js", "application/javascript", gzHtml) {
		t.Fatal("expected gzipped HTML error page for JS to fail IsValidCacheContent")
	}
	if IsValidCacheSample("/assets/js/app.js", "application/javascript", gzHtml[:len(gzHtml)-4], false) {
		t.Fatal("expected truncated gzipped HTML error page to fail IsValidCacheSample")
	}

	// 3. Valid truncated prefix sample: must PASS prefix sample check (isComplete=false / CanEarlyStream)
	//    but MUST FAIL full resource check (isComplete=true / IsValidCacheContent)!
	for _, tc := range []struct {
		name string
		path string
		ct   string
		full []byte
	}{
		{"GzipPNG", "/assets/img/hero.png", "image/png", gzPng},
		{"GzipWOFF2", "/assets/font/game.woff2", "font/woff2", gzWoff2},
		{"GzipTTF", "/assets/font/ui.ttf", "font/ttf", gzTtf},
		{"GzipJS", "/assets/js/app.js", "application/javascript", gzJs},
	} {
		// Cut off the last 10 bytes (removes CRC32/ISIZE trailer and tail of deflate stream)
		truncatedSample := tc.full[:len(tc.full)-10]
		if !CanEarlyStream(tc.path, tc.ct, truncatedSample) {
			t.Fatalf("%s: expected valid prefix sample to pass CanEarlyStream", tc.name)
		}
		if !IsValidCacheSample(tc.path, tc.ct, truncatedSample, false) {
			t.Fatalf("%s: expected valid prefix sample to pass IsValidCacheSample(isComplete=false)", tc.name)
		}
		if IsValidCacheContent(tc.path, tc.ct, truncatedSample) {
			t.Fatalf("%s: expected truncated gzip stream to FAIL IsValidCacheContent", tc.name)
		}
		if IsValidCacheSample(tc.path, tc.ct, truncatedSample, true) {
			t.Fatalf("%s: expected truncated gzip stream to FAIL IsValidCacheSample(isComplete=true)", tc.name)
		}
	}

	// 4. Corrupted Gzip header, corrupted deflate stream, corrupted CRC32/ISIZE trailer, and trailing garbage must FAIL
	corruptHeader := append([]byte{0x1f, 0x8b, 0x99, 0x00, 0x00}, []byte("not_valid_deflate_stream")...)
	if IsValidCacheContent("/assets/js/app.js", "application/javascript", corruptHeader) {
		t.Fatal("expected corrupt gzip header to fail IsValidCacheContent for JS")
	}
	if CanEarlyStream("/assets/js/app.js", "application/javascript", corruptHeader) {
		t.Fatal("expected corrupt gzip header to fail CanEarlyStream for JS")
	}

	corruptCRC := append([]byte(nil), gzJs...)
	corruptCRC[len(corruptCRC)-5] ^= 0xff // flip CRC32 byte in 8-byte trailer
	if IsValidCacheContent("/assets/js/app.js", "application/javascript", corruptCRC) {
		t.Fatal("expected gzip stream with corrupted CRC32 checksum to fail IsValidCacheContent")
	}

	corruptISize := append([]byte(nil), gzPng...)
	corruptISize[len(corruptISize)-1] ^= 0xff // flip ISIZE byte in 8-byte trailer
	if IsValidCacheContent("/assets/img/hero.png", "image/png", corruptISize) {
		t.Fatal("expected gzip stream with corrupted ISIZE trailer to fail IsValidCacheContent")
	}

	trailingGarbage := append(append([]byte(nil), gzJs...), []byte("TRAILING_GARBAGE")...)
	if IsValidCacheContent("/assets/js/app.js", "application/javascript", trailingGarbage) {
		t.Fatal("expected gzip stream with trailing non-gzip garbage to fail IsValidCacheContent")
	}

	// 5. AuditAndRepairWithProgress must NOT falsely delete valid Gzip images/fonts (even when the first
	//    512 compressed bytes cannot produce magic bytes), while still deleting truncated/corrupt Gzip and HTML.
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	mgr.Save("/assets/img/valid_large_hdr.png", map[string]string{
		"content-type":     "image/png",
		"content-encoding": "gzip",
	}, gzPngLargeHdr)
	mgr.Save("/assets/font/valid_large_hdr.woff2", map[string]string{
		"content-type":     "font/woff2",
		"content-encoding": "gzip",
	}, gzWoff2LargeHdr)
	mgr.Save("/assets/font/valid_large_hdr.ttf", map[string]string{
		"content-type":     "font/ttf",
		"content-encoding": "gzip",
	}, gzTtfLargeHdr)

	// Write a truncated gzip JS and a gzipped HTML error page directly to disk to simulate disk corruption
	badTruncJSPath, ok := mgr.resolvePathWithNamespace("gbf", "assets/js/truncated_bad.js")
	if !ok {
		t.Fatal("failed to resolve badTruncJSPath")
	}
	badHtmlPngPath, ok := mgr.resolvePathWithNamespace("gbf", "assets/img/html_bad.png")
	if !ok {
		t.Fatal("failed to resolve badHtmlPngPath")
	}
	_ = writeBytesForTest(badTruncJSPath, gzJs[:len(gzJs)-6])
	_ = writeBytesForTest(badHtmlPngPath, gzHtml)

	res := mgr.AuditAndRepair()
	if got := res["healthy"].(int); got != 3 {
		t.Fatalf("expected 3 healthy gzip assets (PNG, WOFF2, TTF with >512B headers) preserved by audit, got %d (res=%v)", got, res)
	}
	if got := res["corrupted"].(int); got != 2 {
		t.Fatalf("expected 2 corrupted gzip assets (truncated JS + gzipped HTML) removed by audit, got %d (res=%v)", got, res)
	}
}

func writeBytesForTest(p string, data []byte) error {
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	return os.WriteFile(p, data, 0644)
}

