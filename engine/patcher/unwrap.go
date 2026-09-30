package patcher

import (
	"archive/zip"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractOriginFromApk streams assets/lspatch/origin.apk from srcApkPath to destApkPath.
// It verifies ZIP entry existence, non-emptiness, and streams using a fixed buffer to avoid
// high memory consumption. It validates CRC-32 and deletes incomplete output on any failure.
func ExtractOriginFromApk(srcApkPath, destApkPath string) error {
	cleanSrc := filepath.Clean(srcApkPath)
	cleanDest := filepath.Clean(destApkPath)

	if cleanSrc == cleanDest {
		return fmt.Errorf("source and destination paths must differ: %s", cleanDest)
	}

	zr, err := zip.OpenReader(cleanSrc)
	if err != nil {
		return fmt.Errorf("failed to open source APK %s: %w", filepath.Base(cleanSrc), err)
	}
	defer zr.Close()

	var originEntry *zip.File
	for _, f := range zr.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}
		if f.Name == "assets/lspatch/origin.apk" {
			originEntry = f
			break
		}
	}

	if originEntry == nil {
		return fmt.Errorf("assets/lspatch/origin.apk not found in %s", filepath.Base(cleanSrc))
	}

	if originEntry.UncompressedSize64 == 0 {
		return fmt.Errorf("assets/lspatch/origin.apk in %s is empty", filepath.Base(cleanSrc))
	}

	if err := os.MkdirAll(filepath.Dir(cleanDest), 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	outF, err := os.OpenFile(cleanDest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", cleanDest, err)
	}

	rc, err := originEntry.Open()
	if err != nil {
		_ = outF.Close()
		_ = os.Remove(cleanDest)
		return fmt.Errorf("failed to open zip entry assets/lspatch/origin.apk in %s: %w", filepath.Base(cleanSrc), err)
	}

	crcHasher := crc32.NewIEEE()
	mw := io.MultiWriter(outF, crcHasher)

	buf := make([]byte, 64*1024) // 64KB streaming buffer
	copiedBytes, copyErr := io.CopyBuffer(mw, rc, buf)
	rcCloseErr := rc.Close()
	syncErr := outF.Sync()
	closeErr := outF.Close()

	if copyErr != nil {
		_ = os.Remove(cleanDest)
		return fmt.Errorf("failed to stream copy origin.apk from %s: %w", filepath.Base(cleanSrc), copyErr)
	}
	if rcCloseErr != nil {
		_ = os.Remove(cleanDest)
		return fmt.Errorf("failed to close zip entry reader for %s: %w", filepath.Base(cleanSrc), rcCloseErr)
	}
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(cleanDest)
		return fmt.Errorf("failed to flush/close destination file %s: sync=%v, close=%v", cleanDest, syncErr, closeErr)
	}

	if copiedBytes == 0 {
		_ = os.Remove(cleanDest)
		return fmt.Errorf("extracted origin.apk from %s is 0 bytes", filepath.Base(cleanSrc))
	}

	if originEntry.UncompressedSize64 > 0 && copiedBytes != int64(originEntry.UncompressedSize64) {
		_ = os.Remove(cleanDest)
		return fmt.Errorf("extracted size mismatch for %s: expected %d bytes, got %d",
			filepath.Base(cleanSrc), originEntry.UncompressedSize64, copiedBytes)
	}

	if originEntry.CRC32 != 0 && crcHasher.Sum32() != originEntry.CRC32 {
		_ = os.Remove(cleanDest)
		return fmt.Errorf("CRC-32 checksum mismatch for origin.apk in %s: expected 0x%08X, got 0x%08X",
			filepath.Base(cleanSrc), originEntry.CRC32, crcHasher.Sum32())
	}

	return nil
}

// CheckHasNestedOrigin checks whether an APK file itself contains assets/lspatch/origin.apk.
func CheckHasNestedOrigin(apkPath string) (bool, error) {
	zr, err := zip.OpenReader(apkPath)
	if err != nil {
		return false, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			continue
		}
		if f.Name == "assets/lspatch/origin.apk" {
			return true, nil
		}
	}
	return false, nil
}

type unwrapStagedItem struct {
	stagedPath string
	manifest   *ManifestInfo
}

