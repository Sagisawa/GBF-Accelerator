package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
)

func IsValidCacheContent(cleanPath, contentType string, data []byte) bool {
	if len(data) == 0 {
		return false
	}

	cleanLower := strings.ToLower(cleanPath)
	nonHtmlExts := []string{
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp3", ".wav", ".webm",
		".ogg", ".m4a",
		".js", ".css", ".wasm", ".woff", ".woff2", ".ttf", ".otf", ".mp4",
	}

	isAsset := false
	for _, ext := range nonHtmlExts {
		if strings.HasSuffix(cleanLower, ext) {
			isAsset = true
			break
		}
	}
	if !isAsset {
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
	if bytes.HasPrefix(sampleLower, []byte("<!doctype")) ||
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
		bytes.Contains(sampleLower, []byte("504 gateway time-out")) {
		return false
	}

	// Magic byte verification on unstripped data
	rawSample := data
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		rawSample = sample
	}

	if strings.HasSuffix(cleanLower, ".png") {
		if !bytes.HasPrefix(rawSample, []byte("\x89PNG\r\n\x1a\n")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".jpg") || strings.HasSuffix(cleanLower, ".jpeg") {
		if !bytes.HasPrefix(rawSample, []byte("\xff\xd8\xff")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".webp") {
		if len(rawSample) < 12 || !bytes.HasPrefix(rawSample, []byte("RIFF")) || !bytes.Equal(rawSample[8:12], []byte("WEBP")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".gif") {
		if !bytes.HasPrefix(rawSample, []byte("GIF87a")) && !bytes.HasPrefix(rawSample, []byte("GIF89a")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".wav") {
		if len(rawSample) < 12 || !bytes.HasPrefix(rawSample, []byte("RIFF")) || !bytes.Equal(rawSample[8:12], []byte("WAVE")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".ogg") {
		if !bytes.HasPrefix(rawSample, []byte("OggS")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".woff2") {
		if !bytes.HasPrefix(rawSample, []byte("wOF2")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".woff") {
		if !bytes.HasPrefix(rawSample, []byte("wOFF")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".wasm") {
		if !bytes.HasPrefix(rawSample, []byte("\x00asm")) {
			return false
		}
	} else if strings.HasSuffix(cleanLower, ".ttf") || strings.HasSuffix(cleanLower, ".otf") {
		if !bytes.HasPrefix(rawSample, []byte("\x00\x01\x00\x00")) &&
			!bytes.HasPrefix(rawSample, []byte("OTTO")) &&
			!bytes.HasPrefix(rawSample, []byte("true")) {
			return false
		}
	}

	return true
}
