package cache

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gbf-proxy/config"
)

// RootMarkerFileName is the filename placed at the root of a validated cache directory.
const (
	RootMarkerFileName        = ".gbf_cache"
	RootMarkerExpectedContent = "GBF-Accelerator Cache Root\n"
	RootMarkerExpectedText    = "GBF-Accelerator Cache Root"
)

// verifyRootMarkerContent validates that the root marker content matches expected text.
// Note: Root marker content verification is a hygiene gate against accidental deletion,
// not an unfalsifiable cryptographic proof of origin.
func verifyRootMarkerContent(data []byte) bool {
	return bytes.Equal(bytes.TrimSpace(data), []byte(RootMarkerExpectedText))
}

// HasRootMarker checks whether the root marker file exists in baseDir as a regular file
// with the expected marker content. Symlinks, directories, empty files, or corrupted content return false.
func HasRootMarker(baseDir string) bool {
	if baseDir == "" {
		return false
	}
	markerPath := filepath.Join(baseDir, RootMarkerFileName)
	fi, err := os.Lstat(markerPath)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > 256 {
		return false
	}
	content, err := os.ReadFile(markerPath)
	if err != nil {
		return false
	}
	return verifyRootMarkerContent(content)
}

// hasRootMarkerInRoot checks whether the root marker file exists inside root as a regular file
// with the expected marker content, using root-relative handles to prevent symlink traversal.
func hasRootMarkerInRoot(root *os.Root) bool {
	if root == nil {
		return false
	}
	fi, err := root.Lstat(RootMarkerFileName)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > 256 {
		return false
	}
	content, err := root.ReadFile(RootMarkerFileName)
	if err != nil {
		return false
	}
	return verifyRootMarkerContent(content)
}

