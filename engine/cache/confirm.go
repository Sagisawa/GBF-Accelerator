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

func isKnownGBFCategory(cat string) bool {
	switch cat {
	case "js", "css", "img", "sound", "font", "manifest", "html", "misc", "cjs", "json", "model", "app":
		return true
	default:
		return false
	}
}

func isRootFallbackCategory(cat string) bool {
	switch cat {
	case "img", "sound", "js", "css", "font", "manifest", "html", "misc":
		return true
	default:
		return false
	}
}

func isValidCachePathHierarchy(segs []string) bool {
	if len(segs) == 0 {
		return false
	}
	first := segs[0]
	if first == "assets" || first == "assets_en" || first == "assets_jp" {
		if len(segs) < 2 {
			return false
		}
		if len(segs) == 2 {
			return strings.EqualFold(segs[1], "app.js")
		}
		if _, isVer := parsePositiveVersionSegment(segs[1]); isVer {
			if len(segs) < 4 {
				return false
			}
			return isKnownGBFCategory(segs[2])
		}
		return isKnownGBFCategory(segs[1])
	}
	if isRootFallbackCategory(first) {
		return len(segs) >= 2
	}
	return false
}

func isValidCacheDirHierarchy(segs []string) bool {
	if len(segs) == 0 {
		return false
	}
	first := segs[0]
	if first == "assets" || first == "assets_en" || first == "assets_jp" {
		if len(segs) == 1 {
			return true
		}
		if _, isVer := parsePositiveVersionSegment(segs[1]); isVer {
			if len(segs) == 2 {
				return true
			}
			return isKnownGBFCategory(segs[2])
		}
		return isKnownGBFCategory(segs[1])
	}
	if isRootFallbackCategory(first) {
		return true
	}
	return false
}

// isConfirmedCachePathStructure checks whether slashRel matches the canonical cache namespace and directory
// hierarchy of GBF-Accelerator (strictly following positive integer version + category, direct category fallback,
// root category fallback, or multi-host paths). It also verifies that the target file has a valid asset extension.
func isConfirmedCachePathStructure(slashRel string) (ns string, cleanKey string, ok bool) {
	if slashRel == "" || slashRel == "." || strings.HasPrefix(slashRel, "..") {
		return "", "", false
	}
	segs := splitCleanSegments(slashRel)
	if len(segs) < 2 {
		return "", "", false
	}

	if segs[0] == "hosts" {
		if len(segs) < 3 {
			return "", "", false
		}
		rawNS := segs[1]
		if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) {
			return "", "", false
		}
		nsCandidate := sanitizeNamespace(rawNS)
		if nsCandidate == "" || nsCandidate == "hosts" || nsCandidate == "gbf" || nsCandidate != strings.ToLower(rawNS) {
			return "", "", false
		}
		subSegs := segs[2:]
		subKey := strings.Join(subSegs, "/")
		if !isAssetExtension(strings.ToLower(subKey)) {
			return "", "", false
		}
		if !isValidCachePathHierarchy(subSegs) {
			return "", "", false
		}
		return nsCandidate, subKey, true
	}

	if !isAssetExtension(strings.ToLower(slashRel)) {
		return "", "", false
	}
	if !isValidCachePathHierarchy(segs) {
		return "", "", false
	}
	return "gbf", slashRel, true
}

// isConfirmedCacheDir checks whether a relative directory path belongs to the valid cache namespace
// hierarchy (e.g. hosts, hosts/<ns>, assets, assets_en, assets_jp, img, sound, or version dirs).
// Non-cache user directories (e.g. personal_data, user_workspace) return false.
func isConfirmedCacheDir(slashRelDir string) bool {
	if slashRelDir == "" || slashRelDir == "." || strings.HasPrefix(slashRelDir, "..") {
		return false
	}
	slashRelDir = strings.Trim(slashRelDir, "/")
	segs := splitCleanSegments(slashRelDir)
	if len(segs) == 0 {
		return false
	}
	if segs[0] == "hosts" {
		if len(segs) == 1 {
			return true // the top-level "hosts" directory itself
		}
		rawNS := segs[1]
		if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) {
			return false
		}
		ns := sanitizeNamespace(rawNS)
		if ns == "" || ns == "hosts" || ns == "gbf" || ns != strings.ToLower(rawNS) {
			return false
		}
		if len(segs) == 2 {
			return true // "hosts/<ns>"
		}
		return isValidCacheDirHierarchy(segs[2:])
	}
	return isValidCacheDirHierarchy(segs)
}

// IsConfirmedCacheFile determines whether a file on disk belongs to GBF-Accelerator/ACGPower cache.
// It explicitly rejects non-regular files and symbolic links using os.Lstat.
// It enforces a two-tier hierarchy:
// Tier 1 (Strong proof): A valid paired .ext file exists whose metadata (version=1, size, mtimeNano, namespace) matches.
// Tier 2 (Legacy ACGPower without .ext): The file matches canonical cache paths, asset extensions (excluding .csv),
// and magic bytes, AND the root marker (.gbf_cache) exists in baseDir.
// Unconfirmed files, user documents, system junk, and root files are rejected (returns false).
func IsConfirmedCacheFile(baseDir, fullPath string, hasRootMarker bool) bool {
	fi, err := os.Lstat(fullPath)
	if err != nil || fi == nil || !fi.Mode().IsRegular() {
		return false
	}
	return IsConfirmedCacheFileWithInfo(baseDir, fullPath, fi, hasRootMarker)
}

