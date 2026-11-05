//go:build darwin
// +build darwin

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
)

var (
	renameFunc    = os.Rename
	removeAllFunc = os.RemoveAll
	removeFunc    = os.Remove
)

const (
	maxMigrateBodySize = 4096

	ErrCodeMigrationTargetNotEmpty     = "CACHE_MIGRATION_TARGET_NOT_EMPTY"
	ErrCodeMigrationTargetUnconfirmed  = "CACHE_MIGRATION_TARGET_UNCONFIRMED"
	ErrCodeMigrationSourceUnconfirmed  = "CACHE_MIGRATION_SOURCE_UNCONFIRMED"
	ErrCodeMigrationSourceChanged      = "CACHE_MIGRATION_SOURCE_CHANGED"
	ErrCodeMigrationReplacementFailed  = "CACHE_MIGRATION_REPLACEMENT_FAILED"
	ErrCodeMigrationRollbackFailed     = "CACHE_MIGRATION_ROLLBACK_FAILED"
	ErrCodeMigrationConfigCommitFailed = "CACHE_MIGRATION_CONFIG_COMMIT_FAILED"
	ErrCodeMigrationInvalidMode        = "CACHE_MIGRATION_INVALID_MODE"
)

type migrateStandardRequest struct {
	Mode string `json:"mode"`
}

func getStandardCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "GBF-Accelerator", "cache")
}

var standardCacheDirProvider = getStandardCacheDir
var beforeMigrationApplyHook func()

func isStandardCacheDir(cacheDir string) bool {
	if cacheDir == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}

	cleanTarget := filepath.Clean(cacheDir)
	stdBase := filepath.Clean(filepath.Join(home, "Library", "Application Support", "GBF-Accelerator"))
	stdCaches := filepath.Clean(filepath.Join(home, "Library", "Caches", "GBF-Accelerator"))

	// Check if directory resides within macOS Application Support or Caches for GBF-Accelerator
	if cleanTarget == stdBase || strings.HasPrefix(cleanTarget, stdBase+string(filepath.Separator)) {
		return true
	}
	if cleanTarget == stdCaches || strings.HasPrefix(cleanTarget, stdCaches+string(filepath.Separator)) {
		return true
	}
	return false
}

func isSameOrNestedPath(pathA, pathB string) bool {
	cleanA := filepath.Clean(pathA)
	cleanB := filepath.Clean(pathB)
	if cleanA == cleanB {
		return true
	}
	relA, errA := filepath.Rel(cleanA, cleanB)
	if errA == nil && relA != "." && !strings.HasPrefix(relA, "..") {
		return true
	}
	relB, errB := filepath.Rel(cleanB, cleanA)
	if errB == nil && relB != "." && !strings.HasPrefix(relB, "..") {
		return true
	}
	return false
}

// ensureNoSymlinkPathComponents rejects paths with symlink components or non-directory ancestors.
func ensureNoSymlinkPathComponents(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(abs)
	cur := volume + string(filepath.Separator)
	rest := strings.TrimPrefix(abs, cur)
	for _, seg := range strings.Split(rest, string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		fi, statErr := os.Lstat(cur)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("路径组件不能是符号链接: %s", cur)
		}
		if cur != abs && !fi.IsDir() {
			return fmt.Errorf("路径祖先不是目录: %s", cur)
		}
	}
	return nil
}

