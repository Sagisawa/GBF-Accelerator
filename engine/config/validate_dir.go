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
	ErrCodeEmpty             = "ERR_EMPTY_PATH"
	ErrCodeRoot              = "ERR_ROOT_DIRECTORY"
	ErrCodeDriveRoot         = "ERR_DRIVE_ROOT"
	ErrCodeUNCRoot           = "ERR_UNC_ROOT"
	ErrCodeSystemDir         = "ERR_SYSTEM_DIRECTORY"
	ErrCodeUserProtectedDir  = "ERR_USER_PROTECTED_DIRECTORY"
	ErrCodeAncestorProtected = "ERR_ANCESTOR_PROTECTED_DIRECTORY"
	ErrCodeInvalidPath       = "ERR_INVALID_PATH"
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

func dirErr(p, code, msg string) error {
	return &CacheDirValidationError{Code: code, Message: msg, Path: p}
}

// IsSystemJunk returns true if the filename represents OS metadata or system junk
// that should be ignored when determining if a directory is clean or empty,
// and should never be deleted by ClearAll.
func IsSystemJunk(name string) bool {
	base := strings.ToLower(filepath.Base(name))
	return base == ".ds_store" || strings.HasPrefix(base, "._") ||
		strings.Contains(" .spotlight-v100 .fseventsd .trashes thumbs.db desktop.ini $recycle.bin system volume information ", " "+base+" ")
}

// IsDirClean checks whether a directory exists and contains no non-junk files.
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
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	clean := filepath.Clean(p)
	for cur := filepath.Dir(clean); cur != clean && cur != "." && cur != ""; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rel, err := filepath.Rel(cur, clean); err == nil {
				return filepath.Join(resolved, rel)
			}
			break
		}
	}
	return clean
}

// ValidateCacheDir validates that rawPath is a safe directory for storing cache.
func ValidateCacheDir(rawPath string) error {
	resolved := resolveSymlinksWithAncestors(rawPath)
	homeDir, _ := os.UserHomeDir()
	return ValidateCacheDirForPlatform(resolved, runtime.GOOS, homeDir, nil)
}

func normalizePath(p string, isWindows, caseInsensitive bool) string {
	s := strings.TrimSpace(p)
	if s == "" {
		return ""
	}
	if isWindows {
		s = strings.ReplaceAll(s, "\\", "/")
		if len(s) >= 8 && strings.EqualFold(s[:8], "//?/unc/") {
			s = "//" + s[8:]
		} else if strings.HasPrefix(s, "//?/") {
			s = s[4:]
		}
	}
	if caseInsensitive {
		s = strings.ToLower(s)
	}
	isUNC := isWindows && strings.HasPrefix(s, "//")
	s = path.Clean(s)
	if isUNC && !strings.HasPrefix(s, "//") {
		s = "/" + s
	}
	return s
}

func isSubpath(p, parent string) bool {
	return p == parent || strings.HasPrefix(p, parent+"/")
}

func isDriveRoot(norm string) bool {
	return (len(norm) == 2 || (len(norm) == 3 && norm[2] == '/')) && norm[1] == ':' && ((norm[0] >= 'a' && norm[0] <= 'z') || (norm[0] >= 'A' && norm[0] <= 'Z'))
}

var (
	winSystemDirs       = []string{"windows", "program files", "program files (x86)", "programdata", "recovery", "$recycle.bin", "system volume information"}
	unixStrictSysDirs   = []string{"/bin", "/sbin", "/usr", "/etc", "/dev", "/proc", "/sys", "/private/etc", "/system", "/library", "/applications"}
	unixAllowSubSys     = []string{"/var", "/tmp", "/private/var", "/private/tmp", "/private", "/users", "/home", "/volumes", "/mnt", "/media"}
	standardUserFolders = []string{"desktop", "downloads", "documents", "pictures", "music", "videos", "movies"}
)

// ValidateCacheDirForPlatform is the platform-independent core validation logic.
func ValidateCacheDirForPlatform(rawPath, targetGOOS, homeDir string, evalSymlinks func(string) (string, error)) error {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return dirErr(rawPath, ErrCodeEmpty, "cache directory cannot be empty")
	}

	isWindows := targetGOOS == "windows"
	caseInsensitive := isWindows || targetGOOS == "darwin"

	resolved := trimmed
	if evalSymlinks != nil {
		if res, err := evalSymlinks(trimmed); err == nil && res != "" {
			resolved = res
		}
	}

	norm := normalizePath(resolved, isWindows, caseInsensitive)
	if norm == "/" || norm == "." || norm == "" {
		return dirErr(rawPath, ErrCodeRoot, "root directory (/) cannot be used as cache directory")
	}

	if isWindows {
		if isDriveRoot(norm) {
			return dirErr(rawPath, ErrCodeDriveRoot, "drive root (e.g. C:\\) cannot be used as cache directory")
		} else if strings.HasPrefix(norm, "//") && len(strings.Split(strings.Trim(norm, "/"), "/")) <= 2 {
			return dirErr(rawPath, ErrCodeUNCRoot, "network share root (e.g. \\\\server\\share) cannot be used as cache directory")
		}
	}

	checkPath, checkDirs, allowSubSys := norm, unixStrictSysDirs, unixAllowSubSys
	if isWindows {
		checkDirs, allowSubSys = winSystemDirs, []string{"users"}
		if len(norm) >= 2 && norm[1] == ':' {
			checkPath = strings.TrimPrefix(norm[2:], "/")
		}
	}
	for _, sys := range checkDirs {
		if isSubpath(checkPath, sys) {
			return dirErr(rawPath, ErrCodeSystemDir, fmt.Sprintf("system directory %q cannot be used as cache directory", resolved))
		}
	}
	if homeDir != "" {
		if normHome := normalizePath(homeDir, isWindows, caseInsensitive); normHome != "" {
			if norm == normHome {
				return dirErr(rawPath, ErrCodeUserProtectedDir, "user home directory cannot be used directly as cache directory; please choose a dedicated subdirectory")
			} else if !isDriveRoot(norm) && norm != "/" && norm != "" && strings.HasPrefix(normHome, norm+"/") {
				return dirErr(rawPath, ErrCodeAncestorProtected, fmt.Sprintf("ancestor directory %q of user home cannot be used as cache directory", resolved))
			}
			for _, folder := range standardUserFolders {
				if strings.EqualFold(norm, normHome+"/"+folder) {
					return dirErr(rawPath, ErrCodeUserProtectedDir, fmt.Sprintf("standard user folder %q cannot be used directly as cache directory; please choose a dedicated subdirectory", folder))
				}
			}
		}
	}

	for _, sys := range allowSubSys {
		if checkPath == sys {
			return dirErr(rawPath, ErrCodeSystemDir, fmt.Sprintf("system root directory %q cannot be used as cache directory; please use a dedicated subdirectory", resolved))
		}
	}

	return nil
}
