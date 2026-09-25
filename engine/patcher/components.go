package patcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gbf-proxy/config"
)

const (
	CanonicalModuleSHA256   = "237fa8dc10dd46b4f147effc6361f0711e3258a4aacbd2960bcaff5a711b665b"
	CanonicalLicensesSHA256 = "07ebff9961f23ed45efad65eedc8359ece9345fe212a943fdd82353b8b0a2f9f"
	GitHubRepo              = "Sagisawa/GBF-Accelerator"
)

type ComponentSpec struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

type ComponentStatus struct {
	ID          string `json:"id"`
	FileName    string `json:"file_name"`
	Path        string `json:"path"`
	Installed   bool   `json:"installed"`
	Verified    bool   `json:"verified"`
	Size        int64  `json:"size"`
	ExpectedSHA string `json:"expected_sha256"`
	ActualSHA   string `json:"actual_sha256,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ComponentsManifest struct {
	Version     string            `json:"version"`
	InstalledAt string            `json:"installed_at"`
	Components  map[string]string `json:"components"` // ID -> SHA256
}

type DownloadProgress struct {
	Active          bool    `json:"active"`
	CurrentFile     string  `json:"current_file"`
	FileIndex       int     `json:"file_index"`
	TotalFiles      int     `json:"total_files"`
	DownloadedBytes int64   `json:"downloaded_bytes"`
	TotalBytes      int64   `json:"total_bytes"`
	Percent         float64 `json:"percent"`
	SpeedBytesSec   int64   `json:"speed_bytes_sec"`
	Stage           string  `json:"stage"` // "idle", "downloading", "verifying", "done", "error"
	Error           string  `json:"error"`
	Done            bool    `json:"done"`
}

// GetAndroidToolsDir returns the canonical directory where Android components live.
func GetAndroidToolsDir() string {
	return filepath.Join(config.GetBaseDir(), "tools", "android")
}

// GetDefaultComponentSpecs returns the authoritative component metadata.
func GetDefaultComponentSpecs() []ComponentSpec {
	tag := "v" + config.AppVersion
	baseReleaseURL := "https://github.com/" + GitHubRepo + "/releases/download/" + tag + "/"
	return []ComponentSpec{
		{
			ID:          "lspatch",
			FileName:    "lspatch.jar",
			Size:        12106851,
			SHA256:      CanonicalLSPatchSHA256,
			URL:         baseReleaseURL + "lspatch.jar",
			Description: "LSPatch Portable 核心 (" + CanonicalLSPatchVersion + ")",
		},
		{
			ID:          "module",
			FileName:    "xposed-release.apk",
			Size:        995099,
			SHA256:      CanonicalModuleSHA256,
			URL:         baseReleaseURL + "xposed-release.apk",
			Description: "GBF-Accelerator Xposed Module (v0.1)",
		},
		{
			ID:          "licenses",
			FileName:    "THIRD_PARTY_LICENSES.md",
			Size:        1800,
			SHA256:      CanonicalLicensesSHA256,
			URL:         baseReleaseURL + "THIRD_PARTY_LICENSES.md",
			Description: "第三方开源许可协议",
		},
	}
}

// ComputeFileSHA256 calculates the lowercase hex SHA-256 checksum of a file.
func ComputeFileSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CheckComponents verifies whether Android Patch components are installed and uncorrupted.
func CheckComponents(toolsDir string, exeDir string) (allInstalled bool, allVerified bool, statuses []ComponentStatus, errStr string) {
	return CheckComponentsWithSpecs(toolsDir, exeDir, GetDefaultComponentSpecs())
}

// CheckComponentsWithSpecs verifies components against a specified list of ComponentSpecs.
func CheckComponentsWithSpecs(toolsDir string, exeDir string, specs []ComponentSpec) (allInstalled bool, allVerified bool, statuses []ComponentStatus, errStr string) {
	statuses = make([]ComponentStatus, 0, len(specs))
	installedCount := 0
	verifiedCount := 0
	var errs []string

	for _, spec := range specs {
		st := ComponentStatus{
			ID:          spec.ID,
			FileName:    spec.FileName,
			ExpectedSHA: spec.SHA256,
		}

		// Candidate search paths: 1) toolsDir, 2) exeDir/tools/android, 3) exeDir
		candidates := []string{
			filepath.Join(toolsDir, spec.FileName),
		}
		if exeDir != "" && exeDir != toolsDir {
			candidates = append(candidates,
				filepath.Join(exeDir, "tools", "android", spec.FileName),
				filepath.Join(exeDir, spec.FileName),
			)
		}

		var foundPath string
		var foundFi os.FileInfo
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Size() > 0 {
				foundPath = c
				foundFi = fi
				break
			}
		}

		if foundPath == "" {
			st.Installed = false
			st.Verified = false
			st.Error = fmt.Sprintf("文件未安装: %s", spec.FileName)
			statuses = append(statuses, st)
			continue
		}

		st.Installed = true
		st.Path = foundPath
		st.Size = foundFi.Size()
		installedCount++

		actualSHA, err := ComputeFileSHA256(foundPath)
		if err != nil {
			st.Verified = false
			st.Error = fmt.Sprintf("计算校验和失败: %v", err)
			errs = append(errs, st.Error)
			statuses = append(statuses, st)
			continue
		}

		st.ActualSHA = actualSHA
		if !strings.EqualFold(actualSHA, spec.SHA256) {
			st.Verified = false
			st.Error = fmt.Sprintf("哈希校验不匹配: 期望 %s, 实际 %s", spec.SHA256, actualSHA)
			errs = append(errs, fmt.Sprintf("%s 校验失败", spec.FileName))
		} else {
			st.Verified = true
			verifiedCount++
		}

		statuses = append(statuses, st)
	}

	allInstalled = (installedCount == len(specs))
	allVerified = (verifiedCount == len(specs))
	if len(errs) > 0 {
		errStr = strings.Join(errs, "; ")
	}

	return allInstalled, allVerified, statuses, errStr
}

// DownloadComponents performs streaming download, SHA-256 verification, and atomic placement using default specs.
func DownloadComponents(
	ctx context.Context,
	toolsDir string,
	customURLs map[string]string,
	progressFn func(DownloadProgress),
) error {
	return DownloadComponentsWithSpecs(ctx, toolsDir, GetDefaultComponentSpecs(), customURLs, progressFn)
}

// DownloadComponentsWithSpecs performs streaming download, SHA-256 verification, and atomic placement.
// If any error occurs or context is cancelled, unfinished (.tmp) files are strictly removed.
func DownloadComponentsWithSpecs(
	ctx context.Context,
	toolsDir string,
	specs []ComponentSpec,
	customURLs map[string]string,
	progressFn func(DownloadProgress),
) error {
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return fmt.Errorf("failed to create tools directory: %w", err)
	}

	if len(specs) == 0 {
		specs = GetDefaultComponentSpecs()
	}
	var totalTargetBytes int64 = 0
	for _, s := range specs {
		totalTargetBytes += s.Size
	}

	prog := DownloadProgress{
		Active:          true,
		TotalFiles:      len(specs),
		TotalBytes:      totalTargetBytes,
		DownloadedBytes: 0,
		Percent:         0.0,
		Stage:           "downloading",
	}

	if progressFn != nil {
		progressFn(prog)
	}

	client := &http.Client{
		Timeout: 0, // Managed by ctx
	}

	downloadedMap := make(map[string]string)

	for i, spec := range specs {
		select {
		case <-ctx.Done():
			prog.Active = false
			prog.Stage = "error"
			prog.Error = "下载已被用户取消"
			if progressFn != nil {
				progressFn(prog)
			}
			return ctx.Err()
		default:
		}

		prog.FileIndex = i + 1
		prog.CurrentFile = spec.FileName
		targetPath := filepath.Join(toolsDir, spec.FileName)

		// 1. If file already exists and passes SHA-256, skip re-download
		if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() && fi.Size() > 0 {
			if existingSHA, err := ComputeFileSHA256(targetPath); err == nil && strings.EqualFold(existingSHA, spec.SHA256) {
				prog.DownloadedBytes += spec.Size
				prog.Percent = float64(prog.DownloadedBytes) / float64(totalTargetBytes) * 100
				downloadedMap[spec.ID] = existingSHA
				if progressFn != nil {
					progressFn(prog)
				}
				continue
			}
		}

		// 2. Resolve URL
		downloadURL := spec.URL
		if customURLs != nil && customURLs[spec.ID] != "" {
			downloadURL = customURLs[spec.ID]
		}

		tempPath := filepath.Join(toolsDir, spec.FileName+".tmp")
		_ = os.Remove(tempPath) // Remove any previous stale temp file

		cleanupTemp := func() {
			_ = os.Remove(tempPath)
		}

		// 3. Download stream to temp file
		err := func() error {
			defer func() {
				// Safety cleanup if error occurred
			}()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
			if err != nil {
				cleanupTemp()
				return fmt.Errorf("failed to create request for %s: %w", spec.FileName, err)
			}
			req.Header.Set("User-Agent", "GBF-Accelerator/"+config.AppVersion)

			resp, err := client.Do(req)
			if err != nil {
				cleanupTemp()
				return fmt.Errorf("failed to download %s: %w", spec.FileName, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				cleanupTemp()
				return fmt.Errorf("failed to download %s: HTTP %d %s", spec.FileName, resp.StatusCode, resp.Status)
			}

			outF, err := os.Create(tempPath)
			if err != nil {
				cleanupTemp()
				return fmt.Errorf("failed to create temp file for %s: %w", spec.FileName, err)
			}
			defer outF.Close()

			hasher := sha256.New()
			multiWriter := io.MultiWriter(outF, hasher)

			// Read with progress tracking
			buf := make([]byte, 64*1024)
			var fileDownloaded int64 = 0
			lastTime := time.Now()
			var bytesSinceLastTime int64 = 0

			for {
				select {
				case <-ctx.Done():
					_ = outF.Close()
					cleanupTemp()
					return ctx.Err()
				default:
				}

				n, readErr := resp.Body.Read(buf)
				if n > 0 {
					if _, writeErr := multiWriter.Write(buf[:n]); writeErr != nil {
						_ = outF.Close()
						cleanupTemp()
						return fmt.Errorf("failed to write data for %s: %w", spec.FileName, writeErr)
					}

					fileDownloaded += int64(n)
					prog.DownloadedBytes += int64(n)
					bytesSinceLastTime += int64(n)

					now := time.Now()
					elapsed := now.Sub(lastTime)
					if elapsed >= 300*time.Millisecond {
						prog.SpeedBytesSec = int64(float64(bytesSinceLastTime) / elapsed.Seconds())
						lastTime = now
						bytesSinceLastTime = 0
					}

					prog.Percent = float64(prog.DownloadedBytes) / float64(totalTargetBytes) * 100
					if prog.Percent > 99.0 && i < len(specs)-1 {
						prog.Percent = 99.0
					}
					if progressFn != nil {
						progressFn(prog)
					}
				}

				if readErr == io.EOF {
					break
				}
				if readErr != nil {
					_ = outF.Close()
					cleanupTemp()
					return fmt.Errorf("read error during download of %s: %w", spec.FileName, readErr)
				}
			}

			_ = outF.Close()

			// 4. Verify SHA-256
			prog.Stage = "verifying"
			if progressFn != nil {
				progressFn(prog)
			}

			actualSHA := hex.EncodeToString(hasher.Sum(nil))
			if !strings.EqualFold(actualSHA, spec.SHA256) {
				cleanupTemp()
				_ = os.Remove(targetPath)
				return fmt.Errorf("checksum validation failed for %s: expected %s, got %s", spec.FileName, spec.SHA256, actualSHA)
			}

			// 5. Atomic rename to target
			_ = os.Remove(targetPath)
			if err := os.Rename(tempPath, targetPath); err != nil {
				cleanupTemp()
				return fmt.Errorf("failed to finalize component %s: %w", spec.FileName, err)
			}

			downloadedMap[spec.ID] = actualSHA
			prog.Stage = "downloading"
			return nil
		}()

		if err != nil {
			prog.Active = false
			prog.Stage = "error"
			prog.Error = err.Error()
			if progressFn != nil {
				progressFn(prog)
			}
			return err
		}
	}

	// 6. Write manifest.json
	manifest := ComponentsManifest{
		Version:     config.AppVersion,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Components:  downloadedMap,
	}
	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(toolsDir, "manifest.json"), manifestData, 0644)
	}

	prog.Active = false
	prog.Done = true
	prog.Stage = "done"
	prog.Percent = 100.0
	prog.SpeedBytesSec = 0
	if progressFn != nil {
		progressFn(prog)
	}

	return nil
}

// HostAppReleaseName returns the canonical release file name for the Android host app asset.
func HostAppReleaseName() string {
	return fmt.Sprintf("GBF_Accelerator_v%s_Android.apk", config.AppVersion)
}

// FindOrFetchHostApp locates the GBF-Accelerator Android Host App APK locally,
// or on-demand downloads it from the official GitHub Release asset if not present locally.
// Note: This is strictly called on-demand when the user explicitly triggers installation.
func FindOrFetchHostApp(ctx context.Context, toolsDir string, exeDir string) (string, error) {
	candidates := []string{
		filepath.Join(toolsDir, HostAppReleaseName()),
		filepath.Join(toolsDir, "GBF_Accelerator_Android.apk"),
		filepath.Join(toolsDir, "app-release.apk"),
		filepath.Join(exeDir, "release", HostAppReleaseName()),
		filepath.Join(exeDir, "release", "app-release.apk"),
		filepath.Join(exeDir, "..", "release", HostAppReleaseName()),
		filepath.Join(exeDir, "..", "android", "app", "build", "outputs", "apk", "release", "app-release.apk"),
		filepath.Join(exeDir, "..", "android", "app", "build", "outputs", "apk", "debug", "app-debug.apk"),
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() && fi.Size() > 1024*1024 {
			return cand, nil
		}
	}

	// Not found locally -> On-demand download from project Release assets
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create tools directory: %w", err)
	}

	targetPath := filepath.Join(toolsDir, HostAppReleaseName())
	tempPath := targetPath + ".downloading"
	_ = os.Remove(tempPath)

	tag := "v" + config.AppVersion
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", GitHubRepo, tag, HostAppReleaseName())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build download request: %w", err)
	}

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download Android app (%s): %w", downloadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download Android app: HTTP %d from %s", resp.StatusCode, downloadURL)
	}

	outF, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		_ = outF.Close()
		_ = os.Remove(tempPath)
	}()

	if _, err := io.Copy(outF, resp.Body); err != nil {
		return "", fmt.Errorf("download interrupted: %w", err)
	}
	_ = outF.Close()

	_ = os.Remove(targetPath)
	if err := os.Rename(tempPath, targetPath); err != nil {
		return "", fmt.Errorf("failed to save Android app to target path: %w", err)
	}

	return targetPath, nil
}