// validateCacheMigrationTree fails closed if a source contains files whose cache ownership
// cannot be established from the simplified Tier-1/Tier-2 proof used by cache maintenance.
// Residual .tmp., .quarantine, and orphan .ext files do not block migration and are purged during staging.
func validateCacheMigrationTree(root string) error {
	fi, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("迁移源必须是普通目录: %s", root)
	}
	hasMarker := cache.HasRootMarker(root)

	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("迁移目录包含符号链接或特殊文件: %s", path)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		slashRel := filepath.ToSlash(rel)
		if entry.IsDir() {
			if !cache.IsConfirmedCacheDirectory(slashRel) {
				return fmt.Errorf("迁移目录包含无法确认的目录: %s", rel)
			}
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("迁移目录包含非普通文件: %s", path)
		}
		if slashRel == cache.RootMarkerFileName {
			if !hasMarker {
				return fmt.Errorf("缓存根标记无效: %s", path)
			}
			return nil
		}
		if config.IsSystemJunk(filepath.Base(path)) {
			return nil
		}

		// Residual accelerator temp and quarantine files do NOT block migration (purged in staging)
		if cache.IsAcceleratorTempFile(slashRel) || cache.IsAcceleratorQuarantineFile(slashRel) {
			return nil
		}

		// Metadata file (.ext)
		if strings.HasSuffix(slashRel, ".ext") {
			assetPath := strings.TrimSuffix(path, ".ext")
			if assetFi, aErr := os.Lstat(assetPath); aErr == nil && assetFi.Mode().IsRegular() {
				if metaBytes, rErr := os.ReadFile(path); rErr == nil {
					assetSlashRel := strings.TrimSuffix(slashRel, ".ext")
					if _, ok := cache.VerifyTier1Meta(assetSlashRel, metaBytes); !ok {
						return fmt.Errorf("迁移目录包含无法配对验证的元数据: %s", rel)
					}
				} else {
					return fmt.Errorf("迁移目录包含无法读取的元数据: %s", rel)
				}
			} else {
				// Orphan .ext file: verify top-level directory shape; orphan .ext does not block migration
				if !cache.IsConfirmedCacheDirectory(filepath.ToSlash(filepath.Dir(slashRel))) {
					return fmt.Errorf("迁移目录包含未知路径的孤儿元数据: %s", rel)
				}
			}
			return nil
		}

		// Regular data file:
		extPath := path + ".ext"
		if extFi, extErr := os.Lstat(extPath); extErr == nil && extFi.Mode().IsRegular() {
			metaBytes, rErr := os.ReadFile(extPath)
			if rErr != nil {
				return fmt.Errorf("迁移目录包含无法读取的元数据: %s.ext", rel)
			}
			if _, ok := cache.VerifyTier1Meta(slashRel, metaBytes); !ok {
				return fmt.Errorf("迁移目录包含无法确认归属的文件: %s", rel)
			}
			return nil
		}

		// Data file without valid paired .ext is unconfirmed
		return fmt.Errorf("迁移目录包含未纳入元数据确权的文件 (缺少有效 .ext): %s", rel)
	})
}

// hasConfirmedCacheAssets checks whether dir contains at least one confirmed valid GBF cache asset.
func hasConfirmedCacheAssets(dir string) (bool, error) {
	hasMarker := cache.HasRootMarker(dir)
	hasAssets := false

	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil
		}
		if cache.IsConfirmedCacheFileWithInfo(dir, path, info, hasMarker) {
			hasAssets = true
			return filepath.SkipAll
		}
		return nil
	})

	return hasAssets, err
}

// FindResidualMigrationBackups returns all uncleaned .gbf-cache-old-* backup directories in parentDir.
func FindResidualMigrationBackups(parentDir string) ([]string, error) {
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return nil, err
	}
	var backups []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".gbf-cache-old-") {
			backups = append(backups, filepath.Join(parentDir, entry.Name()))
		}
	}
	sort.Strings(backups)
	return backups, nil
}

// RecoverResidualMigrationBackup restores an uncleaned backup directory to targetDir if targetDir is missing or empty.
func RecoverResidualMigrationBackup(backupDir, targetDir string) error {
	bFi, err := os.Lstat(backupDir)
	if err != nil {
		return fmt.Errorf("备份目录不存在或无法访问: %w", err)
	}
	if bFi.Mode()&os.ModeSymlink != 0 || !bFi.IsDir() {
		return fmt.Errorf("备份目录必须是普通目录: %s", backupDir)
	}
	baseName := filepath.Base(backupDir)
	if !strings.HasPrefix(baseName, ".gbf-cache-old-") {
		return fmt.Errorf("非法备份目录名称: %s", baseName)
	}

	if err := validateEmptyMigrationTarget(targetDir); err != nil {
		return err
	}
	if _, err := os.Lstat(targetDir); err == nil {
		if err := removeFunc(targetDir); err != nil {
			return fmt.Errorf("移除空目标目录以便恢复失败: %w", err)
		}
	}

	return renameFunc(backupDir, targetDir)
}

