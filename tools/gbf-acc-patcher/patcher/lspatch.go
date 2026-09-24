package patcher

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var (
	javaVersionRegex = regexp.MustCompile(`(?:openjdk|java) version "(\d+)(?:\.(\d+))?`)
)

type LSPatchConfig struct {
	JavaBinaryPath string
	LSPatchJarPath string
	ModuleApkPath  string
	Verbose        bool
}

// FindJavaRuntime discovers a Java binary with major version >= 21.
// Checks override path, JAVA_HOME, Android Studio JBR, and PATH.
func FindJavaRuntime(overridePath string) (path string, version string, err error) {
	candidates := make([]string, 0)
	if overridePath != "" {
		candidates = append(candidates, overridePath)
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

// FindLSPatchJar discovers lspatch.jar from override, local directories, or project repository.
func FindLSPatchJar(overridePath string, exeDir string) (string, error) {
	candidates := []string{
		overridePath,
		filepath.Join(exeDir, "lspatch.jar"),
		filepath.Join(exeDir, "lib", "lspatch.jar"),
		filepath.Join(exeDir, "..", "..", "build", "lspatch", "lspatch.jar"),
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

	return "", fmt.Errorf("lspatch.jar not found. Please place lspatch.jar in tool directory or specify --lspatch <path>")
}

// FindModuleApk discovers the built SkyLeap Xposed Module APK from override, local directories, or project repository.
func FindModuleApk(overridePath string, exeDir string) (string, error) {
	candidates := []string{
		overridePath,
		filepath.Join(exeDir, "xposed-release.apk"),
		filepath.Join(exeDir, "xposed-debug.apk"),
		filepath.Join(exeDir, "SkyLeapModule.apk"),
		filepath.Join(exeDir, "module.apk"),
		filepath.Join(exeDir, "..", "..", "android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk"),
		filepath.Join(exeDir, "..", "..", "android", "xposed", "build", "outputs", "apk", "debug", "xposed-debug.apk"),
		filepath.Join("android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk"),
		filepath.Join("android", "xposed", "build", "outputs", "apk", "debug", "xposed-debug.apk"),
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

	return "", fmt.Errorf("Xposed Module APK not found. Please build the Xposed module via `./gradlew :xposed:assembleRelease` or specify --module <path>")
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

	if cfg.Verbose {
		args = append(args, "-v")
	}

	cmd := exec.Command(cfg.JavaBinaryPath, args...)

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

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
