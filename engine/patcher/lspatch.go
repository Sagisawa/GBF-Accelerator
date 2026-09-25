package patcher

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"gbf-proxy/config"
)

var (
	javaVersionRegex = regexp.MustCompile(`(?:openjdk|java) version "(\d+)(?:\.(\d+))?`)
)

type LSPatchConfig struct {
	JavaBinaryPath string
	LSPatchJarPath string
	ModuleApkPath  string
	NewPackageName string
	Verbose        bool
	LogFn          func(string)
}

// FindJavaRuntime discovers a Java binary with major version >= 21.
// Checks override path, JAVA_HOME, Android Studio JBR, and PATH.
func FindJavaRuntime(overridePath string) (path string, version string, err error) {
	candidates := make([]string, 0)
	if overridePath != "" {
		candidates = append(candidates, overridePath)
	}

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if runtime.GOOS == "windows" {
			candidates = append(candidates,
				filepath.Join(exeDir, "jre", "bin", "java.exe"),
				filepath.Join(exeDir, "tools", "jre", "bin", "java.exe"),
				filepath.Join(exeDir, "jdk", "bin", "java.exe"),
				filepath.Join(exeDir, "tools", "jdk", "bin", "java.exe"),
			)
		} else {
			candidates = append(candidates,
				filepath.Join(exeDir, "jre", "bin", "java"),
				filepath.Join(exeDir, "tools", "jre", "bin", "java"),
				filepath.Join(exeDir, "jdk", "bin", "java"),
			)
		}
	}

	baseDir := config.GetBaseDir()
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			filepath.Join(baseDir, "jre", "bin", "java.exe"),
			filepath.Join(baseDir, "tools", "jre", "bin", "java.exe"),
		)
	} else {
		candidates = append(candidates,
			filepath.Join(baseDir, "jre", "bin", "java"),
			filepath.Join(baseDir, "tools", "jre", "bin", "java"),
		)
	}

	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		binName := "java"
		if runtime.GOOS == "windows" {
			binName = "java.exe"
		}
		candidates = append(candidates, filepath.Join(jh, "bin", binName))
	}

	if runtime.GOOS == "windows" {
		candidates = append(candidates, `C:\Program Files\Android\Android Studio\jbr\bin\java.exe`)
		candidates = append(candidates, `C:\Program Files\Android\Android Studio Preview\jbr\bin\java.exe`)
	} else if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Android Studio.app/Contents/jbr/Contents/Home/bin/java")
	}

	// PATH lookup
	if path, err := exec.LookPath("java"); err == nil {
		candidates = append(candidates, path)
	}

	var checkedErrors []string
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			major, verStr, err := checkJavaVersion(c)
			if err == nil {
				if major >= 21 {
					return c, verStr, nil
				}
				checkedErrors = append(checkedErrors, fmt.Sprintf("%s (version %s is below required Java 21)", c, verStr))
			} else {
				checkedErrors = append(checkedErrors, fmt.Sprintf("%s (%v)", c, err))
			}
		}
	}

	if len(checkedErrors) > 0 {
		return "", "", fmt.Errorf("no compatible Java 21+ runtime found. Checked:\n  - %s\nLSPatch requires Java 21 or newer (OpenJDK 21+). You can specify a custom path with --java", strings.Join(checkedErrors, "\n  - "))
	}
	return "", "", fmt.Errorf("java executable not found on system. Please install JDK 21+ or specify --java")
}

func checkJavaVersion(javaPath string) (int, string, error) {
	cmd := exec.Command(javaPath, "-version")
	prepareCmd(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, "", fmt.Errorf("failed to run java -version: %w", err)
	}

	outStr := string(out)
	matches := javaVersionRegex.FindStringSubmatch(outStr)
	if len(matches) < 2 {
		return 0, "", fmt.Errorf("unable to parse java version from: %s", strings.TrimSpace(outStr))
	}

	majorStr := matches[1]
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return 0, "", fmt.Errorf("invalid major version %q: %w", majorStr, err)
	}

	// Legacy Java 1.8 format check
	if major == 1 && len(matches) >= 3 && matches[2] != "" {
		minor, _ := strconv.Atoi(matches[2])
		major = minor
	}

	return major, strings.TrimSpace(matches[0]), nil
}

const (
	// CanonicalLSPatchVersion is the pinned version of LSPatch verified on Android 16 / OnePlus 13.
	CanonicalLSPatchVersion = "v1.2 (Build 487)"
	// CanonicalLSPatchSource is the authoritative upstream source for the pinned build.
	CanonicalLSPatchSource = "https://github.com/JingMatrix/LSPatch"
	// CanonicalLSPatchSHA256 is the cryptographic SHA-256 checksum of the verified lspatch.jar.
	CanonicalLSPatchSHA256 = "d238fdc414d121b7fa454d8b4ccf420df3a8c97d563761861ff92bd9c5da2165"
)