// validateCacheOverwriteTarget performs structural sandbox validation on the target directory.
// It ensures target directory is a regular directory containing only recognized cache subdirectories
// (e.g. assets, hosts), standard metadata (.gbf_cache), or OS junk (.DS_Store), rejecting unknown files or directories.
func validateCacheOverwriteTarget(root string) error {
	fi, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("覆盖目标不是普通目录: %s", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("目标目录包含符号链接或特殊文件: %s", filepath.Join(root, name))
		}
		if entry.IsDir() {
			if !cache.IsConfirmedCacheDirectory(name) {
				return fmt.Errorf("目标目录包含未通过确权的目录: %s", name)
			}
			continue
		}

		// Files at root level:
		if name == cache.RootMarkerFileName || config.IsSystemJunk(name) {
			continue
		}
		if name == "favicon.ico" || name == "favicon.ico.ext" {
			continue
		}
		if cache.IsAcceleratorTempFile(name) || cache.IsAcceleratorQuarantineFile(name) {
			return fmt.Errorf("目标目录包含未纳入严格确权的杂项或残留文件: %s", name)
		}
		return fmt.Errorf("目标目录包含未通过确权的文件: %s", name)
	}

	return nil
}

func validateEmptyMigrationTarget(target string) error {
	fi, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("迁移目标不是普通目录: %s", target)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("迁移目标目录并非空目录，拒绝覆盖现有内容: %s", target)
	}
	return nil
}

func validateTargetForMode(dir, mode string) (string, error) {
	if mode == "overwrite" {
		if err := validateCacheOverwriteTarget(dir); err != nil {
			return ErrCodeMigrationTargetUnconfirmed, fmt.Errorf("目标目录包含无法确认为 GBF 缓存的文件，拒绝覆盖现有内容: %w", err)
		}
	} else {
		if err := validateEmptyMigrationTarget(dir); err != nil {
			return ErrCodeMigrationTargetNotEmpty, err
		}
	}
	return "", nil
}

func parseMigrateRequestMode(r io.Reader) (string, error) {
	if r == nil {
		return "migrate", nil
	}
	limitedReader := io.LimitReader(r, maxMigrateBodySize+1)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", errors.New("读取请求体失败")
	}
	if len(bodyBytes) > maxMigrateBodySize {
		return "", fmt.Errorf("请求体大小超过限制 (%d 字节)", maxMigrateBodySize)
	}
	trimmed := bytes.TrimSpace(bodyBytes)
	if len(trimmed) == 0 {
		return "migrate", nil
	}
	var mReq migrateStandardRequest
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&mReq); err != nil {
		return "", fmt.Errorf("请求体 JSON 格式无效: %v", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return "", errors.New("请求体包含多余的 JSON 内容或非法数据")
	}
	switch mReq.Mode {
	case "", "migrate":
		return "migrate", nil
	case "switch_only":
		return "switch_only", nil
	case "overwrite":
		return "overwrite", nil
	default:
		return "", fmt.Errorf("不支持的操作模式: %q", mReq.Mode)
	}
}

