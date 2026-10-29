package cache

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gbf-proxy/config"
)

// RootMarkerFileName is the filename placed at the root of a validated cache directory.
const RootMarkerFileName = ".gbf_cache"

// HasRootMarker checks whether the root marker file exists in baseDir.
func HasRootMarker(baseDir string) bool {
	if baseDir == "" {
		return false
	}
	markerPath := filepath.Join(baseDir, RootMarkerFileName)
	fi, err := os.Stat(markerPath)
	return err == nil && !fi.IsDir()
}

// EnsureRootMarker writes the root marker file to baseDir if not already present.
func EnsureRootMarker(baseDir string) error {
	if baseDir == "" {
		return os.ErrInvalid
	}
	markerPath := filepath.Join(baseDir, RootMarkerFileName)
	if fi, err := os.Stat(markerPath); err == nil && !fi.IsDir() {
		return nil
	}
	return os.WriteFile(markerPath, []byte("GBF-Accelerator Cache Root\n"), 0644)
}

// HasRootMarker checks whether the current cache root has the root marker.
func (m *Manager) HasRootMarker() bool {
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	return HasRootMarker(base)
}

// EnsureRootMarker writes the root marker to the current cache root.
func (m *Manager) EnsureRootMarker() error {
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	return EnsureRootMarker(base)
}

// IsConfirmedCacheFile determines whether a file on disk belongs to GBF-Accelerator/ACGPower cache.
// It enforces a two-tier hierarchy:
// Tier 1 (Strong proof): A valid paired .ext file exists whose metadata (version=1, size, mtimeNano, namespace) matches.
// Tier 2 (Legacy ACGPower without .ext): The file matches canonical cache paths, asset extensions (excluding .csv),
// and magic bytes, AND the root marker (.gbf_cache) exists in baseDir.
// Unconfirmed files, user documents, system junk, and root files are rejected (returns false).
func IsConfirmedCacheFile(baseDir, fullPath string, hasRootMarker bool) bool {
	fi, err := os.Stat(fullPath)
	if err != nil {
		return false
	}
	return IsConfirmedCacheFileWithInfo(baseDir, fullPath, fi, hasRootMarker)
}

// IsConfirmedCacheFileWithInfo performs confirmed cache file verification using existing FileInfo.
func IsConfirmedCacheFileWithInfo(baseDir, fullPath string, fi os.FileInfo, hasRootMarker bool) bool {
	if fi == nil || fi.IsDir() || fi.Size() <= 0 {
		return false
	}

	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}

	slashRel := filepath.ToSlash(rel)
	baseName := filepath.Base(fullPath)

	// Never confirm the root marker file itself or system junk files
	if slashRel == RootMarkerFileName || config.IsSystemJunk(baseName) {
		return false
	}

	// Metadata files (.ext), temp files, and quarantine files are not asset data files
	if strings.HasSuffix(slashRel, ".ext") || strings.Contains(slashRel, ".tmp.") || strings.Contains(slashRel, ".quarantine") {
		return false
	}

	// Resolve namespace and clean key using canonical rules
	ns, cleanKey, _, ok := resolveDiskFileOwner(baseDir, fullPath)
	if !ok {
		return false
	}

	// Cache assets must reside in subdirectories, never directly in the cache root
	segs := splitCleanSegments(cleanKey)
	if len(segs) < 2 {
		return false
	}

	cleanLower := strings.ToLower(cleanKey)
	// Must match asset extensions (nonHtmlExts; .csv is strictly excluded)
	if !isAssetExtension(cleanLower) {
		return false
	}

	// --- Tier 1: Strong evidence via valid paired .ext metadata file ---
	extPath := fullPath + ".ext"
	if metaBytes, err := os.ReadFile(extPath); err == nil {
		if meta, ok := parseExtFile(metaBytes); ok && meta.Version == 1 {
			if meta.Size == fi.Size() && meta.MTimeNano == fi.ModTime().UnixNano() {
				if meta.Namespace == "" || sanitizeNamespace(meta.Namespace) == ns {
					return true
				}
			}
		}
	}

	// --- Tier 2: Legacy ACGPower asset without .ext metadata file ---
	// Root marker is strictly required for un-paired legacy files
	if !hasRootMarker {
		return false
	}

	// Path structure check for legacy GBF / external host assets
	if ns == "gbf" {
		first := segs[0]
		if first != "assets" && first != "assets_en" && first != "assets_jp" && first != "img" && first != "sound" {
			// Check if first segment is a positive version number (e.g. 300/js/app.js)
			if _, ok := parsePositiveVersionSegment(first); !ok {
				return false
			}
		}
	}

	// Magic bytes verification
	return verifyCacheFileMagic(fullPath, cleanLower)
}

// verifyCacheFileMagic reads the file header (up to 512 bytes) and validates magic bytes and non-HTML content.
func verifyCacheFileMagic(filePath, cleanLower string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()

	var buf [512]byte
	n, err := io.ReadFull(f, buf[:])
	if (err != nil && err != io.EOF && err != io.ErrUnexpectedEOF) || n == 0 {
		return false
	}

	sample := buf[:n]
	if n >= 2 && sample[0] == 0x1f && sample[1] == 0x8b {
		return validateCompleteGzipStream(cleanLower, io.MultiReader(bytes.NewReader(sample), f))
	}

	sampleLower := bytes.TrimSpace(bytes.ToLower(sample))
	if isHTMLContent(sampleLower) {
		return false
	}

	return checkMagicBytes(cleanLower, sample)
}
