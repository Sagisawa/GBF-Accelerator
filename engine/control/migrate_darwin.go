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
)

func getStandardCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "GBF-Accelerator", "cache")
}

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

func (c *ControlServer) handleMigrateStandardCache(w http.ResponseWriter, req *http.Request) {
	taskID, _, ok := c.tryBeginCacheTask("migrate_standard")
	if !ok {
		c.sendTaskConflict(w)
		return
	}

	targetDir := getStandardCacheDir()
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

	// Ensure target directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, err.Error())
		c.sendJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("创建标准缓存目录失败: %v", err),
		})
		return
	}

	// If source directory exists and is a directory, clone/copy files to target directory
	if fi, err := os.Stat(sourceDir); err == nil && fi.IsDir() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		// Attempt APFS instant copy-on-write clone first (cp -cR <source>/. <target>)
		cpCmd := exec.CommandContext(ctx, "cp", "-cR", sourceDir+"/.", targetDir)
		if out, err := cpCmd.CombinedOutput(); err != nil {
			// If clone fails (e.g. cross-volume), fall back to regular copy (cp -R)
			fallbackCmd := exec.CommandContext(ctx, "cp", "-R", sourceDir+"/.", targetDir)
			if fbOut, fbErr := fallbackCmd.CombinedOutput(); fbErr != nil {
				c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, fmt.Sprintf("%v: %s", fbErr, string(fbOut)))
				c.sendJSON(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("复制缓存文件至标准目录失败: %v (cp: %s)", fbErr, string(out)),
				})
				return
			}
		}
	}

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