// EnsureRootMarker writes the root marker file to baseDir if not already present with valid content.
// To avoid data loss, it strictly refuses to overwrite existing user files, directories, symlinks,
// or unrecognized files named .gbf_cache.
func EnsureRootMarker(baseDir string) error {
	if baseDir == "" {
		return os.ErrInvalid
	}
	if HasRootMarker(baseDir) {
		return nil
	}
	markerPath := filepath.Join(baseDir, RootMarkerFileName)
	fi, err := os.Lstat(markerPath)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("root marker path is a symlink")
		}
		if fi.IsDir() {
			return errors.New("root marker path is a directory")
		}
		return errors.New("existing .gbf_cache file contains unrecognized content; refusing to overwrite")
	} else if !os.IsNotExist(err) {
		return err
	}

	f, err := os.OpenFile(markerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write([]byte(RootMarkerExpectedContent))
	return err
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

// ValidateDestructiveCacheDir rejects roots that are not safe targets for disk maintenance.
func (m *Manager) ValidateDestructiveCacheDir() error {
	if m == nil {
		return errors.New("cache manager is unavailable")
	}
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	if err := config.ValidateCacheDir(base); err != nil {
		return err
	}
	fi, err := os.Lstat(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return errors.New("cache root must be an ordinary directory, not a symlink or file")
	}
	return nil
}

// isKnownGBFCategory checks whether cat matches any top-level GBF static asset category.
func isKnownGBFCategory(cat string) bool {
	switch cat {
	case "js", "css", "img", "sound", "font", "manifest", "html", "misc", "cjs", "json", "model", "app", "jssdk":
		return true
	default:
		return false
	}
}

// isHistoricalEmptyNSHost checks whether host belongs to the strictly confirmed GBF official domains
// that historically generated cache sidecars without the "ns" field.
func isHistoricalEmptyNSHost(host string) bool {
	switch strings.ToLower(host) {
	case "granbluefantasy.com",
		"game.granbluefantasy.jp",
		"ava-a.sp.mbga.jp",
		"connect.mobage.jp",
		"steam.granbluefantasy.com":
		return true
	default:
		return false
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isPositiveVersionSegment(s string) bool {
	if s == "" || len(s) > 19 {
		return false
	}
	if !isAllDigits(s) {
		return false
	}
	if len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '0' {
			return true
		}
	}
	return false
}

// isTier1VersionFallbackPath checks whether slashRel matches the historical GBF version fallback
// directory structure without the leading "assets/" prefix (e.g. "<ver>/<category>/...").
func isTier1VersionFallbackPath(slashRel string) bool {
	segs := splitCleanSegments(filepath.ToSlash(slashRel))
	if len(segs) < 3 {
		return false
	}
	if !isPositiveVersionSegment(segs[0]) {
		return false
	}
	return isKnownGBFCategory(segs[1])
}

// parseCachePathTopLevelShape validates whether slashRel conforms to a recognized cache top-level shape:
// - assets/, assets_en/, assets_jp/ (>= 2 segments)
// - hosts/<ns>/... (>= 3 segments, with valid sanitized <ns>)
// - positive integer version directory (e.g. 300/...) (>= 2 segments)
// - top-level category directory (e.g. js/, img/, sound/, ...) (>= 2 segments)
//
// Deep directory syntax (subfolder whitelists) is deliberately not checked here because
// write-side cache keys have no deep grammar restrictions and game CDN paths evolve over time.
func parseCachePathTopLevelShape(slashRel string) (ns string, cleanKey string, isHosts bool, ok bool) {
	if slashRel == "" || slashRel == "." || strings.HasPrefix(slashRel, "..") {
		return "", "", false, false
	}
	clean := filepath.ToSlash(filepath.Clean(slashRel))
	clean = strings.TrimPrefix(clean, "/")
	segs := splitCleanSegments(clean)
	if len(segs) < 2 {
		if len(segs) == 1 && strings.EqualFold(segs[0], "favicon.ico") {
			return "gbf", "favicon.ico", false, true
		}
		return "", "", false, false
	}

	first := segs[0]
	if first == "hosts" {
		if len(segs) < 3 {
			return "", "", false, false
		}
		rawNS := segs[1]
		if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) {
			return "", "", false, false
		}
		nsCandidate := sanitizeNamespace(rawNS)
		if nsCandidate == "" || nsCandidate == "hosts" || nsCandidate == "gbf" || nsCandidate != strings.ToLower(rawNS) {
			return "", "", false, false
		}
		subKey := strings.Join(segs[2:], "/")
		return nsCandidate, subKey, true, true
	}

	if first == "assets" || first == "assets_en" || first == "assets_jp" {
		return "gbf", clean, false, true
	}

	if isPositiveVersionSegment(first) {
		// Version fallback path: requires >= 3 segments and segs[1] must be a known category, e.g. 300/js/app.js
		if len(segs) < 3 || !isKnownGBFCategory(segs[1]) {
			return "", "", false, false
		}
		return "gbf", clean, false, true
	}

	if isKnownGBFCategory(first) {
		return "gbf", clean, false, true
	}

	return "", "", false, false
}

// isConfirmedCachePathStructure checks whether slashRel matches the canonical cache namespace and top-level
// shape and possesses a valid asset extension. Retained for backward-compatible call sites.
func isConfirmedCachePathStructure(slashRel string) (ns string, cleanKey string, ok bool) {
	nsCand, keyCand, _, shapeOK := parseCachePathTopLevelShape(slashRel)
	if !shapeOK {
		return "", "", false
	}
	if !isAssetExtension(strings.ToLower(keyCand)) {
		return "", "", false
	}
	return nsCand, keyCand, true
}

// IsConfirmedCacheDirectory reports whether a relative directory belongs to a valid cache top-level shape.
func IsConfirmedCacheDirectory(slashRelDir string) bool {
	return isConfirmedCacheDir(slashRelDir)
}

// isConfirmedCacheDir checks whether a relative directory path belongs to the valid cache namespace
// hierarchy (hosts, hosts/<ns>, assets, assets_en, assets_jp, version dirs, or top-level category dirs).
func isConfirmedCacheDir(slashRelDir string) bool {
	if slashRelDir == "" || slashRelDir == "." || strings.HasPrefix(slashRelDir, "..") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(slashRelDir))
	clean = strings.Trim(clean, "/")
	segs := splitCleanSegments(clean)
	if len(segs) == 0 {
		return false
	}
	first := segs[0]
	if first == "hosts" {
		if len(segs) == 1 {
			return true // "hosts" itself
		}
		rawNS := segs[1]
		if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) {
			return false
		}
		ns := sanitizeNamespace(rawNS)
		if ns == "" || ns == "hosts" || ns == "gbf" || ns != strings.ToLower(rawNS) {
			return false
		}
		// hosts/<ns> or any subdirectory under hosts/<ns>
		return true
	}
	if first == "assets" || first == "assets_en" || first == "assets_jp" {
		return true
	}
	if isPositiveVersionSegment(first) {
		if len(segs) == 1 {
			return true // "300"
		}
		return isKnownGBFCategory(segs[1]) // "300/js", but "2026/photos" -> false
	}
	if isKnownGBFCategory(first) {
		return true
	}
	return false
}

