package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gbf-proxy/config"
)

const (
	GitHubRepo     = "Sagisawa/GBF-Accelerator"
	ReleasesAPIURL = "https://api.github.com/repos/" + GitHubRepo + "/releases/latest"
)

type UpdateInfo struct {
	HasUpdate        bool   `json:"has_update"`
	LatestVersion    string `json:"latest_version"`
	CurrentVersion   string `json:"current_version"`
	ReleaseTitle     string `json:"release_title"`
	ReleaseNotes     string `json:"release_notes"`
	HTMLURL          string `json:"html_url"`
	ReleaseURL       string `json:"release_url"`
	DownloadURL      string `json:"download_url"`
	AssetDownloadURL string `json:"asset_download_url"`
	PublishedAt      string `json:"published_at"`
	SHA256           string `json:"sha256,omitempty"`
	Error            string `json:"error,omitempty"`
}


type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// ParseVersion converts strings like "v1.4.0", "1.4.1-rc1" into slice of ints [1, 4, 0].
func ParseVersion(v string) []int {
	cleaned := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "V")
	// Strip any pre-release / build metadata before numeric parsing.
	if idx := strings.IndexAny(cleaned, "-+"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	parts := strings.Split(cleaned, ".")
	var res []int
	re := regexp.MustCompile(`^(\d+)`)
	for _, p := range parts {
		if m := re.FindStringSubmatch(p); len(m) >= 2 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				res = append(res, n)
				continue
			}
		}
		res = append(res, 0)
	}
	for len(res) < 3 {
		res = append(res, 0)
	}
	return res
}

// preReleaseTag returns the pre-release identifier of a version string
// (e.g. "rc1" for "1.8.0-rc1"), or "" if it is a stable release.
func preReleaseTag(v string) string {
	cleaned := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "V")
	dash := strings.Index(cleaned, "-")
	if dash < 0 {
		return ""
	}
	pre := cleaned[dash+1:]
	if idx := strings.Index(pre, "+"); idx >= 0 {
		pre = pre[:idx]
	}
	return strings.ToLower(pre)
}

// comparePreRelease orders pre-release identifiers per semver: a stable release
// (empty tag) outranks any pre-release; numeric identifiers compare numerically.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" { // stable > pre-release
		return 1
	}
	if b == "" {
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
		case aErr == nil: // numeric < alphanumeric
			return -1
		case bErr == nil:
			return 1
		default:
			if as[i] != bs[i] {
				if as[i] < bs[i] {
					return -1
				}
				return 1
			}
		}
	}
	if len(as) < len(bs) {
		return -1
	}
	if len(as) > len(bs) {
		return 1
	}
	return 0
}

// IsNewerVersion returns true if remote version is strictly greater than current version.
func IsNewerVersion(remote, current string) bool {
	r := ParseVersion(remote)
	c := ParseVersion(current)
	maxLen := len(r)
	if len(c) > maxLen {
		maxLen = len(c)
	}
	for len(r) < maxLen {
		r = append(r, 0)
	}
	for len(c) < maxLen {
		c = append(c, 0)
	}
	for i := 0; i < maxLen; i++ {
		if r[i] > c[i] {
			return true
		}
		if r[i] < c[i] {
			return false
		}
	}
	// Numeric components equal: compare pre-release identifiers (semver rule).
	return comparePreRelease(preReleaseTag(remote), preReleaseTag(current)) > 0
}

var (
	hex64Pattern = regexp.MustCompile(`\b([a-fA-F0-9]{64})\b`)
	hashKeyRe    = regexp.MustCompile(`(?i)(?:sha-?256|checksum|hash)`)
)

// ExtractSHA256 extracts a 64-char hex SHA-256 hash matching a filename or standard pattern from release text.
func ExtractSHA256(text, filename string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	fnClean := strings.TrimSpace(filename)
	if fnClean != "" {
		fnLower := strings.ToLower(fnClean)
		lines := strings.Split(text, "\n")
		for idx, line := range lines {
			line = strings.TrimRight(line, "\r")
			if strings.Contains(strings.ToLower(line), fnLower) {
				// 1. Check for 64-hex hash on the same line (markdown list, table cell, sha256sum, BSD style)
				if m := hex64Pattern.FindStringSubmatch(line); len(m) >= 2 {
					return strings.ToLower(m[1])
				}
				// 2. Check subsequent contiguous/indented lines (up to 5 lines) before next item or file
				endIdx := idx + 6
				if endIdx > len(lines) {
					endIdx = len(lines)
				}
				for _, nextLine := range lines[idx+1 : endIdx] {
					nextLine = strings.TrimRight(nextLine, "\r")
					stripped := strings.TrimSpace(nextLine)
					if stripped == "" {
						break
					}
					// Stop if next line is an unindented new section or list item
					if !strings.HasPrefix(nextLine, " ") && !strings.HasPrefix(nextLine, "\t") {
						firstChar := stripped[:1]
						if strings.ContainsAny(firstChar, "#-*+123") {
							break
						}
					}
					// Stop if next line is another file entry
					lowerStripped := strings.ToLower(stripped)
					if strings.Contains(lowerStripped, ".zip") || strings.Contains(lowerStripped, ".exe") ||
						strings.Contains(lowerStripped, ".tar.gz") || strings.Contains(lowerStripped, ".dmg") {
						break
					}
					if mNext := hex64Pattern.FindStringSubmatch(nextLine); len(mNext) >= 2 {
						return strings.ToLower(mNext[1])
					}
				}
			}
		}
	}

	// Generic search: only when filename not provided, or when exactly one unique hash exists
	allMatches := hex64Pattern.FindAllString(text, -1)
	seen := make(map[string]struct{})
	var uniqueHashes []string
	for _, h := range allMatches {
		hLower := strings.ToLower(h)
		if _, exists := seen[hLower]; !exists {
			seen[hLower] = struct{}{}
			uniqueHashes = append(uniqueHashes, hLower)
		}
	}
	if len(uniqueHashes) == 1 {
		if fnClean == "" || hashKeyRe.MatchString(text) {
			return uniqueHashes[0]
		}
	}

	return ""
}

