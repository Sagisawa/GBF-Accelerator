package cache

import (
	"bytes"
	"errors"
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

func verifyMarkerFileInfo(fi os.FileInfo, readFn func() ([]byte, error)) bool {
	if fi == nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > 256 {
		return false
	}
	content, err := readFn()
	return err == nil && bytes.Equal(bytes.TrimSpace(content), []byte(RootMarkerExpectedText))
}

// HasRootMarker checks whether the root marker file exists in baseDir as a regular file
// with the expected marker content. Symlinks, directories, empty files, or corrupted content return false.
func HasRootMarker(baseDir string) bool {
	if baseDir == "" {
		return false
	}
	markerPath := filepath.Join(baseDir, RootMarkerFileName)
	fi, err := os.Lstat(markerPath)
	return err == nil && verifyMarkerFileInfo(fi, func() ([]byte, error) { return os.ReadFile(markerPath) })
}

func hasRootMarkerInRoot(root *os.Root) bool {
	if root == nil {
		return false
	}
	fi, err := root.Lstat(RootMarkerFileName)
	return err == nil && verifyMarkerFileInfo(fi, func() ([]byte, error) { return root.ReadFile(RootMarkerFileName) })
}

// EnsureRootMarker writes the root marker file to baseDir if not already present with valid content.
func EnsureRootMarker(baseDir string) error {
	if baseDir == "" { return os.ErrInvalid }
	if HasRootMarker(baseDir) { return nil }
	markerPath := filepath.Join(baseDir, RootMarkerFileName)
	if fi, err := os.Lstat(markerPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 { return errors.New("root marker path is a symlink") }
		if fi.IsDir() { return errors.New("root marker path is a directory") }
		return errors.New("existing .gbf_cache file contains unrecognized content; refusing to overwrite")
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(markerPath, []byte(RootMarkerExpectedContent), 0644)
}

func (m *Manager) HasRootMarker() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return HasRootMarker(m.cacheBase)
}

func (m *Manager) EnsureRootMarker() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return EnsureRootMarker(m.cacheBase)
}

func (m *Manager) ValidateDestructiveCacheDir() error {
	if m == nil { return errors.New("cache manager is unavailable") }
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	if err := config.ValidateCacheDir(base); err != nil { return err }
	if fi, err := os.Lstat(base); err == nil && (fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir()) {
		return errors.New("cache root must be an ordinary directory, not a symlink or file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func isKnownGBFCategory(c string) bool {
	return strings.Contains(" js css img sound font manifest html misc cjs json model app jssdk ", " "+c+" ")
}

func isHistoricalEmptyNSHost(host string) bool {
	l := strings.ToLower(host)
	return l == "granbluefantasy.com" || strings.HasSuffix(l, ".granbluefantasy.jp") ||
		strings.HasSuffix(l, ".granbluefantasy.com") || l == "ava-a.sp.mbga.jp" || l == "connect.mobage.jp"
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' { return false }
	}
	return len(s) > 0
}

func isPositiveVersionSegment(s string) bool {
	return s != "" && s != "0" && len(s) <= 19 && isAllDigits(s) && s[0] != '0'
}

func parseValidHostNS(rawNS string) (string, bool) {
	if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) { return "", false }
	ns := sanitizeNamespace(rawNS)
	return ns, ns != "" && ns != "hosts" && ns != "gbf" && ns == strings.ToLower(rawNS)
}

func isGBFTopLevel(first string) bool {
	return first == "assets" || first == "assets_en" || first == "assets_jp" || isKnownGBFCategory(first)
}

func parseCachePathTopLevelShape(slashRel string) (string, string, bool, bool) {
	if slashRel == "" || slashRel == "." || strings.HasPrefix(slashRel, "..") {
		return "", "", false, false
	}
	clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(slashRel)), "/")
	segs := splitCleanSegments(clean)
	if len(segs) == 1 && strings.EqualFold(segs[0], "favicon.ico") {
		return "gbf", "favicon.ico", false, true
	}
	if len(segs) >= 3 && segs[0] == "hosts" {
		if ns, ok := parseValidHostNS(segs[1]); ok {
			return ns, strings.Join(segs[2:], "/"), true, true
		}
		return "", "", false, false
	}
	if len(segs) >= 2 && (isGBFTopLevel(segs[0]) || (isPositiveVersionSegment(segs[0]) && len(segs) >= 3 && isKnownGBFCategory(segs[1]))) {
		return "gbf", clean, false, true
	}
	return "", "", false, false
}