// ValidateLSPatchJar verifies that the target lspatch.jar exists, is a regular file,
// and strictly matches the pinned SHA-256 checksum of JingMatrix/LSPatch v1.2 (Build 487).
func ValidateLSPatchJar(jarPath string) error {
	fi, err := os.Stat(jarPath)
	if err != nil {
		return fmt.Errorf("lspatch.jar not accessible: %w", err)
	}
	if fi.IsDir() || fi.Size() == 0 {
		return fmt.Errorf("lspatch.jar is a directory or empty: %s", jarPath)
	}

	f, err := os.Open(jarPath)
	if err != nil {
		return fmt.Errorf("failed to open lspatch.jar for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to compute lspatch.jar checksum: %w", err)
	}

	actualSHA256 := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actualSHA256, CanonicalLSPatchSHA256) {
		return fmt.Errorf("LSPatch binary validation failed: SHA-256 checksum mismatch for %s\n"+
			"  Expected: %s (%s, %s)\n"+
			"  Actual:   %s\n"+
			"Security policy: Execution halted to prevent using unverified or tampered LSPatch binaries.",
			jarPath, CanonicalLSPatchSHA256, CanonicalLSPatchVersion, CanonicalLSPatchSource, actualSHA256)
	}

	return nil
}

// FindLSPatchJar discovers lspatch.jar from override, local directories, or project repository.
func FindLSPatchJar(overridePath string, exeDir string) (string, error) {
	if overridePath != "" {
		if fi, err := os.Stat(overridePath); err != nil || fi.IsDir() {
			return "", fmt.Errorf("specified lspatch.jar not found or inaccessible: %s", overridePath)
		}
		abs, _ := filepath.Abs(overridePath)
		return abs, nil
	}

	toolsDir := GetAndroidToolsDir()
	candidates := []string{
		filepath.Join(toolsDir, "lspatch.jar"),
		filepath.Join(exeDir, "tools", "android", "lspatch.jar"),
		filepath.Join(exeDir, "tools", "lspatch.jar"),
		filepath.Join(exeDir, "lspatch.jar"),
		filepath.Join(exeDir, "lib", "lspatch.jar"),
		filepath.Join(exeDir, "..", "..", "build", "lspatch", "lspatch.jar"),
		filepath.Join(exeDir, "..", "build", "lspatch", "lspatch.jar"),
		filepath.Join("build", "lspatch", "lspatch.jar"),
	}

	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Size() > 0 {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	return "", fmt.Errorf("lspatch.jar not found. Please place lspatch.jar (%s) in the tool directory or specify --lspatch <path>", CanonicalLSPatchVersion)
}

// FindModuleApk discovers the built SkyLeap Xposed Module APK following the formal hierarchy:
// 1. User explicit override (--module)
// 2. Production release module in tool directory or companion subdirectories (tools/android/xposed-release.apk, etc.)
// 3. Source tree release artifact (android/xposed/build/outputs/apk/release/xposed-release.apk)
// 4. Development environment debug fallback (xposed-debug.apk)
func FindModuleApk(overridePath string, exeDir string) (string, error) {
	if overridePath != "" {
		if fi, err := os.Stat(overridePath); err != nil || fi.IsDir() {
			return "", fmt.Errorf("specified module APK not found or inaccessible: %s", overridePath)
		}
		abs, _ := filepath.Abs(overridePath)
		return abs, nil
	}

	toolsDir := GetAndroidToolsDir()

	// 1. Production release module in companion subdirectories or tool directory
	productionCandidates := []string{
		filepath.Join(toolsDir, "xposed-release.apk"),
		filepath.Join(toolsDir, "SkyLeapModule.apk"),
		filepath.Join(exeDir, "tools", "android", "xposed-release.apk"),
		filepath.Join(exeDir, "tools", "android", "SkyLeapModule.apk"),
		filepath.Join(exeDir, "xposed-release.apk"),
		filepath.Join(exeDir, "SkyLeapModule.apk"),
		filepath.Join(exeDir, "module.apk"),
		filepath.Join(exeDir, "modules", "xposed-release.apk"),
	}

	for _, c := range productionCandidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Size() > 0 {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	// 2. Source tree release artifact
	devReleaseCandidates := []string{
		filepath.Join(exeDir, "..", "..", "android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk"),
		filepath.Join(exeDir, "..", "android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk"),
		filepath.Join("android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk"),
	}

	for _, c := range devReleaseCandidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Size() > 0 {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	// 3. Development debug fallback (only when release build is absent)
	devDebugCandidates := []string{
		filepath.Join(exeDir, "tools", "android", "xposed-debug.apk"),
		filepath.Join(exeDir, "xposed-debug.apk"),
		filepath.Join(exeDir, "modules", "xposed-debug.apk"),
		filepath.Join(exeDir, "..", "..", "android", "xposed", "build", "outputs", "apk", "debug", "xposed-debug.apk"),
		filepath.Join(exeDir, "..", "android", "xposed", "build", "outputs", "apk", "debug", "xposed-debug.apk"),
		filepath.Join("android", "xposed", "build", "outputs", "apk", "debug", "xposed-debug.apk"),
	}

	for _, c := range devDebugCandidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Size() > 0 {
			abs, _ := filepath.Abs(c)
			return abs, nil
		}
	}

	return "", fmt.Errorf("SkyLeap Xposed Module APK not found. Looked for:\n" +
		"  - " + filepath.Join(exeDir, "xposed-release.apk") + " (production release artifact)\n" +
		"  - android/xposed/build/outputs/apk/release/xposed-release.apk (source tree release)\n" +
		"Please build the release module via `./gradlew :xposed:assembleRelease` or specify --module <path>")
}

// ValidateModuleApk verifies that the specified APK is a valid standalone Xposed module
// for GBF-Accelerator (package com.sagisawa.gbfaccelerator.xposed), contains Xposed metadata,
// and strictly rejects the Host App (com.sagisawa.gbfaccelerator).
func ValidateModuleApk(apkPath string) error {
	fi, err := os.Stat(apkPath)
	if err != nil {
		return fmt.Errorf("module APK not accessible: %w", err)
	}
	if fi.IsDir() {
		return fmt.Errorf("module APK path is a directory: %s", apkPath)
	}

	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return fmt.Errorf("module APK is not a valid zip archive: %w", err)
	}
	defer zr.Close()

	var manifestFile *zip.File
	hasXposedMeta := false

	for _, f := range zr.File {
		if f.Name == "AndroidManifest.xml" {
			manifestFile = f
		}
		if f.Name == "META-INF/xposed/java_init.list" || f.Name == "META-INF/xposed/module.prop" {
			hasXposedMeta = true
		}
	}

	if manifestFile == nil {
		return fmt.Errorf("invalid module APK: AndroidManifest.xml not found")
	}

	rc, err := manifestFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open AndroidManifest.xml in module APK: %w", err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return fmt.Errorf("failed to read AndroidManifest.xml from module APK: %w", err)
	}

	info, err := ParseManifest(data)
	if err != nil {
		return fmt.Errorf("failed to parse AndroidManifest.xml in module APK: %w", err)
	}

	const hostAppPkg = "com.sagisawa.gbfaccelerator"
	const expectedModulePkg = "com.sagisawa.gbfaccelerator.xposed"

	if info.PackageName == hostAppPkg {
		return fmt.Errorf("invalid module APK: detected Host App APK (%s); please provide the standalone Xposed module APK (%s)", hostAppPkg, expectedModulePkg)
	}

	if info.PackageName != expectedModulePkg {
		return fmt.Errorf("invalid module APK: unexpected package %q (expected %s)", info.PackageName, expectedModulePkg)
	}

	if !hasXposedMeta {
		return fmt.Errorf("invalid module APK: missing Xposed metadata (META-INF/xposed/java_init.list or module.prop)")
	}

	return nil
}