func buildHTTPClient(proxyURL string, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if proxyURL != "" && proxyURL != "none" && proxyURL != "direct" {
		if u, err := url.Parse(proxyURL); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// CheckForUpdate queries GitHub Releases for the latest version.
func CheckForUpdate(upstreamProxy string, timeout time.Duration, currentVer string) *UpdateInfo {
	if currentVer == "" {
		currentVer = config.AppVersion
	}
	if timeout <= 0 {
		timeout = 8 * time.Second
	}

	proxiesToTry := []string{}
	if upstreamProxy != "" && upstreamProxy != "auto" && upstreamProxy != "none" && upstreamProxy != "direct" {
		proxiesToTry = append(proxiesToTry, upstreamProxy)
	}
	proxiesToTry = append(proxiesToTry, "") // Direct fallback

	var lastErr string
	var relData *githubRelease

	for _, p := range proxiesToTry {
		client := buildHTTPClient(p, timeout)
		req, err := http.NewRequest(http.MethodGet, ReleasesAPIURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", fmt.Sprintf("GBF-Accelerator/%s", currentVer))
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Sprintf("网络连接失败: %v", err)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var parsed githubRelease
			if err := json.Unmarshal(body, &parsed); err == nil {
				relData = &parsed
				break
			}
		} else if resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(string(body)), "rate limit") {
			lastErr = "GitHub API 访问频次受限，请稍后再试"
		} else {
			lastErr = fmt.Sprintf("GitHub API 返回 HTTP %d", resp.StatusCode)
		}
	}

	if relData == nil {
		return &UpdateInfo{
			HasUpdate:      false,
			LatestVersion:  currentVer,
			CurrentVersion: currentVer,
			Error:          lastErr,
		}
	}

	latestVer := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(relData.TagName), "v"), "V")
	if latestVer == "" {
		latestVer = currentVer
	}

	// Find suitable release asset URL
	var downloadURL string
	var matchedAssetName string
	isMac := runtime.GOOS == "darwin"

	for _, asset := range relData.Assets {
		name := asset.Name
		if !strings.HasSuffix(strings.ToLower(name), ".zip") {
			continue
		}
		lower := strings.ToLower(name)
		if isMac {
			if strings.Contains(lower, "windows") || strings.Contains(lower, "win32") || strings.Contains(lower, "win64") || strings.Contains(lower, "linux") {
				continue
			}
			if !strings.Contains(lower, "mac") && !strings.Contains(lower, "darwin") && !strings.Contains(lower, "osx") {
				continue
			}
			if strings.Contains(lower, "universal") {
				downloadURL = asset.BrowserDownloadURL
				matchedAssetName = name
				break
			} else if downloadURL == "" {
				downloadURL = asset.BrowserDownloadURL
				matchedAssetName = name
			}
		} else {
			// Windows
			if strings.Contains(lower, "mac") || strings.Contains(lower, "darwin") || strings.Contains(lower, "osx") || strings.Contains(lower, "linux") {
				continue
			}
			if strings.Contains(lower, "gui") {
				downloadURL = asset.BrowserDownloadURL
				matchedAssetName = name
				break
			} else if downloadURL == "" {
				downloadURL = asset.BrowserDownloadURL
				matchedAssetName = name
			}
		}
	}

	pubDate := relData.PublishedAt
	if len(pubDate) > 10 {
		pubDate = pubDate[:10]
	}

	sha256 := ExtractSHA256(relData.Body, matchedAssetName)

	return &UpdateInfo{
		HasUpdate:        IsNewerVersion(latestVer, currentVer),
		LatestVersion:    latestVer,
		CurrentVersion:   currentVer,
		ReleaseTitle:     relData.Name,
		ReleaseNotes:     relData.Body,
		HTMLURL:          relData.HTMLURL,
		ReleaseURL:       relData.HTMLURL,
		DownloadURL:      downloadURL,
		AssetDownloadURL: downloadURL,
		PublishedAt:      pubDate,
		SHA256:           sha256,
	}
}

