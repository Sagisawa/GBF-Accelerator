package cache

import (
	"bytes"
	"compress/gzip"
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
}
