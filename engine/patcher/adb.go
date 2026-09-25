package patcher

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	ErrSignatureMismatch = errors.New("signatures do not match: please uninstall the original package first")
	ErrDeviceNotFound    = errors.New("target device not found or offline")
	ErrAppNotInstalled   = errors.New("target application is not installed on device")
)

type AdbDevice struct {
	Serial  string `json:"serial"`
	State   string `json:"state"` // "device", "unauthorized", "offline", "no_permissions"
	Model   string `json:"model"`
	Product string `json:"product"`
}

type DeviceAppInfo struct {
	PackageName string   `json:"package_name"`
	VersionName string   `json:"version_name"`
	RemotePaths []string `json:"remote_paths"`
	IsSplit     bool     `json:"is_split"`
	TotalApks   int      `json:"total_apks"`
}

// FindAdb searches for adb executable across custom directories, tools dir, PATH, and common SDK locations.
func FindAdb(toolsDir, exeDir string) (string, error) {
	adbExe := "adb"
	if runtime.GOOS == "windows" {
		adbExe = "adb.exe"
	}

	// 1. Look in toolsDir / platform-tools
	candidates := []string{
		filepath.Join(toolsDir, "platform-tools", adbExe),
		filepath.Join(toolsDir, adbExe),
	}

	// 2. Look in exeDir / tools / android / platform-tools
	if exeDir != "" && exeDir != toolsDir {
		candidates = append(candidates,
			filepath.Join(exeDir, "tools", "android", "platform-tools", adbExe),
			filepath.Join(exeDir, "tools", "android", adbExe),
			filepath.Join(exeDir, "platform-tools", adbExe),
			filepath.Join(exeDir, adbExe),
		)
	}

	// 3. Look in common OS specific paths
	if !isSystemToolsDisabled() {
		switch runtime.GOOS {
		case "windows":
			candidates = append(candidates,
				`C:\platform-tools\adb.exe`,
				filepath.Join(os.Getenv("LOCALAPPDATA"), "Android", "Sdk", "platform-tools", "adb.exe"),
				filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local", "Android", "Sdk", "platform-tools", "adb.exe"),
			)
		case "darwin":
			home := os.Getenv("HOME")
			candidates = append(candidates,
				filepath.Join(home, "Library", "Android", "sdk", "platform-tools", "adb"),
				"/opt/homebrew/bin/adb",
				"/usr/local/bin/adb",
			)
		case "linux":
			home := os.Getenv("HOME")
			candidates = append(candidates,
				filepath.Join(home, "Android", "Sdk", "platform-tools", "adb"),
				"/usr/bin/adb",
				"/usr/local/bin/adb",
			)
		}

		for _, c := range candidates {
			if c == "" {
				continue
			}
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c, nil
			}
		}

		// 4. Look in system PATH
		if p, err := exec.LookPath(adbExe); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs, nil
			}
			return p, nil
		}
	} else {
		for _, c := range candidates {
			if c == "" {
				continue
			}
			if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
				return c, nil
			}
		}
	}

	return "", fmt.Errorf("adb executable not found on system")
}

// CheckAdb probes adb path and reports its version string.
func CheckAdb(toolsDir, exeDir string) (bool, string, string, error) {
	adbPath, err := FindAdb(toolsDir, exeDir)
	if err != nil {
		return false, "", "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, adbPath, "version")
	prepareCmd(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, adbPath, "", fmt.Errorf("failed to run adb version: %w", err)
	}

	firstLine := strings.Split(strings.TrimSpace(string(out)), "\n")[0]
	return true, adbPath, firstLine, nil
}

// ListAdbDevices parses `adb devices -l` output into a structured slice of AdbDevice.
func ListAdbDevices(adbPath string) ([]AdbDevice, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, adbPath, "devices", "-l")
	prepareCmd(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to list adb devices: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	lines := strings.Split(string(out), "\n")
	var devices []AdbDevice

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "List of devices") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		serial := fields[0]
		state := fields[1]

		var model string
		var product string
		for _, f := range fields[2:] {
			if strings.HasPrefix(f, "model:") {
				model = strings.TrimPrefix(f, "model:")
			} else if strings.HasPrefix(f, "product:") {
				product = strings.TrimPrefix(f, "product:")
			}
		}

		if model == "" && product != "" {
			model = product
		} else if model == "" {
			model = serial
		}

		devices = append(devices, AdbDevice{
			Serial:  serial,
			State:   state,
			Model:   model,
			Product: product,
		})
	}

	return devices, nil
}

