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

type BrowserEngineType string

const (
	EngineTypeWebView     BrowserEngineType = "webview"
	EngineTypeChromium    BrowserEngineType = "chromium"
	EngineTypeUnsupported BrowserEngineType = "unsupported"
)

type ApkPackageInfo struct {
	IsSplit       bool              `json:"is_split"`
	BaseApkPath   string            `json:"base_apk_path"`
	SplitApkPaths []string          `json:"split_apk_paths"`
	PackageName   string            `json:"package_name"`
	VersionName   string            `json:"version_name"`
	TotalApks     int               `json:"total_apks"`
	EngineType    BrowserEngineType `json:"engine_type"`
	EngineName    string            `json:"engine_name"`
}

var (
	semverRegex = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)
)

// DetectEngineFromZip inspects an APK zip structure for known native libraries or package names
// to determine whether the app is a system WebView browser, standalone Chromium, or unsupported engine.
func DetectEngineFromZip(zr *zip.Reader, pkgName string) (BrowserEngineType, string) {
	for _, f := range zr.File {
		nameLower := strings.ToLower(f.Name)
		baseName := filepath.Base(nameLower)
		if strings.HasPrefix(nameLower, "lib/") {
			if strings.Contains(baseName, "monochrome") ||
				strings.Contains(baseName, "libchrome") ||
				strings.Contains(baseName, "libkiwi") ||
				strings.Contains(baseName, "libbrave") ||
				strings.Contains(baseName, "libedge") ||
				strings.Contains(baseName, "cronet") {
				return EngineTypeChromium, "Chromium 独立内核 (Chrome / Kiwi 等)"
			}
			if strings.Contains(baseName, "libxul") || strings.Contains(baseName, "libmozglue") {
				return EngineTypeUnsupported, "Gecko 内核 (Firefox 等)"
			}
		}
	}

	if pkgName != "" {
		return DetectEngineFromPackageName(pkgName)
	}

	return EngineTypeWebView, "Android 系统 WebView"
}

// DetectEngineFromPackageName inspects known browser package prefixes.
func DetectEngineFromPackageName(pkgName string) (BrowserEngineType, string) {
	lowerPkg := strings.ToLower(pkgName)
	if strings.HasPrefix(lowerPkg, "com.android.chrome") ||
		strings.HasPrefix(lowerPkg, "com.chrome.") ||
		strings.HasPrefix(lowerPkg, "org.chromium.") ||
		strings.HasPrefix(lowerPkg, "com.kiwibrowser.") ||
		strings.HasPrefix(lowerPkg, "com.microsoft.emmx") ||
		strings.HasPrefix(lowerPkg, "com.brave.browser") ||
		strings.HasPrefix(lowerPkg, "com.opera.") ||
		strings.HasPrefix(lowerPkg, "com.vivaldi.browser") {
		return EngineTypeChromium, "Chromium 独立内核 (Chrome / Kiwi 等)"
	}

	if strings.HasPrefix(lowerPkg, "org.mozilla.") ||
		strings.HasPrefix(lowerPkg, "org.torproject.") {
		return EngineTypeUnsupported, "Gecko 内核 (Firefox 等)"
	}

	return EngineTypeWebView, "Android 系统 WebView"
}

// InspectInput analyzes the user's input path (single .apk, .apks/.xapk/.zip archive, or directory).
// If an archive (.apks/.xapk/.zip) is provided, it unpacks it into workDir/unpacked.
func InspectInput(inputPath string, workDir string) (*ApkPackageInfo, error) {
	fi, err := os.Stat(inputPath)
	if err != nil {
		return nil, fmt.Errorf("input path not accessible: %w", err)
	}

	if fi.IsDir() {
		return inspectDirectory(inputPath)
	}

	lowerExt := strings.ToLower(filepath.Ext(inputPath))
	switch lowerExt {
	case ".apk":
		return inspectSingleApk(inputPath)
	case ".apks", ".xapk", ".zip":
		unpackedDir := filepath.Join(workDir, "unpacked_input")
		if err := os.MkdirAll(unpackedDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create unpacked directory: %w", err)
		}
		if err := unzipArchive(inputPath, unpackedDir); err != nil {
			return nil, fmt.Errorf("failed to extract bundle %s: %w", filepath.Base(inputPath), err)
		}
		return inspectDirectory(unpackedDir)
	default:
		return nil, fmt.Errorf("unsupported file format %q (expected .apk, .apks, .xapk, or directory)", lowerExt)
	}
}

func inspectSingleApk(apkPath string) (*ApkPackageInfo, error) {
	pkg, ver, _, engType, engName, err := parseApkMetadata(apkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse APK %s: %w", filepath.Base(apkPath), err)
	}

	return &ApkPackageInfo{
		IsSplit:       false,
		BaseApkPath:   apkPath,
		SplitApkPaths: nil,
		PackageName:   pkg,
		VersionName:   ver,
		TotalApks:     1,
		EngineType:    engType,
		EngineName:    engName,
	}, nil
}

func inspectDirectory(dirPath string) (*ApkPackageInfo, error) {
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
		return inspectSingleApk(apkFiles[0])
	}

	// Multiple APKs: Identify base.apk vs splits and aggregate engine detection
	var baseApk string
	var splitApks []string
	var foundPackage string
	var foundVersion string
	var foundEngineType BrowserEngineType = EngineTypeWebView
	var foundEngineName string = "Android 系统 WebView"

	for _, apk := range apkFiles {
		baseName := strings.ToLower(filepath.Base(apk))
		pkg, ver, isSplit, engType, engName, _ := parseApkMetadata(apk)

		if pkg != "" && foundPackage == "" {
			foundPackage = pkg
		}
		if ver != "" && foundVersion == "" {
			foundVersion = ver
		}
		if engType == EngineTypeChromium {
			foundEngineType = EngineTypeChromium
			foundEngineName = engName
		} else if engType == EngineTypeUnsupported && foundEngineType != EngineTypeChromium {
			foundEngineType = EngineTypeUnsupported
			foundEngineName = engName
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

	if foundEngineType == EngineTypeWebView && foundPackage != "" {
		pkgType, pkgEngName := DetectEngineFromPackageName(foundPackage)
		if pkgType != EngineTypeWebView {
			foundEngineType = pkgType
			foundEngineName = pkgEngName
		}
	}

	return &ApkPackageInfo{
		IsSplit:       true,
		BaseApkPath:   baseApk,
		SplitApkPaths: splitApks,
		PackageName:   foundPackage,
		VersionName:   foundVersion,
		TotalApks:     len(apkFiles),
		EngineType:    foundEngineType,
		EngineName:    foundEngineName,
	}, nil
}

func parseApkMetadata(apkPath string) (pkgName string, versionName string, isSplit bool, engineType BrowserEngineType, engineName string, err error) {
	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return "", "", false, EngineTypeWebView, "", err
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
		return "", "", false, EngineTypeWebView, "", fmt.Errorf("AndroidManifest.xml not found in APK")
	}

	rc, err := manifestFile.Open()
	if err != nil {
		return "", "", false, EngineTypeWebView, "", err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", "", false, EngineTypeWebView, "", err
	}

	info, err := ParseManifest(data)
	if err != nil {
		return "", "", false, EngineTypeWebView, "", err
	}

	engType, engName := DetectEngineFromZip(&zr.Reader, info.PackageName)
	return info.PackageName, info.VersionName, info.IsSplit, engType, engName, nil
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
