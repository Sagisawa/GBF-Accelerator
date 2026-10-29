package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Standard error codes for cache directory validation
const (
	ErrCodeEmpty              = "ERR_EMPTY_PATH"
	ErrCodeRoot               = "ERR_ROOT_DIRECTORY"
	ErrCodeDriveRoot          = "ERR_DRIVE_ROOT"
	ErrCodeUNCRoot            = "ERR_UNC_ROOT"
	ErrCodeSystemDir          = "ERR_SYSTEM_DIRECTORY"
	ErrCodeUserProtectedDir   = "ERR_USER_PROTECTED_DIRECTORY"
	ErrCodeAncestorProtected  = "ERR_ANCESTOR_PROTECTED_DIRECTORY"
	ErrCodeInvalidPath        = "ERR_INVALID_PATH"
)

// CacheDirValidationError is a structured error returned when a candidate
// cache directory is rejected during configuration saving or modification.
type CacheDirValidationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path"`
}

func (e *CacheDirValidationError) Error() string {
	return fmt.Sprintf("invalid cache directory %q (%s): %s", e.Path, e.Code, e.Message)
}

// IsSystemJunk returns true if the filename represents OS metadata or system junk
// that should be ignored when determining if a directory is clean or empty,
// and should never be deleted by ClearAll.
func IsSystemJunk(name string) bool {
	base := filepath.Base(name)
	if base == ".DS_Store" {
		return true
	}
	if strings.HasPrefix(base, "._") {
		return true
	}
	clean := strings.ToLower(base)
	switch clean {
	case ".spotlight-v100", ".fseventsd", ".trashes", "thumbs.db", "desktop.ini", "$recycle.bin", "system volume information":
		return true
	}
	return false
}

// IsDirClean checks whether a directory exists and contains no user or non-junk files.
// System junk files (.DS_Store, Thumbs.db, desktop.ini, etc.) are ignored.
func IsDirClean(dirPath string) (bool, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	for _, e := range entries {
		if !IsSystemJunk(e.Name()) {
			return false, nil
		}
	}
	return true, nil
}

// resolveSymlinksWithAncestors evaluates symlinks on the path. If the path does not
// yet exist on disk, it walks up to the nearest existing ancestor, evaluates symlinks
// on that ancestor, and re-attaches the trailing non-existent segments.
func resolveSymlinksWithAncestors(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err == nil {
		p = abs
	}
	// Try direct EvalSymlinks
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}

	clean := filepath.Clean(p)
	current := clean
	var tail []string
	for {
		parent := filepath.Dir(current)
		if parent == current || parent == "." || parent == "" {
			break
		}
		tail = append([]string{filepath.Base(current)}, tail...)
		current = parent
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			result := resolved
			for _, seg := range tail {
				result = filepath.Join(result, seg)
			}
			return result
		}
	}
	return clean
}

// ValidateCacheDir validates that rawPath is a safe directory for storing cache.
// It resolves symlinks (falling back to existing ancestors if non-existent)
// and rejects drive roots, filesystem roots, UNC share roots, system directories,
// and protected user top-level directories (while allowing dedicated subdirectories).
func ValidateCacheDir(rawPath string) error {
	resolved := resolveSymlinksWithAncestors(rawPath)
	homeDir, _ := os.UserHomeDir()
	return ValidateCacheDirForPlatform(resolved, runtime.GOOS, homeDir, nil)
}

// splitPathSegments splits a path into cleaned segments for robust ancestor/descendant matching.
// Handles both Windows and Unix slash styles in a platform-independent way so Windows paths
// are accurately evaluated on Linux CI and vice versa.
func splitPathSegments(p string, isWindows bool) []string {
	clean := p
	if isWindows {
		clean = strings.ReplaceAll(clean, `\`, `/`)
	}
	if strings.HasPrefix(clean, `//?/unc/`) || strings.HasPrefix(clean, `//?/UNC/`) {
		clean = "//" + clean[8:]
	} else if strings.HasPrefix(clean, `//?/`) {
		clean = clean[4:]
	}
	clean = path.Clean(clean)
	parts := strings.Split(clean, "/")
	var res []string
	for _, part := range parts {
		if part != "" && part != "." {
			if isWindows {
				res = append(res, strings.ToLower(part))
			} else {
				res = append(res, part)
			}
		}
	}
	return res
}

// isDriveRoot checks whether a segment list represents a Windows drive root (e.g. ["c:"]).
func isDriveRoot(segs []string) bool {
	if len(segs) == 1 {
		s := strings.TrimSuffix(segs[0], ".")
		if len(s) == 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
			return true
		}
	}
	return false
}

