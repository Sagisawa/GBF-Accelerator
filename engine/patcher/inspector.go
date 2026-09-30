package patcher

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ApkPackageInfo struct {
	IsSplit             bool     `json:"is_split"`
	BaseApkPath         string   `json:"base_apk_path"`
	SplitApkPaths       []string `json:"split_apk_paths"`
	PackageName         string   `json:"package_name"`
	VersionName         string   `json:"version_name"`
	TotalApks           int      `json:"total_apks"`
	IsSystemWebView     bool     `json:"is_system_webview"`
	EngineDesc          string   `json:"engine_desc"`
	UnsupportedReason   string   `json:"unsupported_reason,omitempty"`
	IsAlreadyPatched    bool     `json:"is_already_patched"`
	PatchType           string   `json:"patch_type,omitempty"`
	OriginalVersionName string   `json:"original_version_name,omitempty"`
}

var standaloneEngines = []struct {
	pattern string
	engine  string
}{
	{"libmonochrome", "Chromium (Monochrome)"},
	{"libchrome.so", "Chromium"},
	{"libkiwi.so", "Chromium (Kiwi)"},
	{"libbrave.so", "Chromium (Brave)"},
	{"libmsedge.so", "Chromium (Edge)"},
	{"libedge.so", "Chromium (Edge)"},
	{"libxul.so", "Gecko (Firefox)"},
	{"libgeckoview.so", "Gecko (Firefox)"},
}

var standalonePackages = map[string]string{
	"com.android.chrome":           "Google Chrome",
	"com.chrome.beta":              "Chrome Beta",
	"com.chrome.dev":               "Chrome Dev",
	"com.chrome.canary":            "Chrome Canary",
	"com.kiwibrowser.browser":      "Kiwi Browser",
	"com.brave.browser":            "Brave Browser",
	"com.microsoft.emmx":           "Microsoft Edge",
	"com.sec.android.app.sbrowser": "三星浏览器 (Samsung Internet)",
	"org.mozilla.firefox":          "Firefox 火狐浏览器",
	"org.mozilla.fenix":            "Firefox Fenix",
	"org.mozilla.focus":            "Firefox Focus",
	"org.torproject.torbrowser":    "Tor Browser",
	"com.opera.browser":            "Opera 浏览器",
	"com.opera.mini.native":        "Opera Mini",
	"com.opera.touch":              "Opera Touch",
	"com.opera.gx":                 "Opera GX",
	"com.yandex.browser":           "Yandex 浏览器",
}

// CheckIsSystemWebView inspects the given APK paths and package name to verify if it is an Android System WebView browser.
func CheckIsSystemWebView(apkPaths []string, pkgName string) (isWebView bool, engineDesc string, unsupportedReason string) {
	if brand, ok := standalonePackages[pkgName]; ok {
		return false, fmt.Sprintf("独立浏览器内核 (%s · 暂不支持)", brand), fmt.Sprintf("检测到 %s 采用独立浏览器内核，暂不支持。GBF 补丁仅支持基于 Android 系统原生 WebView 的浏览器（如 SkyLeap、Via 等）。", brand)
	}

	for _, apk := range apkPaths {
		zr, err := zip.OpenReader(apk)
		if err != nil {
			continue
		}
		for _, f := range zr.File {
			nameLower := strings.ToLower(f.Name)
			for _, eng := range standaloneEngines {
				if strings.Contains(nameLower, eng.pattern) {
					_ = zr.Close()
					return false, fmt.Sprintf("独立浏览器内核 (%s · 暂不支持)", eng.engine), fmt.Sprintf("检测到安装包内包含独立浏览器引擎 (%s: %s)，暂不支持。GBF 补丁仅支持基于 Android 系统原生 WebView 的浏览器（如 SkyLeap、Via 等）。", eng.engine, filepath.Base(f.Name))
				}
			}
		}
		_ = zr.Close()
	}

	if pkgName == "com.dena.skyleap" {
		return true, "Android 系统原生 WebView (SkyLeap 官方认证 · 支持)", ""
	}
	return true, "Android 系统原生 WebView (支持)", ""
}

