package updater

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	updateApplyFlag       = "--apply-update"
	updateArchiveFlag     = "--update-archive"
	updateTargetFlag      = "--update-target"
	updateSHA256Flag      = "--update-sha256"
	updateVersionFlag     = "--update-version"
	updateRestartArgsFlag = "--update-restart-args"
)

type applyRequest struct {
	ArchivePath    string
	TargetPath     string
	ExpectedSHA256 string
	Version        string
	RestartArgs    []string
}

// LaunchSelfUpdater copies the current executable to a temporary helper, then
// starts that helper to apply the already-downloaded release archive after the
// main process exits. The helper is independent from the running data plane.
func LaunchSelfUpdater(archivePath, expectedSHA256, version string, restartArgs []string) error {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return fmt.Errorf("自动更新暂不支持 %s", runtime.GOOS)
	}
	if archivePath == "" {
		return fmt.Errorf("更新安装包路径为空")
	}
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("更新安装包不存在: %w", err)
	}

	currentExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("无法定位当前程序: %w", err)
	}
	currentExe, err = filepath.Abs(currentExe)
	if err != nil {
		return fmt.Errorf("无法解析当前程序路径: %w", err)
	}

	helperPath, err := copyCurrentExecutableToTemp(currentExe)
	if err != nil {
		return err
	}

	argsPath, err := writeRestartArgs(restartArgs)
	if err != nil {
		_ = os.Remove(helperPath)
		return err
	}

	args := []string{
		updateApplyFlag,
		updateArchiveFlag, archivePath,
		updateTargetFlag, currentExe,
		updateSHA256Flag, strings.ToLower(strings.TrimSpace(expectedSHA256)),
		updateVersionFlag, version,
		updateRestartArgsFlag, argsPath,
	}
	cmd := exec.Command(helperPath, args...)
	cmd.Dir = filepath.Dir(currentExe)
	if err := cmd.Start(); err != nil {
		_ = os.Remove(helperPath)
		_ = os.Remove(argsPath)
		return fmt.Errorf("无法启动更新助手: %w", err)
	}
	return nil
}

// HandleApplyArgs detects the private updater-helper mode and applies the
// archive. It returns handled=true when the current process is an updater
// helper and should not continue through normal application startup.
func HandleApplyArgs(args []string) (handled bool, err error) {
	if !hasFlag(args, updateApplyFlag) {
		return false, nil
	}

	req, parseErr := parseApplyArgs(args)
	if parseErr != nil {
		return true, parseErr
	}
	if applyErr := applyUpdate(req); applyErr != nil {
		// The helper owns the only remaining running copy at this point. Keep the
		// old executable intact whenever replacement fails, then restart it.
		_ = launchRestartTarget(req.TargetPath, req.RestartArgs)
		return true, applyErr
	}
	return true, nil
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func parseApplyArgs(args []string) (applyRequest, error) {
	var req applyRequest
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case updateArchiveFlag:
			i++
			if i >= len(args) {
				return req, fmt.Errorf("缺少 %s 参数", updateArchiveFlag)
			}
			req.ArchivePath = args[i]
		case updateTargetFlag:
			i++
			if i >= len(args) {
				return req, fmt.Errorf("缺少 %s 参数", updateTargetFlag)
			}
			req.TargetPath = args[i]
		case updateSHA256Flag:
			i++
			if i >= len(args) {
				return req, fmt.Errorf("缺少 %s 参数", updateSHA256Flag)
			}
			req.ExpectedSHA256 = args[i]
		case updateVersionFlag:
			i++
			if i >= len(args) {
				return req, fmt.Errorf("缺少 %s 参数", updateVersionFlag)
			}
			req.Version = args[i]
		case updateRestartArgsFlag:
			i++
			if i >= len(args) {
				return req, fmt.Errorf("缺少 %s 参数", updateRestartArgsFlag)
			}
			data, err := os.ReadFile(args[i])
			if err != nil {
				return req, fmt.Errorf("读取重启参数失败: %w", err)
			}
			if err := json.Unmarshal(data, &req.RestartArgs); err != nil {
				return req, fmt.Errorf("解析重启参数失败: %w", err)
			}
			_ = os.Remove(args[i])
		}
	}
	if req.ArchivePath == "" || req.TargetPath == "" {
		return req, fmt.Errorf("更新参数不完整")
	}
	return req, nil
}