// UnwrapPatchedPackage extracts the embedded original APK(s) from a patched package,
// inspects their internal manifests to properly identify base vs splits, and returns
// a clean ApkPackageInfo that can be directly passed into ExecuteLSPatch.
// If any extracted APK still contains an embedded origin.apk (nested patch), it aborts
// with an error and cleans up the temporary directory.
func UnwrapPatchedPackage(info *ApkPackageInfo, workDir string) (*ApkPackageInfo, error) {
	if info == nil {
		return nil, fmt.Errorf("package info is nil")
	}
	if strings.TrimSpace(workDir) == "" {
		return nil, fmt.Errorf("workDir is required")
	}

	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create unwrap working directory %s: %w", workDir, err)
	}

	var srcApks []string
	if !info.IsSplit {
		srcApks = []string{info.BaseApkPath}
	} else {
		srcApks = append([]string{info.BaseApkPath}, info.SplitApkPaths...)
	}

	stagingDir := filepath.Join(workDir, "staged")
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}

	var stagedItems []unwrapStagedItem

	for idx, src := range srcApks {
		stagedPath := filepath.Join(stagingDir, fmt.Sprintf("staged_%d.apk", idx))
		if err := ExtractOriginFromApk(src, stagedPath); err != nil {
			_ = os.RemoveAll(workDir)
			return nil, fmt.Errorf("failed to extract original APK from %s: %w", filepath.Base(src), err)
		}

		// Enforce single-layer unwrap: reject nested patched packages
		hasNested, nErr := CheckHasNestedOrigin(stagedPath)
		if nErr == nil && hasNested {
			_ = os.RemoveAll(workDir)
			return nil, fmt.Errorf("检测到嵌套修补包: %s 解包后仍包含内嵌核心特征，已中止处理以防异常", filepath.Base(src))
		}

		manifest, mErr := parseApkManifestInfo(stagedPath)
		if mErr != nil {
			_ = os.RemoveAll(workDir)
			return nil, fmt.Errorf("failed to parse manifest from extracted APK for %s: %w", filepath.Base(src), mErr)
		}

		stagedItems = append(stagedItems, unwrapStagedItem{
			stagedPath: stagedPath,
			manifest:   manifest,
		})
	}

	if len(stagedItems) == 0 {
		_ = os.RemoveAll(workDir)
		return nil, fmt.Errorf("no APK files were extracted")
	}

	// Classify base APK vs split APKs based on the extracted manifest
	var baseItem *unwrapStagedItem
	var splitItems []unwrapStagedItem

	if len(stagedItems) == 1 {
		baseItem = &stagedItems[0]
	} else {
		for i := range stagedItems {
			item := &stagedItems[i]
			if !item.manifest.IsSplit && baseItem == nil {
				baseItem = item
			} else {
				splitItems = append(splitItems, *item)
			}
		}

		// If no APK had IsSplit == false, fallback to the first item as base
		if baseItem == nil {
			baseItem = &stagedItems[0]
			splitItems = stagedItems[1:]
		}
	}

	// Move base APK to canonical base.apk
	finalBaseApk := filepath.Join(workDir, "base.apk")
	if err := os.Rename(baseItem.stagedPath, finalBaseApk); err != nil {
		// Fallback to copy if rename fails across volume boundaries
		if cErr := copyFile(baseItem.stagedPath, finalBaseApk); cErr != nil {
			_ = os.RemoveAll(workDir)
			return nil, fmt.Errorf("failed to place base.apk: %w", cErr)
		}
		_ = os.Remove(baseItem.stagedPath)
	}

	// Move split APKs to canonical split_<name>.apk
	var finalSplitApks []string
	for idx, sItem := range splitItems {
		var splitFileName string
		if sItem.manifest.SplitName != "" {
			splitFileName = fmt.Sprintf("split_%s.apk", sItem.manifest.SplitName)
		} else {
			splitFileName = fmt.Sprintf("split_%d.apk", idx)
		}
		destPath := filepath.Join(workDir, splitFileName)
		if err := os.Rename(sItem.stagedPath, destPath); err != nil {
			if cErr := copyFile(sItem.stagedPath, destPath); cErr != nil {
				_ = os.RemoveAll(workDir)
				return nil, fmt.Errorf("failed to place split APK %s: %w", splitFileName, cErr)
			}
			_ = os.Remove(sItem.stagedPath)
		}
		finalSplitApks = append(finalSplitApks, destPath)
	}

	_ = os.RemoveAll(stagingDir)

	allExtracted := append([]string{finalBaseApk}, finalSplitApks...)
	basePkg := baseItem.manifest.PackageName
	baseVer := baseItem.manifest.VersionName

	isWebView, engineDesc, unsuppReason := CheckIsSystemWebView(allExtracted, basePkg)

	return &ApkPackageInfo{
		IsSplit:             len(finalSplitApks) > 0,
		BaseApkPath:         finalBaseApk,
		SplitApkPaths:       finalSplitApks,
		PackageName:         basePkg,
		VersionName:         baseVer,
		TotalApks:           len(allExtracted),
		IsSystemWebView:     isWebView,
		EngineDesc:          engineDesc,
		UnsupportedReason:   unsuppReason,
		IsAlreadyPatched:    false,
		PatchType:           "",
		OriginalVersionName: baseVer,
	}, nil
}