// IsConfirmedCacheFileWithInfo performs confirmed cache file verification using existing FileInfo.
// Only regular files with size > 0 can be confirmed; symlinks, directories, and non-regular files are rejected.
func IsConfirmedCacheFileWithInfo(baseDir, fullPath string, fi os.FileInfo, hasRootMarker bool) bool {
	if fi == nil || !fi.Mode().IsRegular() || fi.Size() <= 0 {
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

	// Strict canonical cache hierarchy check for both Tier 1 and Tier 2.
	// GBF-Accelerator never places cache files in arbitrary user paths (e.g. assets/personal_data/...).
	ns, cleanKey, ok := isConfirmedCachePathStructure(slashRel)
	if !ok {
		return false
	}

	// --- Tier 1: Strong evidence via valid paired .ext metadata file ---
	extPath := fullPath + ".ext"
	extFi, extErr := os.Lstat(extPath)
	if extErr == nil {
		// A companion .ext exists. If it exists, this file CANNOT be treated as Tier 2 legacy cache.
		// It MUST strictly validate as an authentic Tier 1 asset; if not, return false immediately.
		if !extFi.Mode().IsRegular() {
			return false
		}
		metaBytes, err := os.ReadFile(extPath)
		if err != nil {
			return false
		}
		meta, ok := parseExtFile(metaBytes)
		if !ok || meta.Version != 1 {
			return false
		}
		if meta.Size != fi.Size() || meta.MTimeNano != fi.ModTime().UnixNano() {
			return false
		}
		if meta.Namespace != "" && meta.Namespace != "gbf" {
			if sanitizeNamespace(meta.Namespace) != ns {
				return false
			}
		} else if ns != "gbf" {
			return false
		}
		return true
	} else if !os.IsNotExist(extErr) {
		// Lstat failed with an error other than NotExist (e.g. permission error).
		// Since we cannot verify whether .ext exists or not, preserve the file.
		return false
	}

	// --- Tier 2: Legacy ACGPower asset without .ext metadata file ---
	// Root marker is strictly required for un-paired legacy files
	if !hasRootMarker {
		return false
	}

	cleanLower := strings.ToLower(cleanKey)
	// Magic bytes verification
	return verifyCacheFileMagic(fullPath, cleanLower)
}

// isConfirmedCacheFileInRoot performs confirmed cache file verification inside an *os.Root.
// It verifies that rel is a regular file within valid cache paths and performs metadata/magic validation
// entirely via root-relative handles.
func isConfirmedCacheFileInRoot(root *os.Root, rel string, fi os.FileInfo, hasRootMarker bool) bool {
	if fi == nil || !fi.Mode().IsRegular() || fi.Size() <= 0 {
		return false
	}
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	slashRel := filepath.ToSlash(rel)
	baseName := filepath.Base(rel)

	if slashRel == RootMarkerFileName || config.IsSystemJunk(baseName) {
		return false
	}

	if strings.HasSuffix(slashRel, ".ext") || strings.Contains(slashRel, ".tmp.") || strings.Contains(slashRel, ".quarantine") {
		return false
	}

	// Strict canonical cache hierarchy check for both Tier 1 and Tier 2.
	// GBF-Accelerator never places cache files in arbitrary user paths (e.g. assets/personal_data/...).
	ns, cleanKey, ok := isConfirmedCachePathStructure(slashRel)
	if !ok {
		return false
	}

	// Tier 1: Check paired .ext metadata file via root
	extRel := rel + ".ext"
	extFi, extErr := root.Lstat(extRel)
	if extErr == nil {
		// A companion .ext exists. If it exists, this file CANNOT be treated as Tier 2 legacy cache.
		// It MUST strictly validate as an authentic Tier 1 asset; if not, return false immediately.
		if !extFi.Mode().IsRegular() {
			return false
		}
		metaBytes, err := root.ReadFile(extRel)
		if err != nil {
			return false
		}
		meta, ok := parseExtFile(metaBytes)
		if !ok || meta.Version != 1 {
			return false
		}
		if meta.Size != fi.Size() || meta.MTimeNano != fi.ModTime().UnixNano() {
			return false
		}
		if meta.Namespace != "" && meta.Namespace != "gbf" {
			if sanitizeNamespace(meta.Namespace) != ns {
				return false
			}
		} else if ns != "gbf" {
			return false
		}
		return true
	} else if !os.IsNotExist(extErr) {
		return false
	}

	// Tier 2: Legacy asset without .ext (requires root marker)
	if !hasRootMarker {
		return false
	}

	cleanLower := strings.ToLower(cleanKey)
	f, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	return verifyCacheFileMagicFromReader(f, cleanLower)
}

// verifyCacheFileMagicFromReader validates magic bytes and non-HTML content from an io.Reader.
func verifyCacheFileMagicFromReader(r io.Reader, cleanLower string) bool {
	var buf [512]byte
	n, err := io.ReadFull(r, buf[:])
	if (err != nil && err != io.EOF && err != io.ErrUnexpectedEOF) || n == 0 {
		return false
	}

	sample := buf[:n]
	if n >= 2 && sample[0] == 0x1f && sample[1] == 0x8b {
		return validateCompleteGzipStream(cleanLower, io.MultiReader(bytes.NewReader(sample), r))
	}

	sampleLower := bytes.TrimSpace(bytes.ToLower(sample))
	if isHTMLContent(sampleLower) {
		return false
	}

	return checkMagicBytes(cleanLower, sample)
}

// verifyCacheFileMagic reads the file header (up to 512 bytes) and validates magic bytes and non-HTML content.
func verifyCacheFileMagic(filePath, cleanLower string) bool {
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	return verifyCacheFileMagicFromReader(f, cleanLower)
}

// CanAutoWriteRootMarker returns whether the root marker (.gbf_cache) can be automatically written
// according to Rule 3:
// 1. Directory is the application's default cache directory (isDefaultDir == true); OR
// 2. Directory is clean/empty (only system junk allowed); OR
// 3. All files in directory are confirmed cache files (allFilesConfirmed == true).
// In all other cases, automatic writing is prohibited and requires explicit user confirmation.
func CanAutoWriteRootMarker(dirPath string, isDefaultDir bool, allFilesConfirmed bool) (bool, error) {
	if isDefaultDir || allFilesConfirmed {
		return true, nil
	}
	clean, err := config.IsDirClean(dirPath)
	if err != nil {
		return false, err
	}
	return clean, nil
}

// TryAutoWriteRootMarker writes .gbf_cache to baseDir if Rule 3 conditions are satisfied.
func TryAutoWriteRootMarker(baseDir string, isDefaultDir bool, allFilesConfirmed bool) (bool, error) {
	can, err := CanAutoWriteRootMarker(baseDir, isDefaultDir, allFilesConfirmed)
	if err != nil || !can {
		return false, err
	}
	return true, EnsureRootMarker(baseDir)
}

// isAcceleratorTempFile checks whether slashRel matches the exact naming pattern of temporary files
// written during cache streaming: <assetPath>.tmp.<pid>.<ts>.<seq> or <assetPath>.ext.tmp.<pid>.<ts>.<seq>.
// The prefix must be a valid cache key structure with an asset extension in a legitimate cache directory,
// and all 3 trailing numeric components (<pid>.<ts>.<seq>) must be strictly digits.
// Arbitrary user files with ".tmp." (e.g. "draft.tmp.txt" or "personal_data/photo.png.tmp.123.456") return false.
func isAcceleratorTempFile(slashRel string) bool {
	parts := strings.Split(slashRel, ".tmp.")
	if len(parts) != 2 {
		return false
	}
	numParts := strings.Split(parts[1], ".")
	if len(numParts) != 3 {
		return false
	}
	for _, np := range numParts {
		if np == "" {
			return false
		}
		for i := 0; i < len(np); i++ {
			if np[i] < '0' || np[i] > '9' {
				return false
			}
		}
	}

	baseRel := parts[0]
	baseRel = strings.TrimSuffix(baseRel, ".ext")
	baseLower := strings.ToLower(baseRel)

	_, _, ok := isConfirmedCachePathStructure(baseLower)
	return ok
}

// isAcceleratorOrphanExtFile checks whether an .ext file belongs to the accelerator cache
// by verifying that its base name is a valid cache asset path in a legitimate cache directory,
// its metadata parses with version 1, and all authentic Accelerator metadata fields
// (verified == true, size > 0, mtime > 0, at > 0, ct != "", and matching namespace) are valid.
// Incomplete or forged JSON files lacking full metadata are rejected to prevent accidental deletion.
func isAcceleratorOrphanExtFile(slashRel string, metaBytes []byte) bool {
	if !strings.HasSuffix(slashRel, ".ext") {
		return false
	}
	baseRel := strings.TrimSuffix(slashRel, ".ext")
	baseLower := strings.ToLower(baseRel)
	ns, _, ok := isConfirmedCachePathStructure(baseLower)
	if !ok {
		return false
	}
	meta, ok := parseExtFile(metaBytes)
	if !ok || meta.Version != 1 || !meta.Verified || meta.Size <= 0 || meta.MTimeNano <= 0 || meta.AccessTime <= 0 || meta.ContentType == "" {
		return false
	}
	if meta.Namespace != "" && meta.Namespace != "gbf" {
		if sanitizeNamespace(meta.Namespace) != ns {
			return false
		}
	} else if ns != "gbf" {
		return false
	}
	return true
}