func (c *ControlServer) handleMigrateStandardCache(w http.ResponseWriter, req *http.Request) {
	taskID, _, ok := c.tryBeginCacheTask("migrate_standard")
	if !ok {
		c.sendTaskConflict(w)
		return
	}

	fail := func(status int, code string, msg string, kv ...string) {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		resp := map[string]interface{}{"ok": false, "error": msg}
		if code != "" {
			resp["code"] = code
		}
		for i := 0; i+1 < len(kv); i += 2 {
			resp[kv[i]] = kv[i+1]
		}
		c.sendJSON(w, status, resp)
	}

	targetDir := standardCacheDirProvider()
	if targetDir == "" {
		fail(http.StatusInternalServerError, "", "无法获取当前用户的 Home 目录")
		return
	}

	reqMode, err := parseMigrateRequestMode(req.Body)
	if err != nil {
		fail(http.StatusBadRequest, ErrCodeMigrationInvalidMode, err.Error())
		return
	}

	curCfg := c.cfgMgr.Get()
	sourceDir := curCfg.CacheDir

	// Check if already pointing to the exact same standard directory
	if filepath.Clean(sourceDir) == filepath.Clean(targetDir) {
		c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": sourceDir}, "")
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"new_dir": sourceDir,
			"message": "当前缓存目录已是 macOS 标准目录",
		})
		return
	}

	// Reject self-overlap or parent-child nesting in overwrite mode
	if reqMode == "overwrite" && isSameOrNestedPath(sourceDir, targetDir) {
		fail(http.StatusBadRequest, ErrCodeMigrationInvalidMode, "源目录与目标目录相同或存在路径嵌套，拒绝覆盖操作")
		return
	}

	// Mode: switch_only
	if reqMode == "switch_only" {
		if err := config.ValidateCacheDir(targetDir); err != nil {
			fail(http.StatusBadRequest, "", fmt.Sprintf("标准缓存目标路径校验失败: %v", err))
			return
		}
		if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
			fail(http.StatusBadRequest, "", fmt.Sprintf("迁移目标路径校验失败: %v", err))
			return
		}

		targetCreatedByUs := false
		if tFi, tErr := os.Lstat(targetDir); tErr != nil {
			if os.IsNotExist(tErr) {
				if err := os.MkdirAll(targetDir, 0755); err != nil {
					fail(http.StatusInternalServerError, "", fmt.Sprintf("创建标准缓存目录失败: %v", err))
					return
				}
				targetCreatedByUs = true
			} else {
				fail(http.StatusInternalServerError, "", tErr.Error())
				return
			}
		} else {
			if tFi.Mode()&os.ModeSymlink != 0 || !tFi.IsDir() {
				fail(http.StatusBadRequest, "", "迁移目标不是普通目录")
				return
			}
		}

		if beforeMigrationApplyHook != nil {
			beforeMigrationApplyHook()
		}

		c.applyMu.Lock()
		defer c.applyMu.Unlock()

		latestCfg := c.cfgMgr.Candidate()
		if filepath.Clean(latestCfg.CacheDir) != filepath.Clean(sourceDir) {
			fail(http.StatusConflict, ErrCodeMigrationSourceChanged, "操作期间源缓存目录已被其他操作修改")
			return
		}

		candidate := latestCfg
		candidate.CacheDir = targetDir

		if err := c.cfgMgr.Commit(candidate); err != nil {
			if targetCreatedByUs {
				_ = removeFunc(targetDir) // Safe cleanup of empty directory created by us
			}
			fail(http.StatusInternalServerError, ErrCodeMigrationConfigCommitFailed, fmt.Sprintf("配置提交失败: %v", err))
			return
		}

		c.cacheMgr.SetCacheBase(targetDir)
		c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": targetDir}, "")
		if c.stats != nil {
			c.stats.Log("INFO", fmt.Sprintf("[CACHE] 缓存目录已切换至 macOS 标准应用数据目录: %s", targetDir))
		}
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"mode":    "switch_only",
			"new_dir": targetDir,
			"message": "缓存目录已成功切换至 macOS 标准目录",
		})
		return
	}

	// Common validation for migrate and overwrite modes
	if err := ensureNoSymlinkPathComponents(sourceDir); err != nil {
		fail(http.StatusBadRequest, "", fmt.Sprintf("迁移源路径校验失败: %v", err))
		return
	}
	if err := config.ValidateCacheDir(targetDir); err != nil {
		fail(http.StatusBadRequest, "", fmt.Sprintf("标准缓存目标路径校验失败: %v", err))
		return
	}
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		fail(http.StatusBadRequest, "", fmt.Sprintf("迁移目标路径校验失败: %v", err))
		return
	}

	sourceInfo, statErr := os.Lstat(sourceDir)
	if reqMode == "overwrite" {
		if statErr != nil {
			fail(http.StatusBadRequest, ErrCodeMigrationSourceUnconfirmed, fmt.Sprintf("源缓存目录不存在或无法访问: %v", statErr))
			return
		}
		if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
			fail(http.StatusBadRequest, ErrCodeMigrationSourceUnconfirmed, "迁移源必须是普通目录，拒绝处理符号链接或文件")
			return
		}
		if err := validateCacheMigrationTree(sourceDir); err != nil {
			fail(http.StatusConflict, ErrCodeMigrationSourceUnconfirmed, fmt.Sprintf("迁移已停止，源目录包含无法确认归属的数据，原目录保持不变: %v", err))
			return
		}
		hasAssets, err := hasConfirmedCacheAssets(sourceDir)
		if err != nil {
			fail(http.StatusInternalServerError, ErrCodeMigrationSourceUnconfirmed, fmt.Sprintf("检查源缓存内容失败: %v", err))
			return
		}
		if !hasAssets {
			fail(http.StatusConflict, ErrCodeMigrationSourceUnconfirmed, "源缓存目录为空或未包含任何有效缓存素材，拒绝覆盖已有目标缓存")
			return
		}
	} else {
		// In "migrate" mode, allow non-existent source directory for backward compatibility
		if statErr == nil {
			if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
				fail(http.StatusBadRequest, "", "迁移源必须是普通目录，拒绝处理符号链接或文件")
				return
			}
			if err := validateCacheMigrationTree(sourceDir); err != nil {
				fail(http.StatusConflict, ErrCodeMigrationSourceUnconfirmed, fmt.Sprintf("迁移已停止，源目录包含无法确认归属的数据，原目录保持不变: %v", err))
				return
			}
		} else if !os.IsNotExist(statErr) {
			fail(http.StatusInternalServerError, "", statErr.Error())
			return
		}
	}

	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		fail(http.StatusInternalServerError, "", fmt.Sprintf("创建标准缓存父目录失败: %v", err))
		return
	}
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		fail(http.StatusBadRequest, "", fmt.Sprintf("迁移目标路径校验失败: %v", err))
		return
	}

	// Pre-check target before staging
	if errCode, err := validateTargetForMode(targetDir, reqMode); err != nil {
		fail(http.StatusConflict, errCode, err.Error(), "target_dir", targetDir)
		return
	}

	// Stage source cache in the target's parent directory
	stageDir, err := os.MkdirTemp(parentDir, ".gbf-cache-migration-*")
	if err != nil {
		fail(http.StatusInternalServerError, "", fmt.Sprintf("创建迁移暂存目录失败: %v", err))
		return
	}
	stageCommitted := false
	defer func() {
		if !stageCommitted {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if sourceInfo, statErr := os.Lstat(sourceDir); statErr == nil && sourceInfo.IsDir() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cpCmd := exec.CommandContext(ctx, "cp", "-cR", sourceDir+string(filepath.Separator)+".", stageDir)
		out, cpErr := cpCmd.CombinedOutput()
		if cpErr != nil {
			fallbackCmd := exec.CommandContext(ctx, "cp", "-R", sourceDir+string(filepath.Separator)+".", stageDir)
			fbOut, fbErr := fallbackCmd.CombinedOutput()
			if fbErr != nil {
				fail(http.StatusInternalServerError, "", fmt.Sprintf("复制缓存至暂存目录失败: %v: %s (clone: %s)", fbErr, string(fbOut), string(out)))
				return
			}
		}
		// Purge residual streaming temp files, quarantine files, and orphan .ext from stageDir
		purgeErr := filepath.WalkDir(stageDir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(stageDir, p)
			if err != nil {
				return err
			}
			slashRel := filepath.ToSlash(rel)
			if cache.IsAcceleratorTempFile(slashRel) || cache.IsAcceleratorQuarantineFile(slashRel) {
				if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
					return err
				}
				return nil
			}
			if strings.HasSuffix(slashRel, ".ext") {
				assetPath := strings.TrimSuffix(p, ".ext")
				if _, err := os.Stat(assetPath); os.IsNotExist(err) {
					if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
						return err
					}
				}
			}
			return nil
		})
		if purgeErr != nil {
			fail(http.StatusInternalServerError, "", fmt.Sprintf("清理暂存目录残留失败: %v", purgeErr))
			return
		}
	}

	if err := validateCacheMigrationTree(stageDir); err != nil {
		fail(http.StatusConflict, "", fmt.Sprintf("迁移暂存目录复核失败，未切换配置: %v", err))
		return
	}

	if beforeMigrationApplyHook != nil {
		beforeMigrationApplyHook()
	}

	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	latestCfg := c.cfgMgr.Candidate()
	if filepath.Clean(latestCfg.CacheDir) != filepath.Clean(sourceDir) {
		fail(http.StatusConflict, ErrCodeMigrationSourceChanged, "操作期间源缓存目录已被其他操作修改")
		return
	}

	// Re-verify target directory immediately before replacement
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		fail(http.StatusBadRequest, "", fmt.Sprintf("迁移目标路径校验失败: %v", err))
		return
	}
	if errCode, err := validateTargetForMode(targetDir, reqMode); err != nil {
		fail(http.StatusConflict, errCode, err.Error(), "target_dir", targetDir)
		return
	}

	// Execute directory replacement with rollback backup
	var oldBackupDir string
	targetExists := false
	if _, statErr := os.Lstat(targetDir); statErr == nil {
		targetExists = true
	}

	if targetExists {
		oldBackupDir = filepath.Join(parentDir, fmt.Sprintf(".gbf-cache-old-%d", time.Now().UnixNano()))
		if err := renameFunc(targetDir, oldBackupDir); err != nil {
			fail(http.StatusInternalServerError, ErrCodeMigrationReplacementFailed, fmt.Sprintf("重命名现有目标目录至备份失败: %v", err))
			return
		}

		// Re-verify the frozen backup directory to eliminate the filesystem race window
		if errCode, err := validateTargetForMode(oldBackupDir, reqMode); err != nil {
			if rbErr := renameFunc(oldBackupDir, targetDir); rbErr != nil {
				msg := fmt.Sprintf("现有目标目录确权复核失败且原目录恢复失败: %v (恢复错误: %v, 原缓存备份保留于: %s)", err, rbErr, oldBackupDir)
				fail(http.StatusInternalServerError, ErrCodeMigrationRollbackFailed, msg, "backup_dir", oldBackupDir)
				return
			}
			fail(http.StatusConflict, errCode, err.Error(), "target_dir", targetDir)
			return
		}
	}

	if err := renameFunc(stageDir, targetDir); err != nil {
		if targetExists {
			if rbErr := renameFunc(oldBackupDir, targetDir); rbErr != nil {
				msg := fmt.Sprintf("重命名暂存目录失败且回滚备份失败: %v (回滚错误: %v, 备份位于: %s)", err, rbErr, oldBackupDir)
				fail(http.StatusInternalServerError, ErrCodeMigrationRollbackFailed, msg, "backup_dir", oldBackupDir)
				return
			}
		}
		fail(http.StatusInternalServerError, ErrCodeMigrationReplacementFailed, fmt.Sprintf("提交迁移目录失败: %v", err))
		return
	}

	// Commit candidate configuration with transactional commit
	candidate := latestCfg
	candidate.CacheDir = targetDir

	if err := c.cfgMgr.Commit(candidate); err != nil {
		cleanupErr := removeAllFunc(targetDir)
		extra := ""
		if cleanupErr != nil {
			extra = fmt.Sprintf(" (新目录清理失败: %v)", cleanupErr)
		}
		if targetExists {
			if rbErr := renameFunc(oldBackupDir, targetDir); rbErr != nil {
				msg := fmt.Sprintf("配置持久化失败且恢复旧目录失败: %v (恢复错误: %v, 备份位于: %s)%s", err, rbErr, oldBackupDir, extra)
				fail(http.StatusInternalServerError, ErrCodeMigrationRollbackFailed, msg, "backup_dir", oldBackupDir)
				return
			}
		}
		fail(http.StatusInternalServerError, ErrCodeMigrationConfigCommitFailed, fmt.Sprintf("配置持久化失败: %v%s", err, extra))
		return
	}

	stageCommitted = true
	c.cacheMgr.SetCacheBase(targetDir)

	var warningMsg string
	if targetExists && oldBackupDir != "" {
		if rmErr := removeAllFunc(oldBackupDir); rmErr != nil {
			warningMsg = fmt.Sprintf("旧目标目录清理失败，请手动清理: %s (%v)", oldBackupDir, rmErr)
			if c.stats != nil {
				c.stats.Log("WARN", fmt.Sprintf("[CACHE] %s", warningMsg))
			}
		}
	}

	c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": targetDir}, "")
	if c.stats != nil {
		c.stats.Log("INFO", fmt.Sprintf("[CACHE] 缓存目录已平滑迁移至 macOS 标准应用数据目录: %s", targetDir))
	}

	resp := map[string]interface{}{
		"ok":      true,
		"mode":    reqMode,
		"new_dir": targetDir,
		"message": "缓存已成功迁移至 macOS 标准目录",
	}
	if warningMsg != "" {
		resp["warning"] = warningMsg
		resp["backup_dir"] = oldBackupDir
	}
	c.sendJSON(w, http.StatusOK, resp)
}
