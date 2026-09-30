package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
)

var nonHtmlExts = []string{
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp3", ".wav", ".webm",
	".ogg", ".m4a",
	".js", ".css", ".wasm", ".woff", ".woff2", ".ttf", ".otf", ".mp4",
}

func isAssetExtension(cleanLower string) bool {
	for _, ext := range nonHtmlExts {
		if strings.HasSuffix(cleanLower, ext) {
			return true
		}
	}
	return false
}

func isHTMLContent(sampleLower []byte) bool {
	return bytes.HasPrefix(sampleLower, []byte("<!doctype")) ||
		bytes.HasPrefix(sampleLower, []byte("<html")) ||
		bytes.HasPrefix(sampleLower, []byte("<head")) ||
		bytes.HasPrefix(sampleLower, []byte("<body")) ||
		bytes.HasPrefix(sampleLower, []byte("<h1")) ||
		bytes.HasPrefix(sampleLower, []byte("<title")) ||
		bytes.Contains(sampleLower, []byte("<title>502")) ||
		bytes.Contains(sampleLower, []byte("<title>503")) ||
		bytes.Contains(sampleLower, []byte("<title>504")) ||
		bytes.Contains(sampleLower, []byte("502 bad gateway")) ||
		bytes.Contains(sampleLower, []byte("503 service")) ||
		bytes.Contains(sampleLower, []byte("504 gateway time-out"))
}

func checkMagicBytes(cleanLower string, sample []byte) bool {
	if strings.HasSuffix(cleanLower, ".png") {
		if !bytes.HasPrefix(sample, []byte("\x89PNG\r\n\x1a\n")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".jpg") || strings.HasSuffix(cleanLower, ".jpeg") {
		if !bytes.HasPrefix(sample, []byte("\xff\xd8\xff")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".webp") {
		if len(sample) < 12 || !bytes.HasPrefix(sample, []byte("RIFF")) || !bytes.Equal(sample[8:12], []byte("WEBP")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".gif") {
		if !bytes.HasPrefix(sample, []byte("GIF87a")) && !bytes.HasPrefix(sample, []byte("GIF89a")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".wav") {
		if len(sample) < 12 || !bytes.HasPrefix(sample, []byte("RIFF")) || !bytes.Equal(sample[8:12], []byte("WAVE")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".ogg") {
		if !bytes.HasPrefix(sample, []byte("OggS")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".woff2") {
		if !bytes.HasPrefix(sample, []byte("wOF2")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".woff") {
		if !bytes.HasPrefix(sample, []byte("wOFF")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".wasm") {
		if !bytes.HasPrefix(sample, []byte("\x00asm")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".ttf") || strings.HasSuffix(cleanLower, ".otf") {
		if !bytes.HasPrefix(sample, []byte("\x00\x01\x00\x00")) &&
			!bytes.HasPrefix(sample, []byte("OTTO")) &&
			!bytes.HasPrefix(sample, []byte("true")) {
			return false
		}
	}
	return true
}

func IsValidCacheContent(cleanPath, contentType string, data []byte) bool {
	if len(data) == 0 {
		return false
	}

	cleanLower := strings.ToLower(cleanPath)
	if !isAssetExtension(cleanLower) {
		return true
	}

	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return false
	}

	sample := data
	if len(data) > 512 {
		sample = data[:512]
	}

	// If gzip compressed, decompress sample
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err == nil {
			buf := make([]byte, 512)
			n, _ := io.ReadFull(gr, buf)
			_ = gr.Close()
			if n > 0 {
				sample = buf[:n]
			}
		}
	}

	sampleLower := bytes.TrimSpace(bytes.ToLower(sample))
	if isHTMLContent(sampleLower) {
		return false
	}

	// Magic byte verification on unstripped data
	rawSample := data
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		rawSample = sample
	}

	return checkMagicBytes(cleanLower, rawSample)
}

// CanEarlyStream performs fast-rejection check on the first chunk of an asset stream.
// It verifies that the chunk does not contain HTML error pages and matches expected magic bytes.
// If the payload is gzip-compressed and cannot be conclusively validated from this chunk alone,
// it returns false so the proxy can fall back to the conservative batch read path.
func CanEarlyStream(cleanPath, contentType string, chunk []byte) bool {
	if len(chunk) == 0 {
		return false
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return false
	}

	cleanLower := strings.ToLower(cleanPath)
	if !isAssetExtension(cleanLower) {
		return true
	}

	// If gzip compressed, attempt to decompress a sample
	if len(chunk) >= 2 && chunk[0] == 0x1f && chunk[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(chunk))
		if err != nil {
			return false
		}
		buf := make([]byte, 512)
		n, err := io.ReadFull(gr, buf)
		_ = gr.Close()
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return false
		}
		if n == 0 {
			return false
		}
		decomp := buf[:n]
		decompLower := bytes.TrimSpace(bytes.ToLower(decomp))
		if isHTMLContent(decompLower) {
			return false
		}
		return checkMagicBytes(cleanLower, decomp)
	}

	chunkLower := bytes.TrimSpace(bytes.ToLower(chunk))
	if isHTMLContent(chunkLower) {
		return false
	}

	return checkMagicBytes(cleanLower, chunk)
}
