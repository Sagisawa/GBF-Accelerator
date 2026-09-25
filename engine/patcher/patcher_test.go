package patcher

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckJavaVersionParsing(t *testing.T) {
	tests := []struct {
		input       string
		expectMajor int
		expectError bool
	}{
		{`openjdk version "25.0.3" 2026-04-21`, 25, false},
		{`openjdk version "21.0.2" 2024-01-16 LTS`, 21, false},
		{`openjdk version "17.0.8" 2023-07-18`, 17, false},
		{`java version "1.8.0_312"`, 8, false},
		{`invalid output`, 0, true},
	}

	for _, tc := range tests {
		matches := javaVersionRegex.FindStringSubmatch(tc.input)
		if tc.expectError {
			if len(matches) >= 2 {
				t.Errorf("expected error for %q, got matches %v", tc.input, matches)
			}
			continue
		}

		if len(matches) < 2 {
			t.Fatalf("expected match for %q, got none", tc.input)
		}

		major := 0
		if matches[1] == "1" && len(matches) >= 3 && matches[2] != "" {
			major = 8
		} else {
			if matches[1] == "25" {
				major = 25
			} else if matches[1] == "21" {
				major = 21
			} else if matches[1] == "17" {
				major = 17
			}
		}

		if major != tc.expectMajor {
			t.Errorf("for input %q: expected major %d, got %d", tc.input, tc.expectMajor, major)
		}
	}
}

func TestInspectInput_RealBaseApk(t *testing.T) {
	// Look for real base.apk in build/skyleap_splits/original/base.apk
	repoRoot := filepath.Join("..", "..", "..")
	baseApkPath := filepath.Join(repoRoot, "build", "skyleap_splits", "original", "base.apk")
	if _, err := os.Stat(baseApkPath); err != nil {
		t.Skipf("skipping test, base.apk not found at %s", baseApkPath)
	}

	tempDir := t.TempDir()
	info, err := InspectInput(baseApkPath, tempDir)
	if err != nil {
		t.Fatalf("InspectInput failed: %v", err)
	}

	if info.IsSplit {
		t.Errorf("expected single APK, got split")
	}
	if info.PackageName != "com.dena.skyleap" {
		t.Errorf("expected package com.dena.skyleap, got %q", info.PackageName)
	}
	if info.VersionName != "1.60.0" {
		t.Errorf("expected version 1.60.0, got %q", info.VersionName)
	}
	if info.TotalApks != 1 {
		t.Errorf("expected TotalApks == 1, got %d", info.TotalApks)
	}
}

func TestInspectInput_RealSplitDirectory(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	splitDirPath := filepath.Join(repoRoot, "build", "skyleap_splits", "original")
	if _, err := os.Stat(splitDirPath); err != nil {
		t.Skipf("skipping test, split directory not found at %s", splitDirPath)
	}

	tempDir := t.TempDir()
	info, err := InspectInput(splitDirPath, tempDir)
	if err != nil {
		t.Fatalf("InspectInput failed: %v", err)
	}

	if !info.IsSplit {
		t.Errorf("expected split APK, got single")
	}
	if info.PackageName != "com.dena.skyleap" {
		t.Errorf("expected package com.dena.skyleap, got %q", info.PackageName)
	}
	if info.VersionName != "1.60.0" {
		t.Errorf("expected version 1.60.0, got %q", info.VersionName)
	}
	if info.TotalApks != 5 {
		t.Errorf("expected TotalApks == 5, got %d", info.TotalApks)
	}
	if !strings.HasSuffix(filepath.Base(info.BaseApkPath), "base.apk") {
		t.Errorf("expected base.apk as BaseApkPath, got %s", info.BaseApkPath)
	}
	if len(info.SplitApkPaths) != 4 {
		t.Errorf("expected 4 splits, got %d", len(info.SplitApkPaths))
	}
}