// ExecuteLSPatch invokes LSPatch Portable to patch the provided APK(s) with the given module.
func ExecuteLSPatch(cfg *LSPatchConfig, baseApk string, splitApks []string, outputDir string) ([]string, error) {
	args := []string{
		"-jar", cfg.LSPatchJarPath,
	}

	args = append(args, baseApk)
	args = append(args, splitApks...)

	args = append(args,
		"-m", cfg.ModuleApkPath,
		"-o", outputDir,
		"-f", // force overwrite
	)

	if cfg.NewPackageName != "" {
		args = append(args, "-pkg", cfg.NewPackageName)
	}

	if cfg.Verbose {
		args = append(args, "-v")
	}

	cmd := exec.Command(cfg.JavaBinaryPath, args...)
	prepareCmd(cmd)

	var outBuf bytes.Buffer
	if cfg.LogFn != nil {
		lw := &lspatchLineWriter{logFn: cfg.LogFn}
		cmd.Stdout = io.MultiWriter(&outBuf, lw)
		cmd.Stderr = io.MultiWriter(&outBuf, lw)
		defer lw.Flush()
	} else {
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
	}

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("LSPatch execution failed (exit code %v):\n%s", err, outBuf.String())
	}

	// Collect generated *-lspatched.apk files
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read LSPatch output directory: %w", err)
	}

	var outputApks []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".apk") {
			outputApks = append(outputApks, filepath.Join(outputDir, e.Name()))
		}
	}

	if len(outputApks) == 0 {
		return nil, fmt.Errorf("LSPatch completed but no output APK was generated:\n%s", outBuf.String())
	}

	return outputApks, nil
}

type lspatchLineWriter struct {
	buf   bytes.Buffer
	logFn func(string)
}

func (w *lspatchLineWriter) Write(p []byte) (n int, err error) {
	for _, b := range p {
		if b == '\n' {
			line := strings.TrimRight(w.buf.String(), "\r")
			w.buf.Reset()
			if w.logFn != nil && line != "" {
				w.logFn(line)
			}
		} else {
			w.buf.WriteByte(b)
		}
	}
	return len(p), nil
}

func (w *lspatchLineWriter) Flush() {
	if w.buf.Len() > 0 {
		line := strings.TrimRight(w.buf.String(), "\r")
		w.buf.Reset()
		if w.logFn != nil && line != "" {
			w.logFn(line)
		}
	}
}