var (
	semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)
)

// InspectInput analyzes the user's input path (single .apk, .apks/.xapk/.zip archive, or directory).
// If an archive (.apks/.xapk/.zip) is provided, it unpacks it into workDir/unpacked.
func InspectInput(inputPath string, workDir string) (*ApkPackageInfo, error) {
	fi, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("input path not accessible: %w", err)
	}

	if fi.IsDir() {
		return inspectDirectory(inputPath, workDir)
	}

	lowerExt := strings.ToLower(filepath.Ext(inputPath))
	switch lowerExt {
	case ".apk":
		return inspectSingleApk(inputPath, workDir)
	case ".apks", ".xapk", ".zip":
		unpackedDir := filepath.Join(workDir, "unpacked_input")
		if err := os.MkdirAll(unpackedDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create unpacked directory: %w", err)
		}
		if err := unzipArchive(inputPath, unpackedDir); err != nil {
			return nil, fmt.Errorf("failed to extract bundle %s: %w", filepath.Base(inputPath), err)
		}
		return inspectDirectory(unpackedDir, workDir)
	default:
		return nil, fmt.Errorf("unsupported file format %q (expected .apk, .apks, .xapk, or directory)", lowerExt)
	}
}

func detectLSPatchFromApk(apkPath string, workDir string) (isPatched bool, patchType string, origPkg string, origVer string) {
	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return false, "", "", ""
	}
	defer zr.Close()

	hasOrigin := false
	hasConfig := false
	hasModules := false
	hasLoader := false

	for _, f := range zr.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}
		switch {
		case f.Name == "assets/lspatch/origin.apk":
			hasOrigin = true
		case f.Name == "assets/lspatch/config.json":
			hasConfig = true
		case strings.HasPrefix(f.Name, "assets/lspatch/modules/"):
			hasModules = true
		case f.Name == "assets/lspatch/loader.dex" || strings.HasPrefix(f.Name, "assets/lspatch/so/"):
			hasLoader = true
		}
	}

	if !hasOrigin {
		return false, "", "", ""
	}

	targetWorkDir := workDir
	shouldCleanupWorkDir := false
	if strings.TrimSpace(targetWorkDir) == "" {
		if t, err := os.MkdirTemp("", "gbf_inspect_origin_*"); err == nil {
			targetWorkDir = t
			shouldCleanupWorkDir = true
		}
	}
	if shouldCleanupWorkDir {
		defer os.RemoveAll(targetWorkDir)
	}

	if targetWorkDir != "" {
		tempOrigin := filepath.Join(targetWorkDir, fmt.Sprintf("origin_probe_%d.apk", os.Getpid()))
		if err := ExtractOriginFromApk(apkPath, tempOrigin); err == nil {
			defer os.Remove(tempOrigin)
			if manifest, mErr := parseApkManifestInfo(tempOrigin); mErr == nil && manifest != nil {
				return true, "lspatch", manifest.PackageName, manifest.VersionName
			}
		}
	}

	if hasConfig || hasModules || hasLoader {
		return true, "lspatch", "", ""
	}

	return false, "", "", ""
}

func inspectSingleApk(apkPath string, workDir string) (*ApkPackageInfo, error) {
	pkg, ver, _, err := parseApkMetadata(apkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse APK %s: %w", filepath.Base(apkPath), err)
	}

	isPatched, patchType, origPkg, origVer := detectLSPatchFromApk(apkPath, workDir)

	effectivePkg := pkg
	if isPatched && origPkg != "" {
		effectivePkg = origPkg
	}

	isWebView, engineDesc, unsuppReason := CheckIsSystemWebView([]string{apkPath}, effectivePkg)

	return &ApkPackageInfo{
		IsSplit:             false,
		BaseApkPath:         apkPath,
		SplitApkPaths:       nil,
		PackageName:         effectivePkg,
		VersionName:         ver,
		TotalApks:           1,
		IsSystemWebView:     isWebView,
		EngineDesc:          engineDesc,
		UnsupportedReason:   unsuppReason,
		IsAlreadyPatched:    isPatched,
		PatchType:           patchType,
		OriginalVersionName: origVer,
	}, nil
}