// GetDeviceApp checks if a package is installed on the device and returns its APK locations and version.
func GetDeviceApp(adbPath, serial, packageName string) (*DeviceAppInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. pm path <package>
	cmd := exec.CommandContext(ctx, adbPath, "-s", serial, "shell", "pm", "path", packageName)
	prepareCmd(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pm path failed: %w", err)
	}

	var remotePaths []string
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "package:") {
			p := strings.TrimPrefix(l, "package:")
			p = strings.TrimSpace(p)
			if p != "" {
				remotePaths = append(remotePaths, p)
			}
		}
	}

	if len(remotePaths) == 0 {
		return nil, ErrAppNotInstalled
	}

	// 2. Query package version via dumpsys
	var versionName string
	vCtx, vCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer vCancel()

	vCmd := exec.CommandContext(vCtx, adbPath, "-s", serial, "shell", "dumpsys", "package", packageName)
	prepareCmd(vCmd)
	vOut, vErr := vCmd.CombinedOutput()
	if vErr == nil {
		for _, vLine := range strings.Split(string(vOut), "\n") {
			vLine = strings.TrimSpace(vLine)
			if strings.HasPrefix(vLine, "versionName=") {
				versionName = strings.TrimPrefix(vLine, "versionName=")
				break
			}
		}
	}

	total := len(remotePaths)
	isSplit := total > 1

	return &DeviceAppInfo{
		PackageName: packageName,
		VersionName: versionName,
		RemotePaths: remotePaths,
		IsSplit:     isSplit,
		TotalApks:   total,
	}, nil
}

type InstalledBrowserApp struct {
	PackageName string `json:"package_name"`
	Label       string `json:"label"`
	IsInstalled bool   `json:"is_installed"`
	IsSkyLeap   bool   `json:"is_skyleap"`
}

var knownBrowserCatalog = []struct {
	pkg   string
	label string
}{
	{"com.dena.skyleap", "DeNA SkyLeap (官方推荐)"},
	{"com.android.chrome", "Google Chrome"},
	{"com.microsoft.emmx", "Microsoft Edge"},
	{"mark.via.gp", "Via 浏览器"},
	{"mark.via", "Via 浏览器"},
	{"com.quark.browser", "夸克浏览器"},
	{"com.sec.android.app.sbrowser", "三星浏览器"},
	{"com.brave.browser", "Brave 浏览器"},
	{"com.kiwibrowser.browser", "Kiwi Browser"},
	{"org.mozilla.firefox", "Firefox 火狐浏览器"},
	{"com.opera.browser", "Opera 浏览器"},
	{"com.opera.mini.native", "Opera Mini"},
	{"com.heytap.browser", "OPPO/OnePlus 系统浏览器"},
	{"com.mi.globalbrowser", "小米系统浏览器"},
	{"com.android.browser", "Android 原生浏览器"},
	{"com.vivo.browser", "vivo 系统浏览器"},
	{"com.huawei.browser", "华为系统浏览器"},
	{"com.ucmobile", "UC 浏览器"},
	{"com.UCMobile", "UC 浏览器"},
	{"com.baidu.searchbox", "百度 App / 浏览器"},
}