// isUNCRoot checks whether a path represents a Windows UNC share root (e.g. \\server\share or //server/share).
func isUNCRoot(p string) bool {
	norm := strings.ReplaceAll(p, `\`, `/`)
	if strings.HasPrefix(norm, `//?/unc/`) || strings.HasPrefix(norm, `//?/UNC/`) {
		norm = "//" + norm[8:]
	} else if strings.HasPrefix(norm, `//?/`) {
		norm = norm[4:]
	}
	if !strings.HasPrefix(norm, "//") {
		return false
	}
	trimmed := strings.Trim(norm, "/")
	parts := strings.Split(trimmed, "/")
	// Exactly 2 components: server and share
	return len(parts) == 2
}

// isUNCShare checks whether a path is under a Windows UNC share.
func isUNCShare(p string) bool {
	norm := strings.ReplaceAll(p, `\`, `/`)
	if strings.HasPrefix(norm, `//?/unc/`) || strings.HasPrefix(norm, `//?/UNC/`) {
		norm = "//" + norm[8:]
	} else if strings.HasPrefix(norm, `//?/`) {
		norm = norm[4:]
	}
	return strings.HasPrefix(norm, "//")
}

// segmentsEqual checks exact segment match (handling case-insensitivity on Windows/macOS).
func segmentsEqual(a, b []string, caseInsensitive bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if caseInsensitive {
			if !strings.EqualFold(a[i], b[i]) {
				return false
			}
		} else {
			if a[i] != b[i] {
				return false
			}
		}
	}
	return true
}

// isAncestorOf checks if ancestorSegs is a strict ancestor of targetSegs.
func isAncestorOf(ancestorSegs, targetSegs []string, caseInsensitive bool) bool {
	if len(ancestorSegs) >= len(targetSegs) {
		return false
	}
	for i := range ancestorSegs {
		if caseInsensitive {
			if !strings.EqualFold(ancestorSegs[i], targetSegs[i]) {
				return false
			}
		} else {
			if ancestorSegs[i] != targetSegs[i] {
				return false
			}
		}
	}
	return true
}

// isDescendantOf checks if targetSegs is a strict descendant of parentSegs.
func isDescendantOf(targetSegs, parentSegs []string, caseInsensitive bool) bool {
	return isAncestorOf(parentSegs, targetSegs, caseInsensitive)
}