func applyUpdate(req applyRequest) error {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		return fmt.Errorf("自动更新暂不支持 %s", runtime.GOOS)
	}

	archivePath, err := filepath.Abs(req.ArchivePath)
	if err != nil {
		return fmt.Errorf("解析安装包路径失败: %w", err)
	}
	targetPath, err := filepath.Abs(req.TargetPath)
	if err != nil {
		return fmt.Errorf("解析目标程序路径失败: %w", err)
	}

	if err := verifyArchiveSHA256(archivePath, req.ExpectedSHA256); err != nil {
		return err
	}

	if runtime.GOOS == "darwin" {
		return applyMacBundle(archivePath, targetPath, req.RestartArgs)
	}
	return applyWindowsExecutable(archivePath, targetPath, req.RestartArgs)
}

func applyWindowsExecutable(archivePath, targetPath string, restartArgs []string) error {
	stagePath, err := extractWindowsExecutable(archivePath, filepath.Dir(targetPath))
	if err != nil {
		return err
	}
	defer os.Remove(stagePath)

	backupPath := targetPath + ".update-backup"
	_ = os.Remove(backupPath)

	// Prepare a verified backup before touching the live executable. Reading a
	// running PE file is allowed on Windows even while the image remains loaded.
	if err := copyFile(targetPath, backupPath); err != nil {
		return fmt.Errorf("无法创建旧版本备份: %w", err)
	}

	var lastErr error
	for i := 0; i < 60; i++ {
		if err := os.Rename(targetPath, backupPath); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		// The parent may still be shutting down; leave the old executable intact.
		return fmt.Errorf("等待旧程序释放文件失败: %w", lastErr)
	}

	if err := os.Rename(stagePath, targetPath); err != nil {
		_ = os.Rename(backupPath, targetPath)
		return fmt.Errorf("替换新版程序失败，已尝试恢复旧版本: %w", err)
	}

	_ = os.Remove(backupPath)
	if err := launchRestartTarget(targetPath, restartArgs); err != nil {
		return fmt.Errorf("新版本已替换，但自动重启失败: %w", err)
	}
	return nil
}

func applyMacBundle(archivePath, targetExePath string, restartArgs []string) error {
	currentBundle, err := macBundleRoot(targetExePath)
	if err != nil {
		return err
	}

	stageDir, err := os.MkdirTemp(filepath.Dir(currentBundle), ".gbf-update-*")
	if err != nil {
		return fmt.Errorf("无法创建 macOS 更新暂存目录: %w", err)
	}
	defer os.RemoveAll(stageDir)

	stageBundle, err := extractMacBundle(archivePath, stageDir)
	if err != nil {
		return err
	}
	backupBundle := currentBundle + ".update-backup"
	_ = os.RemoveAll(backupBundle)

	var lastErr error
	for i := 0; i < 60; i++ {
		if err := os.Rename(currentBundle, backupBundle); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("等待 macOS 应用释放文件失败: %w", lastErr)
	}

	if err := os.Rename(stageBundle, currentBundle); err != nil {
		_ = os.Rename(backupBundle, currentBundle)
		return fmt.Errorf("替换 macOS 应用失败，已尝试恢复旧版本: %w", err)
	}

	_ = os.RemoveAll(backupBundle)
	if err := launchRestartTarget(targetExePath, restartArgs); err != nil {
		return fmt.Errorf("新版本已替换，但自动重启失败: %w", err)
	}
	return nil
}

func extractWindowsExecutable(archivePath, targetDir string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("打开更新压缩包失败: %w", err)
	}
	defer zr.Close()

	var chosen *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || f.FileInfo().Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := filepath.ToSlash(f.Name)
		if !isSafeZipPath(name) || strings.Contains(name, "/") {
			continue
		}
		if strings.EqualFold(name, "GBF_Accelerator.exe") {
			chosen = f
			break
		}
		if chosen == nil && strings.HasSuffix(strings.ToLower(name), ".exe") {
			chosen = f
		}
	}
	if chosen == nil {
		return "", fmt.Errorf("更新压缩包中未找到 Windows 可执行文件")
	}

	stage, err := os.CreateTemp(targetDir, ".gbf-update-*.exe")
	if err != nil {
		return "", fmt.Errorf("无法创建新版程序暂存文件: %w", err)
	}
	stagePath := stage.Name()
	ok := false
	defer func() {
		_ = stage.Close()
		if !ok {
			_ = os.Remove(stagePath)
		}
	}()

	rc, err := chosen.Open()
	if err != nil {
		return "", fmt.Errorf("无法读取新版程序: %w", err)
	}
	_, copyErr := io.Copy(stage, rc)
	_ = rc.Close()
	if copyErr != nil {
		return "", fmt.Errorf("解压新版程序失败: %w", copyErr)
	}
	if err := stage.Close(); err != nil {
		return "", fmt.Errorf("关闭新版程序暂存文件失败: %w", err)
	}
	if err := os.Chmod(stagePath, 0755); err != nil {
		return "", fmt.Errorf("设置新版程序权限失败: %w", err)
	}
	ok = true
	return stagePath, nil
}