// GetDefaultDownloadDir returns the user's Downloads directory, or home directory fallback.
func GetDefaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	dl := filepath.Join(home, "Downloads")
	if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
		return dl
	}
	dlCN := filepath.Join(home, "下载")
	if fi, err := os.Stat(dlCN); err == nil && fi.IsDir() {
		return dlCN
	}
	return home
}

// DownloadReleaseAsset streams the download of a release asset to destination,
// validating zip file integrity before final rename.
func DownloadReleaseAsset(
	assetURL string,
	destPath string,
	upstreamProxy string,
	expectedSHA256 string,
	progressCb func(downloaded, total int64),
	cancelCtx context.Context,
) (string, error) {
	if assetURL == "" {
		return "", fmt.Errorf("download URL is empty")
	}

	if destPath == "" {
		filename := filepath.Base(assetURL)
		if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
			filename = "GBF_Accelerator_update.zip"
		}
		destPath = filepath.Join(GetDefaultDownloadDir(), filename)
	}

	if cancelCtx == nil {
		cancelCtx = context.Background()
	}

	_ = os.MkdirAll(filepath.Dir(destPath), 0755)
	partPath := destPath + ".part"
	_ = os.Remove(partPath)

	proxiesToTry := []string{}
	if upstreamProxy != "" && upstreamProxy != "auto" && upstreamProxy != "none" && upstreamProxy != "direct" {
		proxiesToTry = append(proxiesToTry, upstreamProxy)
	}
	proxiesToTry = append(proxiesToTry, "")

	var lastErr error

	for _, p := range proxiesToTry {
		select {
		case <-cancelCtx.Done():
			_ = os.Remove(partPath)
			return "", cancelCtx.Err()
		default:
		}

		client := buildHTTPClient(p, 0)
		req, err := http.NewRequestWithContext(cancelCtx, http.MethodGet, assetURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", fmt.Sprintf("GBF-Accelerator/%s", config.AppVersion))
		req.Header.Set("Accept", "application/octet-stream")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("server returned HTTP %d", resp.StatusCode)
			continue
		}

		totalBytes := resp.ContentLength
		f, err := os.Create(partPath)
		if err != nil {
			_ = resp.Body.Close()
			return "", fmt.Errorf("failed to create target file: %w", err)
		}

		buf := make([]byte, 64*1024)
		var downloaded int64
		var copyErr error

		for {
			select {
			case <-cancelCtx.Done():
				copyErr = cancelCtx.Err()
				break
			default:
			}
			if copyErr != nil {
				break
			}

			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, wErr := f.Write(buf[:n]); wErr != nil {
					copyErr = wErr
					break
				}
				downloaded += int64(n)
				if progressCb != nil {
					progressCb(downloaded, totalBytes)
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					copyErr = readErr
				}
				break
			}
		}

		_ = f.Close()
		_ = resp.Body.Close()

		if copyErr != nil {
			_ = os.Remove(partPath)
			lastErr = copyErr
			continue
		}

		// Check SHA-256 checksum if provided
		expectedClean := strings.ToLower(strings.TrimSpace(expectedSHA256))
		if expectedClean != "" {
			hasher := sha256.New()
			partFile, oErr := os.Open(partPath)
			if oErr != nil {
				_ = os.Remove(partPath)
				lastErr = fmt.Errorf("failed to open downloaded file for checksum: %w", oErr)
				continue
			}
			_, cErr := io.Copy(hasher, partFile)
			_ = partFile.Close()
			if cErr != nil {
				_ = os.Remove(partPath)
				lastErr = fmt.Errorf("failed to compute checksum: %w", cErr)
				continue
			}
			actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
			if actualSHA256 != expectedClean {
				_ = os.Remove(partPath)
				expShort := expectedClean
				if len(expShort) > 8 {
					expShort = expShort[:8]
				}
				actShort := actualSHA256
				if len(actShort) > 8 {
					actShort = actShort[:8]
				}
				lastErr = fmt.Errorf("SHA-256 校验失败 (预期: %s..., 实际: %s...)", expShort, actShort)
				continue
			}
		}

		// Verify zip file integrity
		zr, zErr := zip.OpenReader(partPath)
		if zErr != nil {
			_ = os.Remove(partPath)
			lastErr = fmt.Errorf("corrupted zip archive: %w", zErr)
			continue
		}
		_ = zr.Close()

		// Success: rename .part to final destPath.
		// On Windows, antivirus or indexing services frequently lock newly downloaded archives
		// momentarily, so retry with backoff before giving up.
		var renameErr error
		for attempt := 0; attempt < 5; attempt++ {
			_ = os.Remove(destPath)
			renameErr = os.Rename(partPath, destPath)
			if renameErr == nil {
				break
			}
			select {
			case <-cancelCtx.Done():
				_ = os.Remove(partPath)
				return "", cancelCtx.Err()
			case <-time.After(150 * time.Millisecond):
			}
		}
		if renameErr != nil {
			_ = os.Remove(partPath)
			return "", fmt.Errorf("failed to finalize downloaded file: %w", renameErr)
		}

		return destPath, nil
	}

	return "", lastErr
}