// ListDeviceBrowsers discovers all browser and SkyLeap packages on the target device.
func ListDeviceBrowsers(adbPath, serial string) ([]InstalledBrowserApp, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, adbPath, "-s", serial, "shell", "pm", "list", "packages")
	prepareCmd(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to query packages on device: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	installedMap := make(map[string]bool)
	var deviceInstalledPkgs []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "package:") {
			pkg := strings.TrimSpace(strings.TrimPrefix(line, "package:"))
			if pkg != "" {
				installedMap[pkg] = true
				deviceInstalledPkgs = append(deviceInstalledPkgs, pkg)
			}
		}
	}

	seen := make(map[string]bool)
	var results []InstalledBrowserApp

	// 1. SkyLeap or SkyLeap clones on the device
	if installedMap["com.dena.skyleap"] {
		results = append(results, InstalledBrowserApp{
			PackageName: "com.dena.skyleap",
			Label:       "DeNA SkyLeap (官方推荐 · 已安装)",
			IsInstalled: true,
			IsSkyLeap:   true,
		})
		seen["com.dena.skyleap"] = true
	}

	for _, pkg := range deviceInstalledPkgs {
		if strings.Contains(strings.ToLower(pkg), "skyleap") && !seen[pkg] {
			results = append(results, InstalledBrowserApp{
				PackageName: pkg,
				Label:       fmt.Sprintf("SkyLeap (%s · 已安装)", pkg),
				IsInstalled: true,
				IsSkyLeap:   true,
			})
			seen[pkg] = true
		}
	}

	// 2. Known browsers installed on the device
	for _, def := range knownBrowserCatalog {
		if installedMap[def.pkg] && !seen[def.pkg] {
			results = append(results, InstalledBrowserApp{
				PackageName: def.pkg,
				Label:       fmt.Sprintf("%s (%s · 已安装)", def.label, def.pkg),
				IsInstalled: true,
				IsSkyLeap:   false,
			})
			seen[def.pkg] = true
		}
	}

	// 3. Other packages containing "browser" installed on the device
	for _, pkg := range deviceInstalledPkgs {
		pLower := strings.ToLower(pkg)
		if strings.Contains(pLower, "browser") && !seen[pkg] {
			results = append(results, InstalledBrowserApp{
				PackageName: pkg,
				Label:       fmt.Sprintf("浏览器应用 (%s · 已安装)", pkg),
				IsInstalled: true,
				IsSkyLeap:   false,
			})
			seen[pkg] = true
		}
	}

	// 4. Other common browsers that are NOT installed on device
	for _, def := range knownBrowserCatalog {
		if !seen[def.pkg] {
			results = append(results, InstalledBrowserApp{
				PackageName: def.pkg,
				Label:       fmt.Sprintf("%s (%s · 未安装)", def.label, def.pkg),
				IsInstalled: false,
				IsSkyLeap:   def.pkg == "com.dena.skyleap",
			})
			seen[def.pkg] = true
		}
	}

	return results, nil
}

// PullDeviceApp pulls all APK parts of a package from the device into targetDir.
func PullDeviceApp(
	ctx context.Context,
	adbPath string,
	serial string,
	app *DeviceAppInfo,
	targetDir string,
	onProgress func(cur, total int, file string),
) ([]string, error) {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory: %w", err)
	}

	var localPaths []string
	total := len(app.RemotePaths)

	for i, remotePath := range app.RemotePaths {
		baseName := filepath.Base(remotePath)
		// Clean up carriage returns from adb shell if any
		baseName = strings.TrimRight(baseName, "\r\n")
		localPath := filepath.Join(targetDir, baseName)

		if onProgress != nil {
			onProgress(i+1, total, baseName)
		}

		cmd := exec.CommandContext(ctx, adbPath, "-s", serial, "pull", remotePath, localPath)
		prepareCmd(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("failed to pull %s: %w (%s)", baseName, err, strings.TrimSpace(string(out)))
		}

		// Verify downloaded file
		if fi, err := os.Stat(localPath); err != nil || fi.Size() == 0 {
			return nil, fmt.Errorf("pulled file %s is empty or inaccessible", baseName)
		}

		localPaths = append(localPaths, localPath)
	}

	return localPaths, nil
}

// InstallToDevice installs single or split APKs to the target device.
func InstallToDevice(
	ctx context.Context,
	adbPath string,
	serial string,
	isSplit bool,
	apkPaths []string,
) (string, error) {
	if len(apkPaths) == 0 {
		return "", fmt.Errorf("no APK paths provided for installation")
	}

	var cmd *exec.Cmd
	if isSplit && len(apkPaths) > 1 {
		args := []string{"-s", serial, "install-multiple", "-r"}
		args = append(args, apkPaths...)
		cmd = exec.CommandContext(ctx, adbPath, args...)
		prepareCmd(cmd)
	} else {
		cmd = exec.CommandContext(ctx, adbPath, "-s", serial, "install", "-r", apkPaths[0])
		prepareCmd(cmd)
	}

	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))

	if err != nil || strings.Contains(outStr, "Failure") {
		if strings.Contains(outStr, "INSTALL_FAILED_UPDATE_INCOMPATIBLE") ||
			strings.Contains(outStr, "INSTALL_FAILED_SHARED_USER_INCOMPATIBLE") ||
			strings.Contains(outStr, "signatures do not match") {
			return outStr, ErrSignatureMismatch
		}
		return outStr, fmt.Errorf("installation failed: %s", outStr)
	}

	return outStr, nil
}

// UninstallFromDevice uninstalls a package from the device.
func UninstallFromDevice(ctx context.Context, adbPath, serial, packageName string) error {
	cmd := exec.CommandContext(ctx, adbPath, "-s", serial, "uninstall", packageName)
	prepareCmd(cmd)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil || (!strings.Contains(outStr, "Success") && !strings.Contains(outStr, "not installed")) {
		return fmt.Errorf("uninstall failed: %s (%w)", outStr, err)
	}
	return nil
}