func extractMacBundle(archivePath, stageDir string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("打开 macOS 更新压缩包失败: %w", err)
	}
	defer zr.Close()

	bundleRoot := ""
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if !isSafeZipPath(name) {
			continue
		}
		parts := strings.Split(strings.Trim(name, "/"), "/")
		for _, p := range parts {
			if strings.HasSuffix(strings.ToLower(p), ".app") {
				bundleRoot = p
				break
			}
		}
		if bundleRoot != "" {
			break
		}
	}
	if bundleRoot == "" {
		return "", fmt.Errorf("更新压缩包中未找到 macOS .app")
	}

	stageBundle := filepath.Join(stageDir, bundleRoot)
	for _, f := range zr.File {
		name := filepath.ToSlash(strings.Trim(f.Name, "/"))
		if name == "" || !isSafeZipPath(name) {
			continue
		}
		parts := strings.Split(name, "/")
		if len(parts) == 0 || parts[0] != bundleRoot {
			continue
		}
		target := filepath.Join(stageDir, filepath.FromSlash(name))
		targetClean := filepath.Clean(target)
		rel, err := filepath.Rel(stageDir, targetClean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("更新压缩包包含非法路径: %s", f.Name)
		}
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("更新压缩包包含不支持的符号链接: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetClean, 0755); err != nil {
				return "", fmt.Errorf("创建更新目录失败: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetClean), 0755); err != nil {
			return "", fmt.Errorf("创建更新目录失败: %w", err)
		}
		out, err := os.Create(targetClean)
		if err != nil {
			return "", fmt.Errorf("创建更新文件失败: %w", err)
		}
		rc, err := f.Open()
		if err != nil {
			_ = out.Close()
			return "", fmt.Errorf("读取更新文件失败: %w", err)
		}
		_, copyErr := io.Copy(out, rc)
		_ = rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return "", fmt.Errorf("解压更新文件失败: %w", copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("关闭更新文件失败: %w", closeErr)
		}
		_ = os.Chmod(targetClean, 0755)
	}
	return stageBundle, nil
}

func macBundleRoot(targetExe string) (string, error) {
	sep := string(os.PathSeparator)
	lower := strings.ToLower(filepath.Clean(targetExe))
	marker := ".app" + sep
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return "", fmt.Errorf("当前 macOS 程序路径不在 .app 包内，无法安全自动更新")
	}
	return filepath.Clean(targetExe[:idx+len(".app")]), nil
}

func verifyArchiveSHA256(path, expected string) error {
	expected = strings.ToLower(strings.TrimSpace(expected))
	if expected == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取更新安装包失败: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("计算更新安装包 SHA-256 失败: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return fmt.Errorf("更新安装包 SHA-256 校验失败")
	}
	return nil
}

func copyCurrentExecutableToTemp(currentExe string) (string, error) {
	ext := filepath.Ext(currentExe)
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	f, err := os.CreateTemp(os.TempDir(), "gbf-accelerator-updater-*"+ext)
	if err != nil {
		return "", fmt.Errorf("无法创建更新助手: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("无法准备更新助手: %w", err)
	}
	if err := copyFile(currentExe, path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("复制更新助手失败: %w", err)
	}
	_ = os.Chmod(path, 0755)
	return path, nil
}

func writeRestartArgs(args []string) (string, error) {
	f, err := os.CreateTemp(os.TempDir(), "gbf-accelerator-restart-*.json")
	if err != nil {
		return "", fmt.Errorf("无法创建重启参数文件: %w", err)
	}
	path := f.Name()
	data, err := json.Marshal(args)
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("无法序列化重启参数: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("无法写入重启参数: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("无法关闭重启参数文件: %w", err)
	}
	return path, nil
}

func launchRestartTarget(target string, args []string) error {
	cmd := exec.Command(target, args...)
	cmd.Dir = filepath.Dir(target)
	if err := cmd.Start(); err != nil {
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func isSafeZipPath(name string) bool {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return false
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == ".." {
			return false
		}
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	return clean == name && clean != ".." && !strings.HasPrefix(clean, "../")
}

// CleanupStaleHelpers removes old updater helper files left behind by Windows,
// where a running process cannot delete its own executable image.
func CleanupStaleHelpers() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-10 * time.Minute)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "gbf-accelerator-updater-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(os.TempDir(), name))
	}
}
