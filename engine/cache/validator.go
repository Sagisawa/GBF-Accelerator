package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
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

func isImageExtension(cleanLower string) bool {
	return strings.HasSuffix(cleanLower, ".png") ||
		strings.HasSuffix(cleanLower, ".jpg") ||
		strings.HasSuffix(cleanLower, ".jpeg") ||
		strings.HasSuffix(cleanLower, ".webp") ||
		strings.HasSuffix(cleanLower, ".gif")
}

func isImageMagic(sample []byte) bool {
	if bytes.HasPrefix(sample, []byte("\x89PNG\r\n\x1a\n")) {
		return true
	}
	if bytes.HasPrefix(sample, []byte("\xff\xd8\xff")) {
		return true
	}
	if len(sample) >= 12 && bytes.HasPrefix(sample, []byte("RIFF")) && bytes.Equal(sample[8:12], []byte("WEBP")) {
		return true
	}
	if bytes.HasPrefix(sample, []byte("GIF87a")) || bytes.HasPrefix(sample, []byte("GIF89a")) {
		return true
	}
	return false
}

func checkMagicBytes(cleanLower string, sample []byte) bool {
	if isImageExtension(cleanLower) {
		if strings.HasSuffix(cleanLower, ".png") && bytes.HasPrefix(sample, []byte("\x89PNG\r\n\x1a\n")) {
			return true
		}
		if (strings.HasSuffix(cleanLower, ".jpg") || strings.HasSuffix(cleanLower, ".jpeg")) && bytes.HasPrefix(sample, []byte("\xff\xd8\xff")) {
			return true
		}
		if strings.HasSuffix(cleanLower, ".webp") && len(sample) >= 12 && bytes.HasPrefix(sample, []byte("RIFF")) && bytes.Equal(sample[8:12], []byte("WEBP")) {
			return true
		}
		if strings.HasSuffix(cleanLower, ".gif") && (bytes.HasPrefix(sample, []byte("GIF87a")) || bytes.HasPrefix(sample, []byte("GIF89a"))) {
			return true
		}
		// If the extension does not match the exact format (e.g. Cygames upstream files having .png
		// extension but actually encoded as JPEG/JFIF or WebP), accept any valid image magic bytes.
		return isImageMagic(sample)
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

func readGzipSample(r io.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func validateCompleteGzipStream(cleanLower string, r io.Reader) bool {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return false
	}
	var buf [512]byte
	n, readErr := readGzipSample(gr, buf[:])
	if readErr != nil && readErr != io.EOF {
		_ = gr.Close()
		return false
	}
	if n == 0 {
		_ = gr.Close()
		return false
	}
	if isAssetExtension(cleanLower) {
		sample := buf[:n]
		sampleLower := bytes.TrimSpace(bytes.ToLower(sample))
		if isHTMLContent(sampleLower) {
			_ = gr.Close()
			return false
		}
		if !checkMagicBytes(cleanLower, sample) {
			_ = gr.Close()
			return false
		}
	}
	if _, copyErr := io.Copy(io.Discard, gr); copyErr != nil {
		_ = gr.Close()
		return false
	}
	if closeErr := gr.Close(); closeErr != nil {
		return false
	}
	return true
}

// IsValidCacheContent validates a complete asset payload in memory.
// For gzip-compressed data, it verifies decompression, non-truncation, CRC32/ISIZE checksums,
// HTML rejection, and inner magic bytes.
func IsValidCacheContent(cleanPath, contentType string, data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return false
	}

	cleanLower := strings.ToLower(cleanPath)

	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		return validateCompleteGzipStream(cleanLower, bytes.NewReader(data))
	}

	if !isAssetExtension(cleanLower) {
		return true
	}

	sample := data
	if len(data) > 512 {
		sample = data[:512]
	}

	sampleLower := bytes.TrimSpace(bytes.ToLower(sample))
	if isHTMLContent(sampleLower) {
		return false
	}

	return checkMagicBytes(cleanLower, sample)
}

// IsValidCacheSample validates either a complete payload (isComplete=true) or a streaming
// prefix sample (isComplete=false) that may be lawfully truncated at a chunk boundary.
func IsValidCacheSample(cleanPath, contentType string, sample []byte, isComplete bool) bool {
	if isComplete {
		return IsValidCacheContent(cleanPath, contentType, sample)
	}
	if len(sample) == 0 {
		return false
	}
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return false
	}

	cleanLower := strings.ToLower(cleanPath)
	if !isAssetExtension(cleanLower) {
		if len(sample) >= 2 && sample[0] == 0x1f && sample[1] == 0x8b {
			gr, err := gzip.NewReader(bytes.NewReader(sample))
			if err != nil {
				return false
			}
			var buf [512]byte
			n, readErr := readGzipSample(gr, buf[:])
			_ = gr.Close()
			if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
				return false
			}
			return n > 0
		}
		return true
	}

	// If gzip compressed, attempt to decompress a prefix sample
	if len(sample) >= 2 && sample[0] == 0x1f && sample[1] == 0x8b {
		gr, err := gzip.NewReader(bytes.NewReader(sample))
		if err != nil {
			return false
		}
		var buf [512]byte
		n, readErr := readGzipSample(gr, buf[:])
		_ = gr.Close()
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
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
		if readErr == io.ErrUnexpectedEOF && len(decompLower) > 0 && decompLower[0] == '<' && n < 64 {
			return false
		}
		return checkMagicBytes(cleanLower, decomp)
	}

	chunk := sample
	if len(sample) > 512 {
		chunk = sample[:512]
	}
	chunkLower := bytes.TrimSpace(bytes.ToLower(chunk))
	if isHTMLContent(chunkLower) {
		return false
	}

	return checkMagicBytes(cleanLower, chunk)
}

// IsValidCacheFile validates a cached asset file on disk.
// For uncompressed files it reads only the first 512 bytes; for gzip-compressed files
// it streams the file through gzip.Reader so large headers/dictionaries do not cause
// false-positive magic-byte rejections while truncation and checksum errors are caught.
func IsValidCacheFile(cleanPath, contentType, filePath string) bool {
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		return false
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	fi, sErr := f.Stat()
	if sErr != nil || fi.Size() <= 0 {
		return false
	}

	var header [512]byte
	n, rErr := io.ReadFull(f, header[:])
	if (rErr != nil && rErr != io.EOF && rErr != io.ErrUnexpectedEOF) || n == 0 {
		return false
	}

	cleanLower := strings.ToLower(cleanPath)
	if n >= 2 && header[0] == 0x1f && header[1] == 0x8b {
		return validateCompleteGzipStream(cleanLower, io.MultiReader(bytes.NewReader(header[:n]), f))
	}

	if !isAssetExtension(cleanLower) {
		return true
	}

	sample := header[:n]
	sampleLower := bytes.TrimSpace(bytes.ToLower(sample))
	if isHTMLContent(sampleLower) {
		return false
	}
	return checkMagicBytes(cleanLower, sample)
}

// CanEarlyStream performs fast-rejection check on the first chunk of an asset stream.
// It verifies that the chunk does not contain HTML error pages and matches expected magic bytes.
// If the payload is gzip-compressed and cannot be conclusively validated from this chunk alone,
// it returns false so the proxy can fall back to the conservative batch read path.
func CanEarlyStream(cleanPath, contentType string, chunk []byte) bool {
	return IsValidCacheSample(cleanPath, contentType, chunk, false)
}