// VerifyTier1Meta is the canonical, unified single source of truth for Tier-1 ownership validation.
// It verifies:
// 1. Path top-level shape is valid (assets/assets_en/assets_jp, hosts/<ns>, version dir, or category dir).
// 2. parseExtFile succeeds and version == 1.
// 3. Namespace in metadata matches path namespace:
//    - hosts/<ns> requires metadata namespace matching <ns> (strictly non-empty and non-gbf).
//    - all other shapes require metadata namespace to be "gbf" or empty.
//
// In accordance with specification:
// - No deep directory syntax check is performed.
// - No asset extension whitelist check is performed (Tier-1 trusts the verified metadata record).
// - mtimeNano and size equality is NOT required for attribution (mismatches require content re-verification
//   by the audit engine, but do not revoke cache ownership).
func VerifyTier1Meta(slashRel string, metaBytes []byte) (diskMeta, bool) {
	if slashRel == "" || len(metaBytes) == 0 {
		return diskMeta{}, false
	}
	expectedNS, _, isHosts, shapeOK := parseCachePathTopLevelShape(slashRel)
	if !shapeOK {
		return diskMeta{}, false
	}
	parsedMeta, parseOK := parseExtFile(metaBytes)
	if !parseOK || parsedMeta.Version != 1 {
		return diskMeta{}, false
	}

	clean := filepath.ToSlash(slashRel)
	clean = strings.TrimPrefix(clean, "/")
	segs := splitCleanSegments(clean)
	isVerFallback := len(segs) >= 1 && isPositiveVersionSegment(segs[0])

	if isHosts {
		// For hosts paths (hosts/<host>/...):
		// - Form A: meta.Namespace == "gbf" is accepted.
		// - Form B: sanitizeNamespace(meta.Namespace) == expectedNS is accepted.
		// - Form C (Historical Compatibility): meta.Namespace == "" is accepted ONLY for strictly confirmed
		//   official GBF hosts and requires parsedMeta.Verified == true.
		// - All other hosts with empty or mismatched namespace are rejected.
		if parsedMeta.Namespace == "gbf" {
			return parsedMeta, true
		}
		if sanitizeNamespace(parsedMeta.Namespace) == expectedNS {
			return parsedMeta, true
		}
		if parsedMeta.Namespace == "" && parsedMeta.Verified && isHistoricalEmptyNSHost(expectedNS) {
			return parsedMeta, true
		}
		return diskMeta{}, false
	} else if isVerFallback {
		// Bare version fallback paths (e.g. 300/js/app.js) strictly require "gbf" namespace.
		if parsedMeta.Namespace != "gbf" {
			return diskMeta{}, false
		}
	} else {
		// Standard GBF paths (assets/..., assets_en/..., assets_jp/..., img/..., sound/..., etc.):
		// Namespace must be "gbf" or empty (for legacy backward compatibility).
		if parsedMeta.Namespace != "" && parsedMeta.Namespace != "gbf" && sanitizeNamespace(parsedMeta.Namespace) != "gbf" {
			return diskMeta{}, false
		}
	}

	return parsedMeta, true
}

// isTier2Candidate checks whether slashRel meets structural requirements for Tier-2 legacy cache:
// root marker present + top-level shape valid + valid asset file extension.
func isTier2Candidate(slashRel string, hasRootMarker bool) (cleanKey string, ok bool) {
	if !hasRootMarker {
		return "", false
	}
	if strings.EqualFold(filepath.ToSlash(filepath.Clean(slashRel)), "favicon.ico") {
		// Root-level favicon.ico is strictly Tier-1 only and requires a valid paired .ext
		return "", false
	}
	_, keyCand, _, shapeOK := parseCachePathTopLevelShape(slashRel)
	if !shapeOK {
		return "", false
	}
	cleanLower := strings.ToLower(keyCand)
	if !isAssetExtension(cleanLower) {
		return "", false
	}
	return cleanLower, true
}

// IsTier2Candidate checks whether slashRel meets structural requirements for Tier-2 legacy cache:
// root marker present + top-level shape valid + valid asset file extension.
func IsTier2Candidate(slashRel string, hasRootMarker bool) (cleanKey string, ok bool) {
	return isTier2Candidate(slashRel, hasRootMarker)
}