// ValidateCacheDirForPlatform is the platform-independent core validation logic.
// It can be tested on any OS (including Linux CI) by specifying targetGOOS ("windows", "darwin", "linux").
func ValidateCacheDirForPlatform(rawPath string, targetGOOS string, homeDir string, evalSymlinks func(string) (string, error)) error {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return &CacheDirValidationError{
			Code:    ErrCodeEmpty,
			Message: "cache directory cannot be empty",
			Path:    rawPath,
		}
	}

	isWindows := targetGOOS == "windows"
	caseInsensitive := isWindows || targetGOOS == "darwin"

	// Resolve symlinks if a custom resolver was provided
	resolved := trimmed
	if evalSymlinks != nil {
		if res, err := evalSymlinks(trimmed); err == nil && res != "" {
			resolved = res
		}
	}

	segs := splitPathSegments(resolved, isWindows)
	if len(segs) == 0 {
		return &CacheDirValidationError{
			Code:    ErrCodeRoot,
			Message: "filesystem root cannot be used as cache directory",
			Path:    rawPath,
		}
	}

	// 1. Root directory validation
	if isWindows {
		if isDriveRoot(segs) {
			return &CacheDirValidationError{
				Code:    ErrCodeDriveRoot,
				Message: "drive root (e.g. C:\\) cannot be used as cache directory",
				Path:    rawPath,
			}
		}
		if isUNCRoot(resolved) {
			return &CacheDirValidationError{
				Code:    ErrCodeUNCRoot,
				Message: "network share root (e.g. \\\\server\\share) cannot be used as cache directory",
				Path:    rawPath,
			}
		}
	} else {
		// Unix / macOS filesystem root: "/" produces 0 segments after split
		// If resolved was literally "/"
		cleanSlash := filepath.ToSlash(resolved)
		if cleanSlash == "/" {
			return &CacheDirValidationError{
				Code:    ErrCodeRoot,
				Message: "root directory (/) cannot be used as cache directory",
				Path:    rawPath,
			}
		}
	}

	// 2. System directories definition
	var systemDirs [][]string
	var systemRootsAllowSubdirs [][]string // e.g. /tmp, /var, /private/var where descendants are allowed

	if isWindows {
		// Drive letter of target if available, or check against any drive letter
		systemDirNames := []string{
			"windows",
			"program files",
			"program files (x86)",
			"programdata",
			"recovery",
			"$recycle.bin",
			"system volume information",
		}
		for _, name := range systemDirNames {
			// e.g. ["c:", "windows"] or relative ["windows"]
			if len(segs) > 0 && len(segs[0]) == 2 && segs[0][1] == ':' {
				systemDirs = append(systemDirs, []string{segs[0], name})
			} else {
				systemDirs = append(systemDirs, []string{name})
			}
		}
	} else {
		// Unix / macOS system directories
		strictSys := []string{
			"/bin", "/sbin", "/usr", "/etc", "/dev", "/proc", "/sys",
			"/private/etc", "/System", "/Library", "/Applications",
		}
		for _, s := range strictSys {
			systemDirs = append(systemDirs, splitPathSegments(s, false))
		}

		// System directories that reject the dir itself, but ALLOW subdirectories
		// e.g. /var, /tmp, /private/var, /private/tmp, /private
		allowSubSys := []string{"/var", "/tmp", "/private/var", "/private/tmp", "/private"}
		for _, s := range allowSubSys {
			systemRootsAllowSubdirs = append(systemRootsAllowSubdirs, splitPathSegments(s, false))
		}
	}

	// Check system directories that reject exact match and descendants
	for _, sysSegs := range systemDirs {
		if segmentsEqual(segs, sysSegs, caseInsensitive) || isDescendantOf(segs, sysSegs, caseInsensitive) {
			return &CacheDirValidationError{
				Code:    ErrCodeSystemDir,
				Message: fmt.Sprintf("system directory %q cannot be used as cache directory", resolved),
				Path:    rawPath,
			}
		}
	}

	// Check system roots that only reject exact match (descendants allowed, like /tmp/mycache, /private/var/folders/...)
	for _, sysRoot := range systemRootsAllowSubdirs {
		if segmentsEqual(segs, sysRoot, caseInsensitive) {
			return &CacheDirValidationError{
				Code:    ErrCodeSystemDir,
				Message: fmt.Sprintf("system root directory %q cannot be used as cache directory; please use a dedicated subdirectory", resolved),
				Path:    rawPath,
			}
		}
	}

	// 3. User protected directories (Home directory and standard user folders)
	var homeSegs []string
	if homeDir != "" {
		homeSegs = splitPathSegments(homeDir, isWindows)
	}

	// If target is ancestor of user home directory (e.g. C:\Users or /Users or /home):
	if len(homeSegs) > 1 {
		// Ancestors of home:
		for i := 1; i < len(homeSegs); i++ {
			ancestor := homeSegs[:i]
			if !isDriveRoot(ancestor) && segmentsEqual(segs, ancestor, caseInsensitive) {
				return &CacheDirValidationError{
					Code:    ErrCodeAncestorProtected,
					Message: fmt.Sprintf("ancestor directory %q of user home cannot be used as cache directory", resolved),
					Path:    rawPath,
				}
			}
		}
	}

	if len(homeSegs) > 0 {
		// User home directory itself: exact match is rejected
		if segmentsEqual(segs, homeSegs, caseInsensitive) {
			return &CacheDirValidationError{
				Code:    ErrCodeUserProtectedDir,
				Message: "user home directory cannot be used directly as cache directory; please choose a dedicated subdirectory",
				Path:    rawPath,
			}
		}

		// Standard user folders under home
		standardFolders := []string{
			"desktop",
			"downloads",
			"documents",
			"pictures",
			"music",
			"videos",
			"movies",
		}
		for _, folder := range standardFolders {
			folderSegs := append(append([]string{}, homeSegs...), folder)
			// EXACT match: reject!
			if segmentsEqual(segs, folderSegs, caseInsensitive) {
				return &CacheDirValidationError{
					Code:    ErrCodeUserProtectedDir,
					Message: fmt.Sprintf("standard user folder %q cannot be used directly as cache directory; please choose a dedicated subdirectory", folder),
					Path:    rawPath,
				}
			}
			// Descendants (e.g. ~/Downloads/ACGPower/cache): explicitly ALLOWED!
		}
	}

	return nil
}
