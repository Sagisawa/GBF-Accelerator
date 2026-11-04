//go:build darwin
// +build darwin

package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
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
	sampledLegacyCount := 0
	const maxLegacyMagicSamples = 50

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

		// Legacy file without .ext: check directory shape, asset extension, and sampled magic bytes
		if !hasMarker {
			return fmt.Errorf("未标记缓存目录包含无法确认归属的文件: %s", rel)
		}
		// All files must check directory structure and asset extension
		if _, ok := cache.IsTier2Candidate(slashRel, true); !ok {
			return fmt.Errorf("迁移目录包含无法确认归属的文件 (目录结构或素材扩展名无效): %s", rel)
		}
		if info.Size() <= 0 {
			return fmt.Errorf("迁移目录包含无法确认归属的空文件: %s", rel)
		}
		if sampledLegacyCount < maxLegacyMagicSamples {
			if !cache.IsConfirmedCacheFile(root, path, true) {
				return fmt.Errorf("迁移目录包含无法确认归属的文件: %s", rel)
			}
			sampledLegacyCount++
		}
		return nil
	})
}

// validateCacheOverwriteTarget performs 100% full attribution check on every single file in the target directory
// without any sampling cutoffs. It strictly rejects unknown files, orphan .ext metadata, system junk, and temp files.
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
	hasMarker := cache.HasRootMarker(root)

	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("目标目录包含符号链接或特殊文件: %s", path)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		slashRel := filepath.ToSlash(rel)

		if entry.IsDir() {
			if !cache.IsConfirmedCacheDirectory(slashRel) {
				return fmt.Errorf("目标目录包含未通过确权的目录: %s", rel)
			}
			return nil
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("目标目录包含非普通文件: %s", path)
		}

		// Root marker check
		if slashRel == cache.RootMarkerFileName {
			if !hasMarker {
				return fmt.Errorf("目标目录根标记无效: %s", path)
			}
			return nil
		}

		// Strictly reject system junk, temp streaming files, and quarantine files in overwrite target
		if config.IsSystemJunk(entry.Name()) || cache.IsAcceleratorTempFile(slashRel) || cache.IsAcceleratorQuarantineFile(slashRel) {
			return fmt.Errorf("目标目录包含未纳入严格确权的杂项或残留文件: %s", rel)
		}

		// Metadata file (.ext)
		if strings.HasSuffix(slashRel, ".ext") {
			assetPath := strings.TrimSuffix(path, ".ext")
			assetFi, aErr := os.Lstat(assetPath)
			if aErr != nil || !assetFi.Mode().IsRegular() {
				return fmt.Errorf("目标目录包含未配对的孤儿元数据: %s", rel)
			}
			metaBytes, rErr := os.ReadFile(path)
			if rErr != nil {
				return fmt.Errorf("目标目录包含无法读取的元数据: %s", rel)
			}
			assetSlashRel := strings.TrimSuffix(slashRel, ".ext")
			if _, ok := cache.VerifyTier1Meta(assetSlashRel, metaBytes); !ok {
				return fmt.Errorf("目标目录包含无法配对验证的元数据: %s", rel)
			}
			return nil
		}

		// Asset file (without .ext)
		if info.Size() <= 0 {
			return fmt.Errorf("目标目录包含空文件: %s", rel)
		}

		extPath := path + ".ext"
		if extFi, extErr := os.Lstat(extPath); extErr == nil && extFi.Mode().IsRegular() {
			metaBytes, rErr := os.ReadFile(extPath)
			if rErr != nil {
				return fmt.Errorf("目标目录包含无法读取的元数据: %s.ext", rel)
			}
			if _, ok := cache.VerifyTier1Meta(slashRel, metaBytes); !ok {
				return fmt.Errorf("目标目录包含无法确认归属的文件: %s", rel)
			}
			// Validate content format / magic bytes consistency
			if !cache.VerifyCacheFileMagic(path, strings.ToLower(slashRel)) {
				return fmt.Errorf("目标目录素材内容与格式校验不一致: %s", rel)
			}
			return nil
		}

		// Legacy file without .ext:
		// Check directory structure, asset extension, and verify magic bytes for EVERY single file (no 50-file limit)
		cleanLower, ok := cache.IsTier2Candidate(slashRel, true)
		if !ok {
			return fmt.Errorf("目标目录包含无法确认归属的文件 (目录结构或素材扩展名无效): %s", rel)
		}
		if !cache.VerifyCacheFileMagic(path, cleanLower) {
			return fmt.Errorf("目标目录素材魔数或内容校验失败: %s", rel)
		}
		return nil
	})
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