func TestInspectInput_ApksZipArchive(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	baseApkPath := filepath.Join(repoRoot, "build", "skyleap_splits", "original", "base.apk")
	splitApkPath := filepath.Join(repoRoot, "build", "skyleap_splits", "original", "split_config.ja.apk")
	if _, err := os.Stat(baseApkPath); err != nil {
		t.Skipf("skipping test, base.apk not found at %s", baseApkPath)
	}

	tempDir := t.TempDir()
	archivePath := filepath.Join(tempDir, "skyleap_bundle.apks")

	// Create test .apks zip archive
	zipFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}
	zw := zip.NewWriter(zipFile)

	addFileToZip := func(name, srcPath string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("failed to add file to zip: %v", err)
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("failed to write file to zip: %v", err)
		}
	}

	addFileToZip("base.apk", baseApkPath)
	if _, err := os.Stat(splitApkPath); err == nil {
		addFileToZip("split_config.ja.apk", splitApkPath)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip: %v", err)
	}
	zipFile.Close()

	// Inspect the created .apks archive
	info, err := InspectInput(archivePath, tempDir)
	if err != nil {
		t.Fatalf("InspectInput on .apks archive failed: %v", err)
	}

	if !info.IsSplit {
		t.Errorf("expected split structure for archive, got single")
	}
	if info.PackageName != "com.dena.skyleap" {
		t.Errorf("expected package com.dena.skyleap, got %q", info.PackageName)
	}
	if info.TotalApks < 2 {
		t.Errorf("expected at least 2 APKs in bundle, got %d", info.TotalApks)
	}
}

func TestBundleOutput_Single(t *testing.T) {
	tempDir := t.TempDir()
	rawApk := filepath.Join(tempDir, "raw-lspatched.apk")
	if err := os.WriteFile(rawApk, []byte("fake apk data"), 0644); err != nil {
		t.Fatalf("failed to write raw apk: %v", err)
	}

	outDir := filepath.Join(tempDir, "final_out")
	res, err := BundleOutput(false, "skyleap.apk", []string{rawApk}, outDir)
	if err != nil {
		t.Fatalf("BundleOutput failed: %v", err)
	}

	if res.IsSplit {
		t.Errorf("expected single APK result")
	}
	if !strings.HasSuffix(res.SingleApk, "skyleap-patched.apk") {
		t.Errorf("expected filename ending with skyleap-patched.apk, got %s", res.SingleApk)
	}
	if res.TotalBytes != int64(len("fake apk data")) {
		t.Errorf("expected size %d, got %d", len("fake apk data"), res.TotalBytes)
	}
}

