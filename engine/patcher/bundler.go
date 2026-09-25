package patcher

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type BundleResult struct {
	IsSplit      bool     `json:"is_split"`
	SingleApk    string   `json:"single_apk"`
	SplitDir     string   `json:"split_dir"`
	ApksArchive  string   `json:"apks_archive"`
	TotalApks    int      `json:"total_apks"`
	TotalBytes   int64    `json:"total_bytes"`
	PackageFiles []string `json:"package_files"`
	BackupPath   string   `json:"backup_path,omitempty"`
	BackupDir    string   `json:"backup_dir,omitempty"`
}

// BundleOutput organizes the patched APKs produced by LSPatch into the final user output directory.
func BundleOutput(isSplit bool, originalBaseName string, rawApks []string, targetOutputDir string) (*BundleResult, error) {
	if err := os.MkdirAll(targetOutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target output directory: %w", err)
	}

	cleanBaseName := strings.TrimSuffix(originalBaseName, filepath.Ext(originalBaseName))
	if cleanBaseName == "" {
		cleanBaseName = "skyleap"
	}

	result := &BundleResult{
		IsSplit:      isSplit,
		TotalApks:    len(rawApks),
		PackageFiles: make([]string, 0, len(rawApks)),
	}

	if !isSplit {
		// Single APK output
		if len(rawApks) == 0 {
			return nil, fmt.Errorf("no patched APK available to bundle")
		}
		destName := cleanBaseName + "-patched.apk"
		destPath := filepath.Join(targetOutputDir, destName)

		if err := copyFile(rawApks[0], destPath); err != nil {
			return nil, fmt.Errorf("failed to copy patched APK to output: %w", err)
		}

		fi, err := os.Stat(destPath)
		if err != nil || fi.Size() == 0 {
			return nil, fmt.Errorf("output APK %s is missing or empty", destPath)
		}

		result.SingleApk = destPath
		result.TotalBytes = fi.Size()
		result.PackageFiles = append(result.PackageFiles, destPath)
		return result, nil
	}

	// Split APK output:
	// 1. Copy splits to targetOutputDir/splits/
	splitDir := filepath.Join(targetOutputDir, cleanBaseName+"-splits")
	if err := os.MkdirAll(splitDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create split output directory: %w", err)
	}

	var copiedSplits []string
	var totalBytes int64

	for _, raw := range rawApks {
		fname := filepath.Base(raw)
		// Clean up name if it has -lspatched
		destFile := filepath.Join(splitDir, fname)
		if err := copyFile(raw, destFile); err != nil {
			return nil, fmt.Errorf("failed to copy split APK %s: %w", fname, err)
		}
		fi, err := os.Stat(destFile)
		if err != nil || fi.Size() == 0 {
			return nil, fmt.Errorf("copied split APK %s is missing or empty", destFile)
		}
		totalBytes += fi.Size()
		copiedSplits = append(copiedSplits, destFile)
	}

	result.SplitDir = splitDir
	result.TotalBytes = totalBytes
	result.PackageFiles = copiedSplits

	// 2. Build an aggregated .apks ZIP archive containing all splits
	apksPath := filepath.Join(targetOutputDir, cleanBaseName+"-patched.apks")
	if err := createApksArchive(copiedSplits, apksPath); err != nil {
		return nil, fmt.Errorf("failed to generate .apks bundle archive: %w", err)
	}
	result.ApksArchive = apksPath

	return result, nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func createApksArchive(apkFiles []string, destZipPath string) error {
	outZip, err := os.Create(destZipPath)
	if err != nil {
		return err
	}
	defer outZip.Close()

	zw := zip.NewWriter(outZip)
	defer zw.Close()

	for _, apkPath := range apkFiles {
		fi, err := os.Stat(apkPath)
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		header.Name = filepath.Base(apkPath)
		// APK files inside APKS should be stored (no extra compression)
		header.Method = zip.Store

		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		f, err := os.Open(apkPath)
		if err != nil {
			return err
		}

		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// VerifyIntegrity conducts a rigorous post-packaging validation of the generated output artifacts.
// It ensures that:
// 1. All output files exist, are regular non-empty files.
// 2. Base APK is present and non-empty.
// 3. For Split APKs, split count matches the inspected input count.
// 4. Output APKs / APKS archives are valid zip files.
// 5. Total output bytes > 0.
func VerifyIntegrity(res *BundleResult, expectedIsSplit bool, expectedTotalApks int) error {
	if res == nil {
		return fmt.Errorf("integrity audit failed: result is nil")
	}

	if res.IsSplit != expectedIsSplit {
		return fmt.Errorf("integrity audit failed: split mode mismatch (expected isSplit=%v, got %v)", expectedIsSplit, res.IsSplit)
	}

	if res.TotalBytes <= 0 {
		return fmt.Errorf("integrity audit failed: output artifact total size is 0 bytes")
	}

	if !expectedIsSplit {
		// Single APK verification
		if res.SingleApk == "" {
			return fmt.Errorf("integrity audit failed: single APK path is empty")
		}
		fi, err := os.Stat(res.SingleApk)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			return fmt.Errorf("integrity audit failed: output APK %s is missing or empty", res.SingleApk)
		}
		zr, err := zip.OpenReader(res.SingleApk)
		if err != nil {
			return fmt.Errorf("integrity audit failed: output APK %s is not a valid zip archive: %w", res.SingleApk, err)
		}
		zr.Close()
		return nil
	}

	// Split APK verification
	if res.TotalApks != expectedTotalApks {
		return fmt.Errorf("integrity audit failed: split count mismatch (expected %d APKs, got %d)", expectedTotalApks, res.TotalApks)
	}

	if res.SplitDir == "" {
		return fmt.Errorf("integrity audit failed: split output directory path is empty")
	}
	dirFi, err := os.Stat(res.SplitDir)
	if err != nil || !dirFi.IsDir() {
		return fmt.Errorf("integrity audit failed: split directory %s does not exist", res.SplitDir)
	}

	if len(res.PackageFiles) != expectedTotalApks {
		return fmt.Errorf("integrity audit failed: package files count %d does not match expected %d", len(res.PackageFiles), expectedTotalApks)
	}

	hasBaseApk := false
	for _, fpath := range res.PackageFiles {
		fi, err := os.Stat(fpath)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			return fmt.Errorf("integrity audit failed: split file %s is missing or empty", fpath)
		}
		fname := strings.ToLower(filepath.Base(fpath))
		if strings.HasPrefix(fname, "base") {
			hasBaseApk = true
		} else if _, _, isSplit, err := parseApkMetadata(fpath); err == nil && !isSplit {
			hasBaseApk = true
		} else if !strings.HasPrefix(fname, "config.") && !strings.HasPrefix(fname, "split_") {
			hasBaseApk = true
		}
		zr, err := zip.OpenReader(fpath)
		if err != nil {
			return fmt.Errorf("integrity audit failed: split file %s is corrupt: %w", fpath, err)
		}
		zr.Close()
	}

	if !hasBaseApk {
		return fmt.Errorf("integrity audit failed: base APK was not found among output split files")
	}

	if res.ApksArchive == "" {
		return fmt.Errorf("integrity audit failed: .apks archive path is empty")
	}
	apksFi, err := os.Stat(res.ApksArchive)
	if err != nil || apksFi.IsDir() || apksFi.Size() == 0 {
		return fmt.Errorf("integrity audit failed: .apks archive %s is missing or empty", res.ApksArchive)
	}

	zr, err := zip.OpenReader(res.ApksArchive)
	if err != nil {
		return fmt.Errorf("integrity audit failed: .apks archive %s is not a valid zip archive: %w", res.ApksArchive, err)
	}
	defer zr.Close()

	if len(zr.File) != expectedTotalApks {
		return fmt.Errorf("integrity audit failed: .apks archive contains %d entries, expected %d", len(zr.File), expectedTotalApks)
	}

	return nil
}