func IsConfirmedCacheDirectory(slashRelDir string) bool {
	if slashRelDir == "" || slashRelDir == "." || strings.HasPrefix(slashRelDir, "..") {
		return false
	}
	segs := splitCleanSegments(strings.Trim(filepath.ToSlash(filepath.Clean(slashRelDir)), "/"))
	if len(segs) == 0 {
		return false
	}
	if segs[0] == "hosts" {
		return len(segs) == 1 || func() bool { _, ok := parseValidHostNS(segs[1]); return ok }()
	}
	return isGBFTopLevel(segs[0]) || (isPositiveVersionSegment(segs[0]) && (len(segs) == 1 || isKnownGBFCategory(segs[1])))
}

func VerifyTier1Meta(slashRel string, metaBytes []byte) (diskMeta, bool) {
	if slashRel == "" || len(metaBytes) == 0 {
		return diskMeta{}, false
	}
	expectedNS, _, isHosts, shapeOK := parseCachePathTopLevelShape(slashRel)
	parsedMeta, parseOK := parseExtFile(metaBytes)
	if !shapeOK || !parseOK || parsedMeta.Version != 1 {
		return diskMeta{}, false
	}
	segs := splitCleanSegments(strings.TrimPrefix(filepath.ToSlash(slashRel), "/"))
	if isHosts {
		return parsedMeta, parsedMeta.Namespace == "gbf" || sanitizeNamespace(parsedMeta.Namespace) == expectedNS ||
			(parsedMeta.Namespace == "" && parsedMeta.Verified && isHistoricalEmptyNSHost(expectedNS))
	}
	if (len(segs) >= 1 && isPositiveVersionSegment(segs[0]) && parsedMeta.Namespace != "gbf") ||
		(parsedMeta.Namespace != "" && parsedMeta.Namespace != "gbf" && sanitizeNamespace(parsedMeta.Namespace) != "gbf") {
		return diskMeta{}, false
	}
	return parsedMeta, true
}

func isIgnoredCachePath(slashRel, fileName string) bool {
	return slashRel == RootMarkerFileName || config.IsSystemJunk(fileName) ||
		strings.HasSuffix(slashRel, ".ext") || strings.Contains(slashRel, ".tmp.") || IsAcceleratorQuarantineFile(slashRel)
}

func checkConfirmedCacheMeta(slashRel, baseName string, readMeta func() ([]byte, error)) bool {
	if isIgnoredCachePath(slashRel, baseName) { return false }
	metaBytes, err := readMeta()
	if err != nil { return false }
	_, ok := VerifyTier1Meta(slashRel, metaBytes)
	return ok
}

func IsConfirmedCacheFile(baseDir, fullPath string, hasRootMarker bool) bool {
	fi, err := os.Lstat(fullPath)
	return err == nil && IsConfirmedCacheFileWithInfo(baseDir, fullPath, fi, hasRootMarker)
}

func IsConfirmedCacheFileWithInfo(baseDir, fullPath string, fi os.FileInfo, hasRootMarker bool) bool {
	if fi == nil || !fi.Mode().IsRegular() || fi.Size() <= 0 { return false }
	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") { return false }
	return checkConfirmedCacheMeta(filepath.ToSlash(rel), filepath.Base(fullPath), func() ([]byte, error) { return os.ReadFile(fullPath + ".ext") })
}

func isConfirmedCacheFileInRoot(root *os.Root, rel string, fi os.FileInfo, hasRootMarker bool) bool {
	if fi == nil || !fi.Mode().IsRegular() || fi.Size() <= 0 || rel == "" || rel == "." || strings.HasPrefix(rel, "..") { return false }
	return checkConfirmedCacheMeta(filepath.ToSlash(rel), filepath.Base(rel), func() ([]byte, error) { return root.ReadFile(rel + ".ext") })
}

func IsAcceleratorTempFile(slashRel string) bool {
	parts := strings.Split(slashRel, ".tmp.")
	if len(parts) != 2 { return false }
	np := strings.Split(parts[1], ".")
	if len(np) != 3 || !isAllDigits(np[0]) || !isAllDigits(np[1]) || !isAllDigits(np[2]) { return false }
	base := strings.ToLower(strings.TrimSuffix(parts[0], ".ext"))
	_, cleanKey, _, ok := parseCachePathTopLevelShape(base)
	return ok && isAssetExtension(strings.ToLower(cleanKey))
}

func IsAcceleratorQuarantineFile(slashRel string) bool {
	slash := filepath.ToSlash(slashRel)
	idx := strings.LastIndex(slash, ".quarantine.")
	if idx < 0 || !isAllDigits(slash[idx+len(".quarantine."):]) { return false }
	base := strings.ToLower(strings.TrimSuffix(slash[:idx], ".ext"))
	if !isAssetExtension(base) { return false }
	if !filepath.IsAbs(slash) && !strings.Contains(slash, ":") {
		_, _, _, ok := parseCachePathTopLevelShape(strings.TrimPrefix(base, "/"))
		return ok
	}
	return true
}
