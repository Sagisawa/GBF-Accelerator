//go:build darwin
// +build darwin

package control

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
)

func getStandardCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "GBF-Accelerator", "cache")
}

var standardCacheDirProvider = getStandardCacheDir

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
		if !cache.IsConfirmedCacheDirectory(filepath.ToSlash(filepath.Dir(slashRel))) {
			return fmt.Errorf("迁移目录包含无法确认目录归属的文件: %s", rel)
		}
		if sampledLegacyCount < maxLegacyMagicSamples {
			if !cache.IsConfirmedCacheFile(root, path, true) {
				return fmt.Errorf("迁移目录包含无法确认归属的文件: %s", rel)
			}
			sampledLegacyCount++
		} else {
			if info.Size() <= 0 {
				return fmt.Errorf("迁移目录包含无法确认归属的空文件: %s", rel)
			}
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
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "无法获取当前用户的 Home 目录",
		})
		return
	}

	curCfg := c.cfgMgr.Get()
	sourceDir := curCfg.CacheDir

	if isStandardCacheDir(sourceDir) {
		c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": sourceDir}, "")
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"new_dir": sourceDir,
			"message": "当前缓存目录已在 macOS 标准目录下",
		})
		return
	}

	if err := ensureNoSymlinkPathComponents(sourceDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("迁移源路径校验失败: %v", err)})
		return
	}
	if err := config.ValidateCacheDir(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("标准缓存目标路径校验失败: %v", err)})
		return
	}
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("迁移目标路径校验失败: %v", err)})
		return
	}

	if sourceInfo, statErr := os.Lstat(sourceDir); statErr == nil {
		if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.IsDir() {
			msg := "迁移源必须是普通目录，拒绝处理符号链接或文件"
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		if err := validateCacheMigrationTree(sourceDir); err != nil {
			msg := fmt.Sprintf("迁移已停止，源目录包含无法确认归属的数据，原目录保持不变: %v", err)
			c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok": false, "code": "CACHE_MIGRATION_SOURCE_UNCONFIRMED", "error": msg,
			})
			return
		}
	} else if !os.IsNotExist(statErr) {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, statErr.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": statErr.Error()})
		return
	}

	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("创建标准缓存父目录失败: %v", err)})
		return
	}
	if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("迁移目标路径校验失败: %v", err)})
		return
	}
	if err := validateEmptyMigrationTarget(targetDir); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	stageDir, err := os.MkdirTemp(parentDir, ".gbf-cache-migration-*")
	if err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("创建迁移暂存目录失败: %v", err)})
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
				c.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
				return
			}
		}
		// Purge residual streaming temp files, quarantine files, and orphan .ext from stageDir
		_ = filepath.WalkDir(stageDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(stageDir, p)
			if err != nil {
				return nil
			}
			slashRel := filepath.ToSlash(rel)
			if cache.IsAcceleratorTempFile(slashRel) || cache.IsAcceleratorQuarantineFile(slashRel) {
				_ = os.Remove(p)
				return nil
			}
			if strings.HasSuffix(slashRel, ".ext") {
				assetPath := strings.TrimSuffix(p, ".ext")
				if _, err := os.Stat(assetPath); os.IsNotExist(err) {
					_ = os.Remove(p)
				}
			}
			return nil
		})
	}

	if err := validateCacheMigrationTree(stageDir); err != nil {
		msg := fmt.Sprintf("迁移暂存目录复核失败，未切换配置: %v", err)
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusConflict, map[string]string{"error": msg})
		return
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		msg := fmt.Sprintf("提交迁移目录失败，目标目录未被覆盖: %v", err)
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		c.sendJSON(w, http.StatusConflict, map[string]string{"error": msg})
		return
	}
	stageCommitted = true

	// Update candidate configuration with transactional commit
	candidate := curCfg
	candidate.CacheDir = targetDir

	if err := c.cfgMgr.Commit(candidate); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("配置持久化失败: %v", err),
		})
		return
	}

	// Hot reload runtime cache base
	c.cacheMgr.SetCacheBase(targetDir)

	c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": targetDir}, "")

	if c.stats != nil {
		c.stats.Log("INFO", fmt.Sprintf("[CACHE] 缓存目录已平滑迁移至 macOS 标准应用数据目录: %s", targetDir))
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"new_dir": targetDir,
		"message": "缓存已成功迁移至 macOS 标准目录",
	})
}