func inspectDirectory(dirPath string, workDir string) (*ApkPackageInfo, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	var apkFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".apk") {
			apkFiles = append(apkFiles, filepath.Join(dirPath, e.Name()))
		}
	}

	if len(apkFiles) == 0 {
		return nil, fmt.Errorf("no .apk files found in %s", dirPath)
	}

	if len(apkFiles) == 1 {
		return inspectSingleApk(apkFiles[0], workDir)
	}

	// Multiple APKs: Identify base.apk vs splits
	var baseApk string
	var splitApks []string
	var foundPackage string
	var foundVersion string

	for _, apk := range apkFiles {
		baseName := strings.ToLower(filepath.Base(apk))
		pkg, ver, isSplit, _ := parseApkMetadata(apk)

		if pkg != "" && foundPackage == "" {
			foundPackage = pkg
		}
		if ver != "" && foundVersion == "" {
			foundVersion = ver
		}

		if baseName == "base.apk" || (!isSplit && baseApk == "") {
			if baseApk != "" {
				splitApks = append(splitApks, baseApk)
			}
			baseApk = apk
		} else {
			splitApks = append(splitApks, apk)
		}
	}

	if baseApk == "" {
		// Fallback: pick the first APK as base
		baseApk = apkFiles[0]
		splitApks = apkFiles[1:]
	}

	isPatched, patchType, origPkg, origVer := detectLSPatchFromApk(baseApk, workDir)
	if !isPatched {
		for _, sp := range splitApks {
			if ip, pt, op, ov := detectLSPatchFromApk(sp, workDir); ip {
				isPatched, patchType, origPkg, origVer = ip, pt, op, ov
				break
			}
		}
	}

	effectivePkg := foundPackage
	if isPatched && origPkg != "" {
		effectivePkg = origPkg
	}

	allApks := append([]string{baseApk}, splitApks...)
	isWebView, engineDesc, unsuppReason := CheckIsSystemWebView(allApks, effectivePkg)

	return &ApkPackageInfo{
		IsSplit:             true,
		BaseApkPath:         baseApk,
		SplitApkPaths:       splitApks,
		PackageName:         effectivePkg,
		VersionName:         foundVersion,
		TotalApks:           len(apkFiles),
		IsSystemWebView:     isWebView,
		EngineDesc:          engineDesc,
		UnsupportedReason:   unsuppReason,
		IsAlreadyPatched:    isPatched,
		PatchType:           patchType,
		OriginalVersionName: origVer,
	}, nil
}

func parseApkManifestInfo(apkPath string) (*ManifestInfo, error) {
	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	var manifestFile *zip.File
	for _, f := range zr.File {
		if f.Name == "AndroidManifest.xml" {
			manifestFile = f
			break
		}
	}

	if manifestFile == nil {
		return nil, fmt.Errorf("AndroidManifest.xml not found in APK %s", filepath.Base(apkPath))
	}

	rc, err := manifestFile.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}

	return ParseManifest(data)
}

func parseApkMetadata(apkPath string) (pkgName string, versionName string, isSplit bool, err error) {
	info, err := parseApkManifestInfo(apkPath)
	if err != nil {
		return "", "", false, err
	}
	return info.PackageName, info.VersionName, info.IsSplit, nil
}

func IsValidPackageName(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 {
			return false
		}
		for i, r := range p {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '_' && (i == 0 || (r < '0' || r > '9')) {
				return false
			}
		}
	}
	return true
}

func isValidPackageName(s string) bool {
	return IsValidPackageName(s)
}

func unzipArchive(srcZip, destDir string) error {
	zr, err := zip.OpenReader(srcZip)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, f := range zr.File {
		// Prevent Zip Slip
		cleanPath := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanPath, "..") || filepath.IsAbs(cleanPath) {
			continue
		}
		targetPath := filepath.Join(destDir, cleanPath)

		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