// PlatformToolsDownloadURL returns our project GitHub Release asset URL for the current OS.
func PlatformToolsDownloadURL() string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/platform-tools-%s.zip", AssetsRepo, AssetsTag, runtime.GOOS)
}

// PlatformToolsGoogleFallbackURL returns Google's official mirror URL as secondary fallback.
func PlatformToolsGoogleFallbackURL() string {
	switch runtime.GOOS {
	case "darwin":
		return "https://dl.google.com/android/repository/platform-tools-latest-darwin.zip"
	case "linux":
		return "https://dl.google.com/android/repository/platform-tools-latest-linux.zip"
	default:
		return "https://dl.google.com/android/repository/platform-tools-latest-windows.zip"
	}
}

// DownloadPlatformTools downloads the platform-tools archive and extracts adb into toolsDir/platform-tools.
// It prioritizes our project's GitHub Release asset and automatically falls back to Google's official mirror.
func DownloadPlatformTools(
	ctx context.Context,
	toolsDir string,
	customURL string,
	progressFn func(curBytes, totalBytes int64, percent float64),
) (string, error) {
	var candidateURLs []string
	if customURL != "" {
		candidateURLs = []string{customURL}
	} else {
		candidateURLs = []string{PlatformToolsDownloadURL()}
	}

	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create tools dir: %w", err)
	}

	var resp *http.Response
	var lastErr error
	client := buildDownloadHTTPClient("")

	for _, dlURL := range candidateURLs {
		req, err := http.NewRequestWithContext(ctx, "GET", dlURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		r, err := client.Do(req)
		if err == nil && r.StatusCode == http.StatusOK {
			resp = r
			break
		}
		if r != nil {
			_ = r.Body.Close()
			lastErr = fmt.Errorf("HTTP %d from %s", r.StatusCode, dlURL)
		} else {
			lastErr = err
		}
	}

	if resp == nil {
		return "", fmt.Errorf("failed to download platform-tools from available sources: %w", lastErr)
	}
	defer resp.Body.Close()

	tmpZip := filepath.Join(toolsDir, "platform-tools.tmp.zip")
	defer os.Remove(tmpZip)

	f, err := os.Create(tmpZip)
	if err != nil {
		return "", err
	}

	totalBytes := resp.ContentLength
	var downloadedBytes int64
	buf := make([]byte, 64*1024)

	for {
		select {
		case <-ctx.Done():
			f.Close()
			return "", ctx.Err()
		default:
		}

		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				f.Close()
				return "", wErr
			}
			downloadedBytes += int64(n)
			if progressFn != nil && totalBytes > 0 {
				progressFn(downloadedBytes, totalBytes, float64(downloadedBytes)/float64(totalBytes)*100.0)
			}
		}

		if rErr != nil {
			if rErr == io.EOF {
				break
			}
			f.Close()
			return "", rErr
		}
	}
	_ = f.Close()

	// Extract zip archive into toolsDir
	zr, err := zip.OpenReader(tmpZip)
	if err != nil {
		return "", fmt.Errorf("failed to open downloaded zip: %w", err)
	}
	defer zr.Close()

	for _, file := range zr.File {
		cleanName := filepath.Clean(file.Name)
		if strings.Contains(cleanName, "..") {
			continue
		}

		destPath := filepath.Join(toolsDir, cleanName)
		if file.FileInfo().IsDir() {
			_ = os.MkdirAll(destPath, 0755)
			continue
		}

		_ = os.MkdirAll(filepath.Dir(destPath), 0755)
		outFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return "", fmt.Errorf("failed to create extracted file %s: %w", destPath, err)
		}

		rc, err := file.Open()
		if err != nil {
			outFile.Close()
			return "", fmt.Errorf("failed to read zip entry %s: %w", file.Name, err)
		}

		_, err = io.Copy(outFile, rc)
		_ = rc.Close()
		_ = outFile.Close()
		if err != nil {
			return "", fmt.Errorf("failed to extract file %s: %w", destPath, err)
		}
	}

	adbExe := "adb"
	if runtime.GOOS == "windows" {
		adbExe = "adb.exe"
	}

	finalAdb := filepath.Join(toolsDir, "platform-tools", adbExe)
	if _, err := os.Stat(finalAdb); err != nil {
		return "", fmt.Errorf("extracted platform-tools does not contain %s", adbExe)
	}

	return finalAdb, nil
}

// CleanAdbOutput parses lines removing CR/LF.
func CleanAdbOutput(output []byte) string {
	return string(bytes.TrimSpace(bytes.ReplaceAll(output, []byte("\r"), nil)))
}