func (c *ControlServer) handleMigrateStandardCache(w http.ResponseWriter, req *http.Request) {
	taskID, _, ok := c.tryBeginCacheTask("migrate_standard")
	if !ok {
		c.sendTaskConflict(w)
		return
	}

	targetDir := standardCacheDirProvider()
	if targetDir == "" {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, "Unable to determine user home directory")
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "无法获取当前用户的 Home 目录",
		})
		return
	}

	// Request mode parsing
	reqMode := "migrate"
	if req.Body != nil {
		limitedReader := io.LimitReader(req.Body, maxMigrateBodySize)
		bodyBytes, err := io.ReadAll(limitedReader)
		if err != nil {
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, "读取请求体失败")
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok": false, "code": ErrCodeMigrationInvalidMode, "error": "读取请求体失败",
			})
			return
		}
		trimmed := bytes.TrimSpace(bodyBytes)
		if len(trimmed) > 0 {
			var mReq migrateStandardRequest
			dec := json.NewDecoder(bytes.NewReader(trimmed))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&mReq); err != nil {
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, "请求体 JSON 格式无效")
				c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
					"ok": false, "code": ErrCodeMigrationInvalidMode, "error": fmt.Sprintf("请求体 JSON 格式无效: %v", err),
				})
				return
			}
			switch mReq.Mode {
			case "", "migrate":
				reqMode = "migrate"
			case "switch_only":
				reqMode = "switch_only"
			case "overwrite":
				reqMode = "overwrite"
			default:
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, "不支持的操作模式")
				c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
					"ok": false, "code": ErrCodeMigrationInvalidMode, "error": fmt.Sprintf("不支持的操作模式: %q", mReq.Mode),
				})
				return
			}
		}
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
		msg := "源目录与目标目录相同或存在路径嵌套，拒绝覆盖操作"
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "code": ErrCodeMigrationInvalidMode, "error": msg,
		})
		return
	}

	// Mode: switch_only
	if reqMode == "switch_only" {
		if err := config.ValidateCacheDir(targetDir); err != nil {
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok": false, "error": fmt.Sprintf("标准缓存目标路径校验失败: %v", err),
			})
			return
		}
		if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok": false, "error": fmt.Sprintf("迁移目标路径校验失败: %v", err),
			})
			return
		}

		targetCreatedByUs := false
		if tFi, tErr := os.Lstat(targetDir); tErr != nil {
			if os.IsNotExist(tErr) {
				if err := os.MkdirAll(targetDir, 0755); err != nil {
					c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
					c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
						"ok": false, "error": fmt.Sprintf("创建标准缓存目录失败: %v", err),
					})
					return
				}
				targetCreatedByUs = true
			} else {
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, tErr.Error())
				c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok": false, "error": tErr.Error(),
				})
				return
			}
		} else {
			if tFi.Mode()&os.ModeSymlink != 0 || !tFi.IsDir() {
				msg := "迁移目标不是普通目录"
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
				c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
					"ok": false, "error": msg,
				})
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
			msg := "操作期间源缓存目录已被其他操作修改"
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok": false, "code": ErrCodeMigrationSourceChanged, "error": msg,
			})
			return
		}

		candidate := latestCfg
		candidate.CacheDir = targetDir

		if err := c.cfgMgr.Commit(candidate); err != nil {
			if targetCreatedByUs {
				_ = os.Remove(targetDir) // Safe cleanup of empty directory created by us
			}
			msg := fmt.Sprintf("配置提交失败: %v", err)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"ok": false, "code": ErrCodeMigrationConfigCommitFailed, "error": msg,
			})
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
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("迁移源路径校验失败: %v", err),
		})
		return
	}
	if err := config.ValidateCacheDir(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("标准缓存目标路径校验失败: %v", err),
		})
		return
	}
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("迁移目标路径校验失败: %v", err),
		})
		return
	}

	if sourceInfo, statErr := os.Lstat(sourceDir); statErr == nil {
		if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
			msg := "迁移源必须是普通目录，拒绝处理符号链接或文件"
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok": false, "error": msg,
			})
			return
		}
		if err := validateCacheMigrationTree(sourceDir); err != nil {
			msg := fmt.Sprintf("迁移已停止，源目录包含无法确认归属的数据，原目录保持不变: %v", err)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok": false, "code": ErrCodeMigrationSourceUnconfirmed, "error": msg,
			})
			return
		}
	} else if !os.IsNotExist(statErr) {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, statErr.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": statErr.Error(),
		})
		return
	}

	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("创建标准缓存父目录失败: %v", err),
		})
		return
	}
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("迁移目标路径校验失败: %v", err),
		})
		return
	}

	// Pre-check target before staging
	if reqMode == "overwrite" {
		if err := validateCacheOverwriteTarget(targetDir); err != nil {
			msg := fmt.Sprintf("目标目录包含无法确认为 GBF 缓存的文件，拒绝覆盖现有内容: %v", err)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok":         false,
				"code":       ErrCodeMigrationTargetUnconfirmed,
				"target_dir": targetDir,
				"error":      msg,
			})
			return
		}
	} else {
		if err := validateEmptyMigrationTarget(targetDir); err != nil {
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok":         false,
				"code":       ErrCodeMigrationTargetNotEmpty,
				"target_dir": targetDir,
				"error":      err.Error(),
			})
			return
		}
	}

	// Stage source cache in the target's parent directory
	stageDir, err := os.MkdirTemp(parentDir, ".gbf-cache-migration-*")
	if err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("创建迁移暂存目录失败: %v", err),
		})
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
				msg := fmt.Sprintf("复制缓存至暂存目录失败: %v: %s (clone: %s)", fbErr, string(fbOut), string(out))
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
				c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok": false, "error": msg,
				})
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
			msg := fmt.Sprintf("清理暂存目录残留失败: %v", purgeErr)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"ok": false, "error": msg,
			})
			return
		}
	}

	if err := validateCacheMigrationTree(stageDir); err != nil {
		msg := fmt.Sprintf("迁移暂存目录复核失败，未切换配置: %v", err)
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok": false, "error": msg,
		})
		return
	}

	if beforeMigrationApplyHook != nil {
		beforeMigrationApplyHook()
	}

	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	latestCfg := c.cfgMgr.Candidate()
	if filepath.Clean(latestCfg.CacheDir) != filepath.Clean(sourceDir) {
		msg := "操作期间源缓存目录已被其他操作修改"
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok": false, "code": ErrCodeMigrationSourceChanged, "error": msg,
		})
		return
	}

	// Re-verify target directory immediately before replacement
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": fmt.Sprintf("迁移目标路径校验失败: %v", err),
		})
		return
	}
	if reqMode == "overwrite" {
		if err := validateCacheOverwriteTarget(targetDir); err != nil {
			msg := fmt.Sprintf("目标目录复核失败，拒绝覆盖现有内容: %v", err)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok":         false,
				"code":       ErrCodeMigrationTargetUnconfirmed,
				"target_dir": targetDir,
				"error":      msg,
			})
			return
		}
	} else {
		if err := validateEmptyMigrationTarget(targetDir); err != nil {
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok":         false,
				"code":       ErrCodeMigrationTargetNotEmpty,
				"target_dir": targetDir,
				"error":      err.Error(),
			})
			return
		}
	}

	// Execute directory replacement with rollback backup
	var oldBackupDir string
	targetExists := false
	if _, statErr := os.Lstat(targetDir); statErr == nil {
		targetExists = true
	}

	if targetExists {
		oldBackupDir = filepath.Join(parentDir, fmt.Sprintf(".gbf-cache-old-%d", time.Now().UnixNano()))
		if err := os.Rename(targetDir, oldBackupDir); err != nil {
			msg := fmt.Sprintf("重命名现有目标目录至备份失败: %v", err)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"ok": false, "code": ErrCodeMigrationReplacementFailed, "error": msg,
			})
			return
		}
	}

	if err := os.Rename(stageDir, targetDir); err != nil {
		if targetExists {
			if rbErr := os.Rename(oldBackupDir, targetDir); rbErr != nil {
				msg := fmt.Sprintf("重命名暂存目录失败且回滚备份失败: %v (回滚错误: %v, 备份位于: %s)", err, rbErr, oldBackupDir)
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
				c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok": false, "code": ErrCodeMigrationRollbackFailed, "backup_dir": oldBackupDir, "error": msg,
				})
				return
			}
		}
		msg := fmt.Sprintf("提交迁移目录失败: %v", err)
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "code": ErrCodeMigrationReplacementFailed, "error": msg,
		})
		return
	}

	// Commit candidate configuration with transactional commit
	candidate := latestCfg
	candidate.CacheDir = targetDir

	if err := c.cfgMgr.Commit(candidate); err != nil {
		_ = os.RemoveAll(targetDir)
		if targetExists {
			if rbErr := os.Rename(oldBackupDir, targetDir); rbErr != nil {
				msg := fmt.Sprintf("配置持久化失败且恢复旧目录失败: %v (恢复错误: %v, 备份位于: %s)", err, rbErr, oldBackupDir)
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
				c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"ok": false, "code": ErrCodeMigrationRollbackFailed, "backup_dir": oldBackupDir, "error": msg,
				})
				return
			}
		}
		msg := fmt.Sprintf("配置持久化失败: %v", err)
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "code": ErrCodeMigrationConfigCommitFailed, "error": msg,
		})
		return
	}

	stageCommitted = true
	c.cacheMgr.SetCacheBase(targetDir)

	var warningMsg string
	if targetExists && oldBackupDir != "" {
		if rmErr := os.RemoveAll(oldBackupDir); rmErr != nil {
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