// isUnverifiedLegacyCacheCandidateInRoot identifies a structurally plausible legacy cache path
// that has no sidecar ownership record. It is diagnostic-only: callers must never delete it.
func isUnverifiedLegacyCacheCandidateInRoot(root *os.Root, rel string, fi os.FileInfo, hasRootMarker bool) bool {
	if root == nil || !hasRootMarker || fi == nil || !fi.Mode().IsRegular() {
		return false
	}
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	slashRel := filepath.ToSlash(rel)
	baseName := filepath.Base(rel)
	if slashRel == RootMarkerFileName || config.IsSystemJunk(baseName) ||
		strings.HasSuffix(slashRel, ".ext") || strings.Contains(slashRel, ".tmp.") ||
		isAcceleratorQuarantineFile(slashRel) {
		return false
	}
	if _, ok := isTier2Candidate(slashRel, hasRootMarker); !ok {
		return false
	}
	_, err := root.Lstat(rel + ".ext")
	return os.IsNotExist(err)
}

// IsConfirmedCacheFile determines whether a file on disk belongs to GBF-Accelerator/ACGPower cache.
// It explicitly rejects non-regular files and symbolic links using os.Lstat.
// Two-tier hierarchy:
// Tier 1 (Strong proof): A valid paired .ext file exists whose metadata (version=1, namespace) matches.
// Tier 2 (Legacy ACGPower without .ext): Root marker exists + valid top-level shape + asset extension + magic bytes.
func IsConfirmedCacheFile(baseDir, fullPath string, hasRootMarker bool) bool {
	fi, err := os.Lstat(fullPath)
	if err != nil || fi == nil || !fi.Mode().IsRegular() {
		return false
	}
	return IsConfirmedCacheFileWithInfo(baseDir, fullPath, fi, hasRootMarker)
}

// IsConfirmedCacheFileWithInfo performs confirmed cache file verification using existing FileInfo.
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

	if slashRel == RootMarkerFileName || config.IsSystemJunk(baseName) {
		return false
	}

	if strings.HasSuffix(slashRel, ".ext") || strings.Contains(slashRel, ".tmp.") || isAcceleratorQuarantineFile(slashRel) {
		return false
	}

	// --- Tier 1: Strong evidence via valid paired .ext metadata file ---
	extPath := fullPath + ".ext"
	extFi, extErr := os.Lstat(extPath)
	if extErr == nil {
		if !extFi.Mode().IsRegular() {
			return false
		}
		metaBytes, err := os.ReadFile(extPath)
		if err != nil {
			return false
		}
		_, ok := VerifyTier1Meta(slashRel, metaBytes)
		return ok
	} else if !os.IsNotExist(extErr) {
		return false
	}

	// --- Tier 2: Legacy ACGPower asset without .ext metadata file ---
	cleanLower, ok := isTier2Candidate(slashRel, hasRootMarker)
	if !ok {
		return false
	}

	return verifyCacheFileMagic(fullPath, cleanLower)
}

// isConfirmedCacheFileInRoot performs confirmed cache file verification inside an *os.Root.
func isConfirmedCacheFileInRoot(root *os.Root, rel string, fi os.FileInfo, hasRootMarker bool) bool {
	confirmed, _ := isConfirmedCacheFileInRootDetailed(root, rel, fi, hasRootMarker)
	return confirmed
}

// isConfirmedCacheFileInRootDetailed performs verified cache attribution inside an *os.Root,
// returning whether the file is confirmed, and whether it was confirmed via Tier 1 paired metadata.
func isConfirmedCacheFileInRootDetailed(root *os.Root, rel string, fi os.FileInfo, hasRootMarker bool) (confirmed bool, isTier1 bool) {
	if fi == nil || !fi.Mode().IsRegular() || fi.Size() <= 0 {
		return false, false
	}
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return false, false
	}
	slashRel := filepath.ToSlash(rel)
	baseName := filepath.Base(rel)

	if slashRel == RootMarkerFileName || config.IsSystemJunk(baseName) {
		return false, false
	}

	if strings.HasSuffix(slashRel, ".ext") || strings.Contains(slashRel, ".tmp.") || isAcceleratorQuarantineFile(slashRel) {
		return false, false
	}

	// Tier 1: Check paired .ext metadata file via root
	extRel := rel + ".ext"
	extFi, extErr := root.Lstat(extRel)
	if extErr == nil {
		if !extFi.Mode().IsRegular() {
			return false, false
		}
		metaBytes, err := root.ReadFile(extRel)
		if err != nil {
			return false, false
		}
		_, ok := VerifyTier1Meta(slashRel, metaBytes)
		if ok {
			return true, true
		}
		return false, false
	} else if !os.IsNotExist(extErr) {
		return false, false
	}

	// Tier 2: Legacy asset without .ext (requires root marker)
	cleanLower, ok := isTier2Candidate(slashRel, hasRootMarker)
	if !ok {
		return false, false
	}

	f, err := root.Open(rel)
	if err != nil {
		return false, false
	}
	defer f.Close()
	if verifyCacheFileMagicFromReader(f, cleanLower) {
		return true, false
	}
	return false, false
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