func TestBundleOutput_Split(t *testing.T) {
	tempDir := t.TempDir()
	rawBase := filepath.Join(tempDir, "base-lspatched.apk")
	rawSplit := filepath.Join(tempDir, "split_config.arm64_v8a-lspatched.apk")
	os.WriteFile(rawBase, []byte("base data"), 0644)
	os.WriteFile(rawSplit, []byte("split data"), 0644)

	outDir := filepath.Join(tempDir, "final_out")
	res, err := BundleOutput(true, "skyleap.apks", []string{rawBase, rawSplit}, outDir)
	if err != nil {
		t.Fatalf("BundleOutput failed: %v", err)
	}

	if !res.IsSplit {
		t.Errorf("expected split APK result")
	}
	if res.TotalApks != 2 {
		t.Errorf("expected 2 apks, got %d", res.TotalApks)
	}
	if res.ApksArchive == "" {
		t.Errorf("expected non-empty ApksArchive path")
	}

	// Verify generated .apks is valid ZIP
	zr, err := zip.OpenReader(res.ApksArchive)
	if err != nil {
		t.Fatalf("generated .apks is not a valid zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 2 {
		t.Errorf("expected 2 files in .apks zip, got %d", len(zr.File))
	}
}

func TestInspectInput_NonExistentInput(t *testing.T) {
	tempDir := t.TempDir()
	_, err := InspectInput(filepath.Join(tempDir, "not_there.apk"), tempDir)
	if err == nil {
		t.Errorf("expected error for non-existent input, got nil")
	}
}

func TestInspectInput_UnsupportedExtension(t *testing.T) {
	tempDir := t.TempDir()
	fakeFile := filepath.Join(tempDir, "document.pdf")
	os.WriteFile(fakeFile, []byte("test"), 0644)

	_, err := InspectInput(fakeFile, tempDir)
	if err == nil {
		t.Errorf("expected error for unsupported extension, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported file format") {
		t.Errorf("expected 'unsupported file format' message, got %v", err)
	}
}

func TestFindModuleApk_Priority(t *testing.T) {
	tempDir := t.TempDir()

	// 1. When no files exist
	_, err := FindModuleApk("", tempDir)
	if err == nil {
		t.Errorf("expected error when no candidates exist")
	}

	// 2. Fallback candidate in exeDir
	debugApk := filepath.Join(tempDir, "xposed-debug.apk")
	if err := os.WriteFile(debugApk, []byte("fake debug"), 0644); err != nil {
		t.Fatal(err)
	}
	found, err := FindModuleApk("", tempDir)
	if err != nil {
		t.Fatalf("expected to find xposed-debug.apk: %v", err)
	}
	if !strings.HasSuffix(found, "xposed-debug.apk") {
		t.Errorf("expected xposed-debug.apk, got %s", found)
	}

	// 3. Higher priority release APK in exeDir
	releaseApk := filepath.Join(tempDir, "xposed-release.apk")
	if err := os.WriteFile(releaseApk, []byte("fake release"), 0644); err != nil {
		t.Fatal(err)
	}
	found, err = FindModuleApk("", tempDir)
	if err != nil {
		t.Fatalf("expected to find xposed-release.apk: %v", err)
	}
	if !strings.HasSuffix(found, "xposed-release.apk") {
		t.Errorf("expected xposed-release.apk, got %s", found)
	}

	// 4. Override path has highest priority
	overrideApk := filepath.Join(tempDir, "custom-module.apk")
	if err := os.WriteFile(overrideApk, []byte("fake override"), 0644); err != nil {
		t.Fatal(err)
	}
	found, err = FindModuleApk(overrideApk, tempDir)
	if err != nil {
		t.Fatalf("expected override path to succeed: %v", err)
	}
	if !strings.HasSuffix(found, "custom-module.apk") {
		t.Errorf("expected custom-module.apk, got %s", found)
	}
}

func TestValidateModuleApk_RealXposedRelease(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	modulePath := filepath.Join(repoRoot, "android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk")
	if _, err := os.Stat(modulePath); err != nil {
		t.Skipf("skipping test, xposed-release.apk not found at %s", modulePath)
	}

	if err := ValidateModuleApk(modulePath); err != nil {
		t.Fatalf("ValidateModuleApk failed on real xposed-release.apk: %v", err)
	}
}

func TestValidateModuleApk_RejectsHostApp(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	hostApkPath := filepath.Join(repoRoot, "android", "app", "build", "outputs", "apk", "debug", "app-debug.apk")
	if _, err := os.Stat(hostApkPath); err != nil {
		t.Skipf("skipping test, app-debug.apk not found at %s", hostApkPath)
	}

	err := ValidateModuleApk(hostApkPath)
	if err == nil {
		t.Fatalf("expected ValidateModuleApk to reject host app APK, but succeeded")
	}

	if !strings.Contains(err.Error(), "detected Host App APK (com.sagisawa.gbfaccelerator)") {
		t.Errorf("expected error mentioning detected Host App APK, got: %v", err)
	}
}

func TestValidateModuleApk_MissingXposedMetadata(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	realModule := filepath.Join(repoRoot, "android", "xposed", "build", "outputs", "apk", "release", "xposed-release.apk")
	if _, err := os.Stat(realModule); err != nil {
		t.Skipf("skipping test, xposed-release.apk not found at %s", realModule)
	}

	// Read AndroidManifest.xml from real module
	zr, err := zip.OpenReader(realModule)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var manifestData []byte
	for _, f := range zr.File {
		if f.Name == "AndroidManifest.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			manifestData = data
			break
		}
	}

	if manifestData == nil {
		t.Fatal("AndroidManifest.xml not found in real module")
	}

	// Create a zip with the valid manifest but NO META-INF/xposed
	tempDir := t.TempDir()
	badApkPath := filepath.Join(tempDir, "bad-module.apk")
	f, err := os.Create(badApkPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(manifestData); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	err = ValidateModuleApk(badApkPath)
	if err == nil {
		t.Fatalf("expected ValidateModuleApk to fail on missing Xposed metadata, but succeeded")
	}
	if !strings.Contains(err.Error(), "missing Xposed metadata") {
		t.Errorf("expected error mentioning missing Xposed metadata, got: %v", err)
	}
}

func TestValidateModuleApk_InvalidZip(t *testing.T) {
	tempDir := t.TempDir()
	corruptPath := filepath.Join(tempDir, "corrupt.apk")
	if err := os.WriteFile(corruptPath, []byte("not a zip file"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateModuleApk(corruptPath)
	if err == nil {
		t.Errorf("expected error for corrupt zip, got nil")
	}
}

func TestValidateLSPatchJar_Canonical(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	jarPath := filepath.Join(repoRoot, "build", "lspatch", "lspatch.jar")
	if _, err := os.Stat(jarPath); err != nil {
		t.Skipf("skipping test, lspatch.jar not found at %s", jarPath)
	}

	if err := ValidateLSPatchJar(jarPath); err != nil {
		t.Fatalf("ValidateLSPatchJar failed on canonical build/lspatch/lspatch.jar: %v", err)
	}
}

func TestValidateLSPatchJar_ChecksumMismatch(t *testing.T) {
	tempDir := t.TempDir()
	fakeJar := filepath.Join(tempDir, "tampered-lspatch.jar")
	if err := os.WriteFile(fakeJar, []byte("tampered lspatch binary data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateLSPatchJar(fakeJar)
	if err == nil {
		t.Fatalf("expected ValidateLSPatchJar to reject tampered binary, but succeeded")
	}
	if !strings.Contains(err.Error(), "SHA-256 checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got: %v", err)
	}
}

func TestValidateLSPatchJar_EmptyAndMissing(t *testing.T) {
	tempDir := t.TempDir()

	// Missing
	missingJar := filepath.Join(tempDir, "missing.jar")
	if err := ValidateLSPatchJar(missingJar); err == nil {
		t.Errorf("expected error for missing jar")
	}

	// Empty
	emptyJar := filepath.Join(tempDir, "empty.jar")
	if err := os.WriteFile(emptyJar, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLSPatchJar(emptyJar); err == nil {
		t.Errorf("expected error for empty jar")
	}
}

func TestFindLSPatchJar_OverridePriority(t *testing.T) {
	tempDir := t.TempDir()
	customJar := filepath.Join(tempDir, "custom.jar")
	if err := os.WriteFile(customJar, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err := FindLSPatchJar(customJar, tempDir)
	if err != nil {
		t.Fatalf("FindLSPatchJar failed with override: %v", err)
	}
	if !strings.HasSuffix(found, "custom.jar") {
		t.Errorf("expected custom.jar, got %s", found)
	}
}

func TestVerifyIntegrity_SingleApk(t *testing.T) {
	tempDir := t.TempDir()
	apkPath := filepath.Join(tempDir, "valid-patched.apk")

	// Create a valid zip file as single apk
	f, err := os.Create(apkPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("AndroidManifest.xml")
	w.Write([]byte("dummy manifest"))
	zw.Close()
	f.Close()

	fi, _ := os.Stat(apkPath)
	validResult := &BundleResult{
		IsSplit:      false,
		SingleApk:    apkPath,
		TotalApks:    1,
		TotalBytes:   fi.Size(),
		PackageFiles: []string{apkPath},
	}

	// 1. Valid case
	if err := VerifyIntegrity(validResult, false, 1); err != nil {
		t.Errorf("expected valid single APK to pass integrity: %v", err)
	}

	// 2. Mode mismatch
	if err := VerifyIntegrity(validResult, true, 1); err == nil {
		t.Errorf("expected error for split mode mismatch")
	}

	// 3. Corrupt zip
	corruptApk := filepath.Join(tempDir, "corrupt.apk")
	os.WriteFile(corruptApk, []byte("corrupt"), 0644)
	corruptResult := &BundleResult{
		IsSplit:      false,
		SingleApk:    corruptApk,
		TotalApks:    1,
		TotalBytes:   7,
		PackageFiles: []string{corruptApk},
	}
	if err := VerifyIntegrity(corruptResult, false, 1); err == nil {
		t.Errorf("expected error for corrupt zip APK")
	}
}

func TestVerifyIntegrity_SplitApk(t *testing.T) {
	tempDir := t.TempDir()
	splitDir := filepath.Join(tempDir, "splits")
	os.MkdirAll(splitDir, 0755)

	baseApk := filepath.Join(splitDir, "base-lspatched.apk")
	configApk := filepath.Join(splitDir, "split_config-lspatched.apk")

	for _, p := range []string{baseApk, configApk} {
		f, _ := os.Create(p)
		zw := zip.NewWriter(f)
		w, _ := zw.Create("classes.dex")
		w.Write([]byte("dex"))
		zw.Close()
		f.Close()
	}

	apksArchive := filepath.Join(tempDir, "bundle.apks")
	createApksArchive([]string{baseApk, configApk}, apksArchive)

	validSplitResult := &BundleResult{
		IsSplit:      true,
		SplitDir:     splitDir,
		ApksArchive:  apksArchive,
		TotalApks:    2,
		TotalBytes:   1000,
		PackageFiles: []string{baseApk, configApk},
	}

	// 1. Valid split
	if err := VerifyIntegrity(validSplitResult, true, 2); err != nil {
		t.Errorf("expected valid split to pass integrity: %v", err)
	}

	// 2. Count mismatch
	if err := VerifyIntegrity(validSplitResult, true, 3); err == nil {
		t.Errorf("expected error for split count mismatch")
	}

	// 3. Missing base APK
	noBaseResult := &BundleResult{
		IsSplit:      true,
		SplitDir:     splitDir,
		ApksArchive:  apksArchive,
		TotalApks:    1,
		TotalBytes:   500,
		PackageFiles: []string{configApk},
	}
	if err := VerifyIntegrity(noBaseResult, true, 1); err == nil {
		t.Errorf("expected error when base APK is missing")
	}
}

func TestInputRemainsUnmodified(t *testing.T) {
	tempDir := t.TempDir()
	originalFile := filepath.Join(tempDir, "input.apk")
	originalBytes := []byte("original unmodifiable apk data")
	if err := os.WriteFile(originalFile, originalBytes, 0644); err != nil {
		t.Fatal(err)
	}

	// Run InspectInput (will fail AXML parsing, which is fine, but it must not mutate original)
	InspectInput(originalFile, tempDir)

	afterBytes, err := os.ReadFile(originalFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != string(originalBytes) {
		t.Errorf("input file was modified during processing!")
	}
}

func TestCheckIsSystemWebView(t *testing.T) {
	// 1. SkyLeap package
	isWebView, _, unsupp := CheckIsSystemWebView(nil, "com.dena.skyleap")
	if !isWebView || unsupp != "" {
		t.Errorf("expected SkyLeap to be identified as system webview, got %v (%s)", isWebView, unsupp)
	}

	// 2. Via browser package
	isWebView, _, unsupp = CheckIsSystemWebView(nil, "mark.via.gp")
	if !isWebView || unsupp != "" {
		t.Errorf("expected Via to be identified as system webview, got %v (%s)", isWebView, unsupp)
	}

	// 3. Known standalone Chrome package
	isWebView, _, _ = CheckIsSystemWebView(nil, "com.android.chrome")
	if isWebView {
		t.Errorf("expected Chrome to be identified as standalone non-webview")
	}

	// 4. Known standalone Kiwi package
	isWebView, _, _ = CheckIsSystemWebView(nil, "com.kiwibrowser.browser")
	if isWebView {
		t.Errorf("expected Kiwi to be identified as standalone non-webview")
	}

	// 5. APK with embedded libmonochrome.so
	tempDir := t.TempDir()
	chromeApk := filepath.Join(tempDir, "fake_chrome.apk")
	f, err := os.Create(chromeApk)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("lib/arm64-v8a/libmonochrome.so")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("fake monochrome binary"))
	_ = zw.Close()
	_ = f.Close()

	isWebView, _, unsupp = CheckIsSystemWebView([]string{chromeApk}, "com.custom.browser")
	if isWebView {
		t.Errorf("expected APK containing libmonochrome.so to be rejected as non-webview")
	}

	// 6. APK with embedded libxul.so (Firefox Gecko)
	firefoxApk := filepath.Join(tempDir, "fake_firefox.apk")
	ff, err := os.Create(firefoxApk)
	if err != nil {
		t.Fatal(err)
	}
	fzw := zip.NewWriter(ff)
	fw, err := fzw.Create("lib/arm64-v8a/libxul.so")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("fake xul binary"))
	_ = fzw.Close()
	_ = ff.Close()

	isWebView, _, unsupp = CheckIsSystemWebView([]string{firefoxApk}, "org.custom.browser")
	if isWebView {
		t.Errorf("expected APK containing libxul.so to be rejected as non-webview")
	}

	// 7. Clean APK with standard resources
	cleanApk := filepath.Join(tempDir, "clean_app.apk")
	cf, err := os.Create(cleanApk)
	if err != nil {
		t.Fatal(err)
	}
	czw := zip.NewWriter(cf)
	cw, err := czw.Create("classes.dex")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = cw.Write([]byte("fake dex"))
	_ = czw.Close()
	_ = cf.Close()

	isWebView, _, unsupp = CheckIsSystemWebView([]string{cleanApk}, "com.custom.clean.browser")
	if !isWebView || unsupp != "" {
		t.Errorf("expected clean app to be identified as system webview, got %v (%s)", isWebView, unsupp)
	}
}
