package patcher

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper to build a test zip archive with specified compression method
func buildZipFileWithMethod(t *testing.T, destPath string, entries map[string][]byte, method uint16) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", destPath, err)
	}
	f, err := os.Create(destPath)
	if err != nil {
		t.Fatalf("failed to create file %s: %v", destPath, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range entries {
		fh := &zip.FileHeader{
			Name:   name,
			Method: method,
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("failed to create zip entry %s: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("failed to write zip entry %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close zip %s: %v", destPath, err)
	}
}

func buildZipFile(t *testing.T, destPath string, entries map[string][]byte) {
	buildZipFileWithMethod(t, destPath, entries, zip.Deflate)
}

// helper to get real manifest from base-patched.apk if available
func getSampleManifestBytes() []byte {
	candidates := []string{
		filepath.Join("..", "..", "bin", "output_patched", "base-patched.apk"),
		filepath.Join("..", "bin", "output_patched", "base-patched.apk"),
		filepath.Join("bin", "output_patched", "base-patched.apk"),
	}
	for _, c := range candidates {
		zr, err := zip.OpenReader(c)
		if err == nil {
			for _, f := range zr.File {
				if f.Name == "AndroidManifest.xml" {
					rc, err := f.Open()
					if err == nil {
						data, _ := io.ReadAll(rc)
						_ = rc.Close()
						_ = zr.Close()
						if len(data) > 0 {
							return data
						}
					}
				}
			}
			_ = zr.Close()
		}
	}
	return nil
}

func getSampleSplitManifestBytes() []byte {
	candidates := []string{
		filepath.Join("..", "..", "bin", "output_patched", "gbf_extracted_1585985014-splits", "split_config.arm64_v8a-487-lspatched.apk"),
		filepath.Join("bin", "output_patched", "gbf_extracted_1585985014-splits", "split_config.arm64_v8a-487-lspatched.apk"),
	}
	for _, c := range candidates {
		zr, err := zip.OpenReader(c)
		if err == nil {
			for _, f := range zr.File {
				if f.Name == "AndroidManifest.xml" {
					rc, err := f.Open()
					if err == nil {
						data, _ := io.ReadAll(rc)
						_ = rc.Close()
						_ = zr.Close()
						if len(data) > 0 {
							return data
						}
					}
				}
			}
			_ = zr.Close()
		}
	}
	return nil
}

// 1. Normal single APK origin extraction
func TestExtractOriginFromApk_Normal(t *testing.T) {
	tempDir := t.TempDir()
	srcApk := filepath.Join(tempDir, "patched.apk")
	destApk := filepath.Join(tempDir, "extracted_origin.apk")

	originPayload := []byte("PK\x03\x04fake-valid-apk-bytes-for-origin-testing-12345")
	buildZipFile(t, srcApk, map[string][]byte{
		"assets/lspatch/origin.apk":  originPayload,
		"assets/lspatch/config.json": []byte(`{"version": 1}`),
		"classes.dex":                []byte("dex data"),
	})

	err := ExtractOriginFromApk(srcApk, destApk)
	if err != nil {
		t.Fatalf("ExtractOriginFromApk failed: %v", err)
	}

	gotBytes, err := os.ReadFile(destApk)
	if err != nil {
		t.Fatalf("failed to read extracted destination: %v", err)
	}

	if !bytes.Equal(gotBytes, originPayload) {
		t.Errorf("extracted content mismatch: expected %q, got %q", string(originPayload), string(gotBytes))
	}
}

// 2. origin.apk not found
func TestExtractOriginFromApk_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	srcApk := filepath.Join(tempDir, "normal.apk")
	destApk := filepath.Join(tempDir, "extracted_origin.apk")

	buildZipFile(t, srcApk, map[string][]byte{
		"AndroidManifest.xml": []byte("manifest"),
		"classes.dex":         []byte("dex data"),
	})

	err := ExtractOriginFromApk(srcApk, destApk)
	if err == nil {
		t.Fatalf("expected error when assets/lspatch/origin.apk is missing, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}

	if _, statErr := os.Stat(destApk); statErr == nil {
		t.Errorf("expected destination file to not exist after failure")
	}
}

// 3. origin.apk is empty (0 bytes)
func TestExtractOriginFromApk_EmptyOrigin(t *testing.T) {
	tempDir := t.TempDir()
	srcApk := filepath.Join(tempDir, "empty_origin.apk")
	destApk := filepath.Join(tempDir, "extracted.apk")

	buildZipFile(t, srcApk, map[string][]byte{
		"assets/lspatch/origin.apk": []byte{},
	})

	err := ExtractOriginFromApk(srcApk, destApk)
	if err == nil {
		t.Fatalf("expected error for 0-byte origin.apk, got nil")
	}

	if _, statErr := os.Stat(destApk); statErr == nil {
		t.Errorf("expected destination file to be cleaned up after failure")
	}
}

// 4. Same source and destination paths
func TestExtractOriginFromApk_SamePath(t *testing.T) {
	tempDir := t.TempDir()
	samePath := filepath.Join(tempDir, "app.apk")
	buildZipFile(t, samePath, map[string][]byte{"assets/lspatch/origin.apk": []byte("content")})

	err := ExtractOriginFromApk(samePath, samePath)
	if err == nil {
		t.Errorf("expected error when src and dest are identical, got nil")
	}
}

// 5. CRC-32 integrity validation and cleanup on failure
func TestExtractOriginFromApk_CRCValidation(t *testing.T) {
	tempDir := t.TempDir()
	srcApk := filepath.Join(tempDir, "corrupted_crc.apk")
	destApk := filepath.Join(tempDir, "dest.apk")

	// Craft a normal zip file using zip.Store so payload is stored uncompressed
	payload := []byte("1234567890abcdefghijklmnopqrstuvwxyz")
	buildZipFileWithMethod(t, srcApk, map[string][]byte{
		"assets/lspatch/origin.apk": payload,
	}, zip.Store)

	// Now tamper with the zip file content bytes directly so the stored CRC in the directory no longer matches
	rawZip, err := os.ReadFile(srcApk)
	if err != nil {
		t.Fatal(err)
	}
	// Find the payload inside rawZip and mutate one byte
	payloadIdx := bytes.Index(rawZip, payload)
	if payloadIdx == -1 {
		t.Fatalf("could not find payload in raw zip")
	}
	rawZip[payloadIdx] ^= 0xFF // Flip bits to corrupt payload
	if err := os.WriteFile(srcApk, rawZip, 0644); err != nil {
		t.Fatal(err)
	}

	err = ExtractOriginFromApk(srcApk, destApk)
	if err == nil {
		t.Fatalf("expected error due to CRC mismatch, got nil")
	}

	// Verify target file was deleted on failure
	if _, statErr := os.Stat(destApk); statErr == nil {
		t.Errorf("expected destination file to be removed when CRC check fails")
	}
}

// 6. Single APK unwrap and metadata restoration
func TestUnwrapPatchedPackage_SingleApk(t *testing.T) {
	manifestBytes := getSampleManifestBytes()
	if len(manifestBytes) == 0 {
		t.Skip("skipping test: valid AndroidManifest.xml sample not available")
	}

	tempDir := t.TempDir()
	innerApk := filepath.Join(tempDir, "inner_original.apk")
	buildZipFile(t, innerApk, map[string][]byte{
		"AndroidManifest.xml": manifestBytes,
		"classes.dex":         []byte("original classes"),
	})
	innerData, err := os.ReadFile(innerApk)
	if err != nil {
		t.Fatal(err)
	}

	outerApk := filepath.Join(tempDir, "outer_patched.apk")
	buildZipFile(t, outerApk, map[string][]byte{
		"AndroidManifest.xml":        manifestBytes,
		"assets/lspatch/origin.apk":  innerData,
		"assets/lspatch/config.json": []byte(`{"version": 1}`),
		"assets/lspatch/loader.dex":  []byte("loader"),
	})

	inspectWorkDir := filepath.Join(tempDir, "inspect_work")
	info, err := InspectInput(outerApk, inspectWorkDir)
	if err != nil {
		t.Fatalf("InspectInput failed: %v", err)
	}

	if !info.IsAlreadyPatched {
		t.Errorf("expected IsAlreadyPatched=true")
	}
	if info.PatchType != "lspatch" {
		t.Errorf("expected PatchType='lspatch', got %q", info.PatchType)
	}

	unwrapDir := filepath.Join(tempDir, "unwrap_work")
	cleanInfo, err := UnwrapPatchedPackage(info, unwrapDir)
	if err != nil {
		t.Fatalf("UnwrapPatchedPackage failed: %v", err)
	}

	if cleanInfo.IsSplit {
		t.Errorf("expected single APK (IsSplit=false)")
	}
	if cleanInfo.TotalApks != 1 {
		t.Errorf("expected TotalApks=1, got %d", cleanInfo.TotalApks)
	}
	if cleanInfo.IsAlreadyPatched {
		t.Errorf("expected clean package to have IsAlreadyPatched=false")
	}
	if filepath.Base(cleanInfo.BaseApkPath) != "base.apk" {
		t.Errorf("expected base.apk filename, got %s", filepath.Base(cleanInfo.BaseApkPath))
	}
}

// 7. Split APKs unwrap and multi-file restoration
func TestUnwrapPatchedPackage_SplitApks(t *testing.T) {
	baseManifest := getSampleManifestBytes()
	splitManifest := getSampleSplitManifestBytes()
	if len(baseManifest) == 0 || len(splitManifest) == 0 {
		t.Skip("skipping test: base or split AndroidManifest.xml sample not available")
	}

	tempDir := t.TempDir()

	// 1. Inner clean APKs
	innerBase := filepath.Join(tempDir, "inner_base.apk")
	buildZipFile(t, innerBase, map[string][]byte{
		"AndroidManifest.xml": baseManifest,
		"classes.dex":         []byte("base dex"),
	})
	innerBaseData, _ := os.ReadFile(innerBase)

	innerSplit := filepath.Join(tempDir, "inner_split.apk")
	buildZipFile(t, innerSplit, map[string][]byte{
		"AndroidManifest.xml": splitManifest,
		"lib/fake.so":         []byte("so data"),
	})
	innerSplitData, _ := os.ReadFile(innerSplit)

	// 2. Outer patched split APKs in a directory
	patchedSplitsDir := filepath.Join(tempDir, "patched_splits")
	outerBase := filepath.Join(patchedSplitsDir, "base-487-lspatched.apk")
	outerSplit := filepath.Join(patchedSplitsDir, "split_config.arm64_v8a-487-lspatched.apk")

	buildZipFile(t, outerBase, map[string][]byte{
		"AndroidManifest.xml":        baseManifest,
		"assets/lspatch/origin.apk":  innerBaseData,
		"assets/lspatch/config.json": []byte(`{}`),
		"assets/lspatch/loader.dex":  []byte("loader"),
	})
	buildZipFile(t, outerSplit, map[string][]byte{
		"AndroidManifest.xml":       splitManifest,
		"assets/lspatch/origin.apk": innerSplitData,
	})

	// Inspect the directory of patched splits
	inspectWorkDir := filepath.Join(tempDir, "inspect_work")
	info, err := InspectInput(patchedSplitsDir, inspectWorkDir)
	if err != nil {
		t.Fatalf("InspectInput failed: %v", err)
	}

	if !info.IsSplit {
		t.Errorf("expected IsSplit=true")
	}
	if !info.IsAlreadyPatched {
		t.Errorf("expected IsAlreadyPatched=true")
	}
	if info.TotalApks != 2 {
		t.Errorf("expected TotalApks=2, got %d", info.TotalApks)
	}

	// Unwrap
	unwrapDir := filepath.Join(tempDir, "unwrap_work")
	cleanInfo, err := UnwrapPatchedPackage(info, unwrapDir)
	if err != nil {
		t.Fatalf("UnwrapPatchedPackage failed: %v", err)
	}

	if !cleanInfo.IsSplit {
		t.Errorf("expected clean package IsSplit=true")
	}
	if cleanInfo.TotalApks != 2 {
		t.Errorf("expected clean package TotalApks=2, got %d", cleanInfo.TotalApks)
	}
	if filepath.Base(cleanInfo.BaseApkPath) != "base.apk" {
		t.Errorf("expected base APK named base.apk, got %s", filepath.Base(cleanInfo.BaseApkPath))
	}
	if len(cleanInfo.SplitApkPaths) != 1 {
		t.Fatalf("expected 1 split APK path, got %d", len(cleanInfo.SplitApkPaths))
	}
	if !strings.HasPrefix(filepath.Base(cleanInfo.SplitApkPaths[0]), "split_") {
		t.Errorf("expected split APK filename prefix split_, got %s", filepath.Base(cleanInfo.SplitApkPaths[0]))
	}
}

// 8. Reject nested patched package (only 1 layer unwrap allowed)
func TestUnwrapPatchedPackage_NestedRejected(t *testing.T) {
	manifestBytes := getSampleManifestBytes()
	if len(manifestBytes) == 0 {
		manifestBytes = []byte("dummy manifest")
	}

	tempDir := t.TempDir()

	// Level 2 (inner-inner)
	level2Apk := filepath.Join(tempDir, "level2.apk")
	buildZipFile(t, level2Apk, map[string][]byte{
		"AndroidManifest.xml": manifestBytes,
	})
	level2Data, _ := os.ReadFile(level2Apk)

	// Level 1 (inner, but still patched!)
	level1Apk := filepath.Join(tempDir, "level1.apk")
	buildZipFile(t, level1Apk, map[string][]byte{
		"AndroidManifest.xml":        manifestBytes,
		"assets/lspatch/origin.apk":  level2Data,
		"assets/lspatch/config.json": []byte(`{}`),
	})
	level1Data, _ := os.ReadFile(level1Apk)

	// Level 0 (outer package)
	level0Apk := filepath.Join(tempDir, "level0.apk")
	buildZipFile(t, level0Apk, map[string][]byte{
		"AndroidManifest.xml":        manifestBytes,
		"assets/lspatch/origin.apk":  level1Data,
		"assets/lspatch/config.json": []byte(`{}`),
	})

	info := &ApkPackageInfo{
		IsSplit:          false,
		BaseApkPath:      level0Apk,
		TotalApks:        1,
		IsAlreadyPatched: true,
	}

	unwrapDir := filepath.Join(tempDir, "unwrap_nested")
	_, err := UnwrapPatchedPackage(info, unwrapDir)
	if err == nil {
		t.Fatalf("expected error for nested patched package, got nil")
	}

	if !strings.Contains(err.Error(), "嵌套修补包") {
		t.Errorf("expected error message to contain '嵌套修补包', got: %v", err)
	}

	// Staging and unwrapDir should be cleaned up
	if entries, _ := os.ReadDir(unwrapDir); len(entries) > 0 {
		t.Errorf("expected workDir to be cleaned up after nested rejection, found %d entries", len(entries))
	}
}

// 9. Normal unpatched APK inspection is completely unaffected
func TestInspectInput_NormalUnpatchedUnchanged(t *testing.T) {
	manifestBytes := getSampleManifestBytes()
	if len(manifestBytes) == 0 {
		t.Skip("skipping test: sample manifest not available")
	}

	tempDir := t.TempDir()
	cleanApk := filepath.Join(tempDir, "clean.apk")
	buildZipFile(t, cleanApk, map[string][]byte{
		"AndroidManifest.xml": manifestBytes,
		"classes.dex":         []byte("dex code"),
	})

	info, err := InspectInput(cleanApk, tempDir)
	if err != nil {
		t.Fatalf("InspectInput failed: %v", err)
	}

	if info.IsAlreadyPatched {
		t.Errorf("expected IsAlreadyPatched=false for clean APK")
	}
	if info.PatchType != "" {
		t.Errorf("expected empty PatchType for clean APK, got %q", info.PatchType)
	}
	if info.OriginalVersionName != "" {
		t.Errorf("expected empty OriginalVersionName for clean APK, got %q", info.OriginalVersionName)
	}
}

// 10. Real sample verification (if bin/output_patched/base-patched.apk exists)
func TestRealBasePatchedApk_UnwrapAndInspect(t *testing.T) {
	realPath := filepath.Join("..", "..", "bin", "output_patched", "base-patched.apk")
	if _, err := os.Stat(realPath); err != nil {
		t.Skipf("skipping real sample test: %s not found", realPath)
	}

	tempDir := t.TempDir()
	info, err := InspectInput(realPath, tempDir)
	if err != nil {
		t.Fatalf("InspectInput on real sample failed: %v", err)
	}

	if !info.IsAlreadyPatched {
		t.Errorf("expected real base-patched.apk to be detected as already patched")
	}
	if info.PatchType != "lspatch" {
		t.Errorf("expected PatchType='lspatch', got %s", info.PatchType)
	}

	unwrapDir := filepath.Join(tempDir, "real_unwrap")
	cleanInfo, err := UnwrapPatchedPackage(info, unwrapDir)
	if err != nil {
		t.Fatalf("UnwrapPatchedPackage on real sample failed: %v", err)
	}

	if cleanInfo.IsAlreadyPatched {
		t.Errorf("expected clean package to not be already patched")
	}
	if cleanInfo.TotalApks != 1 {
		t.Errorf("expected 1 total apk, got %d", cleanInfo.TotalApks)
	}
	if !strings.HasPrefix(cleanInfo.PackageName, "com.dena.skyleap") {
		t.Errorf("expected package to have prefix com.dena.skyleap, got %s", cleanInfo.PackageName)
	}
}

// 11. Real split samples verification (if bin/output_patched/gbf_extracted_1585985014-splits exists)
func TestRealSplits_UnwrapAndInspect(t *testing.T) {
	realSplitsDir := filepath.Join("..", "..", "bin", "output_patched", "gbf_extracted_1585985014-splits")
	if _, err := os.Stat(realSplitsDir); err != nil {
		t.Skipf("skipping real splits test: %s not found", realSplitsDir)
	}

	tempDir := t.TempDir()
	info, err := InspectInput(realSplitsDir, tempDir)
	if err != nil {
		t.Fatalf("InspectInput on real splits dir failed: %v", err)
	}

	if !info.IsAlreadyPatched {
		t.Errorf("expected real splits to be detected as already patched")
	}
	if !info.IsSplit {
		t.Errorf("expected real splits to have IsSplit=true")
	}

	unwrapDir := filepath.Join(tempDir, "real_splits_unwrap")
	cleanInfo, err := UnwrapPatchedPackage(info, unwrapDir)
	if err != nil {
		t.Fatalf("UnwrapPatchedPackage on real splits failed: %v", err)
	}

	if !cleanInfo.IsSplit {
		t.Errorf("expected clean package IsSplit=true")
	}
	if cleanInfo.TotalApks != 5 {
		t.Errorf("expected 5 total APKs (1 base + 4 splits), got %d", cleanInfo.TotalApks)
	}
	if cleanInfo.PackageName != "com.dena.skyleap" {
		t.Errorf("expected package com.dena.skyleap, got %s", cleanInfo.PackageName)
	}
}

// 12. CheckHasNestedOrigin helper
func TestCheckHasNestedOrigin(t *testing.T) {
	tempDir := t.TempDir()
	apkWithOrigin := filepath.Join(tempDir, "with_origin.apk")
	buildZipFile(t, apkWithOrigin, map[string][]byte{"assets/lspatch/origin.apk": []byte("foo")})

	has, err := CheckHasNestedOrigin(apkWithOrigin)
	if err != nil {
		t.Fatalf("CheckHasNestedOrigin failed: %v", err)
	}
	if !has {
		t.Errorf("expected has=true for apkWithOrigin")
	}

	apkWithoutOrigin := filepath.Join(tempDir, "without_origin.apk")
	buildZipFile(t, apkWithoutOrigin, map[string][]byte{"classes.dex": []byte("foo")})

	has, err = CheckHasNestedOrigin(apkWithoutOrigin)
	if err != nil {
		t.Fatalf("CheckHasNestedOrigin failed: %v", err)
	}
	if has {
		t.Errorf("expected has=false for apkWithoutOrigin")
	}
}