// AreAllFilesConfirmed walks baseDir and checks whether every regular file belongs to confirmed
// cache assets, companion/orphan .ext metadata, temporary streaming files, quarantine files,
// or system junk.
// Crucially, it evaluates attribution assuming the root marker exists (hasRootMarker = true),
// avoiding circular dependency when validating a legacy ACGPower cache directory before the marker is written.
func AreAllFilesConfirmed(baseDir string) (bool, error) {
	clean, err := config.IsDirClean(baseDir)
	if err != nil {
		return false, err
	}
	if clean {
		return true, nil
	}

	allConfirmed := true
	walkErr := filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == baseDir {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			allConfirmed = false
			return err
		}
		slashRel := filepath.ToSlash(rel)
		baseName := entry.Name()

		if slashRel == RootMarkerFileName || config.IsSystemJunk(baseName) {
			return nil
		}
		if isAcceleratorTempFile(slashRel) || isAcceleratorQuarantineFile(slashRel) {
			return nil
		}
		if strings.HasSuffix(slashRel, ".ext") {
			// Companion or orphan metadata file with valid asset top-level shape
			assetSlashRel := strings.TrimSuffix(slashRel, ".ext")
			_, _, _, shapeOK := parseCachePathTopLevelShape(assetSlashRel)
			if !shapeOK {
				allConfirmed = false
				return filepath.SkipAll
			}
			return nil
		}

		fi, err := entry.Info()
		if err != nil || !fi.Mode().IsRegular() {
			allConfirmed = false
			return filepath.SkipAll
		}

		// Evaluate with hasRootMarker = true to prevent circular dependency
		if !IsConfirmedCacheFileWithInfo(baseDir, path, fi, true) {
			allConfirmed = false
			return filepath.SkipAll
		}
		return nil
	})

	if walkErr != nil && walkErr != filepath.SkipAll {
		return false, walkErr
	}
	return allConfirmed, nil
}

// IsAcceleratorTempFile checks whether slashRel matches the naming pattern of temporary cache files.
func IsAcceleratorTempFile(slashRel string) bool {
	return isAcceleratorTempFile(slashRel)
}

// IsAcceleratorQuarantineFile checks whether slashRel matches the naming pattern of quarantined cache files.
func IsAcceleratorQuarantineFile(slashRel string) bool {
	return isAcceleratorQuarantineFile(slashRel)
}

// isAcceleratorTempFile checks whether slashRel matches the exact naming pattern of temporary files
// written during cache streaming: <assetPath>.tmp.<pid>.<ts>.<seq> or <assetPath>.ext.tmp.<pid>.<ts>.<seq>.
// The prefix must conform to a recognized cache top-level shape, and all 3 trailing components must be strictly digits.
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
		if !isAllDigits(np) {
			return false
		}
	}

	baseRel := parts[0]
	baseRel = strings.TrimSuffix(baseRel, ".ext")
	baseLower := strings.ToLower(baseRel)

	_, cleanKey, _, ok := parseCachePathTopLevelShape(baseLower)
	if !ok {
		return false
	}
	return isAssetExtension(strings.ToLower(cleanKey))
}

// isAcceleratorQuarantineFile checks whether slashRel matches the exact naming pattern of quarantined
// files: <assetPath>.quarantine.<ts> or <assetPath>.ext.quarantine.<ts>.
// The base asset path must conform to the cache top-level shape with a valid asset extension, and <ts> must be all digits.
// User files (such as x.quarantine.docx) return false and are preserved.
func isAcceleratorQuarantineFile(slashRel string) bool {
	slash := filepath.ToSlash(slashRel)
	idx := strings.LastIndex(slash, ".quarantine.")
	if idx < 0 {
		return false
	}
	ts := slash[idx+len(".quarantine."):]
	if !isAllDigits(ts) {
		return false
	}
	basePath := slash[:idx]
	basePath = strings.TrimSuffix(basePath, ".ext")
	baseLower := strings.ToLower(basePath)

	if !isAssetExtension(baseLower) {
		return false
	}

	if !filepath.IsAbs(slash) && !strings.Contains(slash, ":") {
		clean := strings.TrimPrefix(baseLower, "/")
		_, _, _, ok := parseCachePathTopLevelShape(clean)
		return ok
	}
	return true
}
