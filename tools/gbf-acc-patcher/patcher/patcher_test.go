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
