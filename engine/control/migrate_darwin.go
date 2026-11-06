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
	renameFunc               = os.Rename
	removeAllFunc            = os.RemoveAll
	removeFunc               = os.Remove
	standardCacheDirProvider = getStandardCacheDir
	beforeMigrationApplyHook func()
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
	if home, _ := os.UserHomeDir(); home != "" {
		return filepath.Join(home, "Library", "Application Support", "GBF-Accelerator", "cache")
	}
	return ""
}

func isStandardCacheDir(cacheDir string) bool {
	home, _ := os.UserHomeDir()
	if home == "" || cacheDir == "" {
		return false
	}
	clean := filepath.Clean(cacheDir)
	for _, b := range []string{filepath.Join(home, "Library", "Application Support", "GBF-Accelerator"), filepath.Join(home, "Library", "Caches", "GBF-Accelerator")} {
		if clean == b || strings.HasPrefix(clean, b+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isSameOrNestedPath(a, b string) bool {
	cA, cB := filepath.Clean(a), filepath.Clean(b)
	rA, eA := filepath.Rel(cA, cB)
	rB, eB := filepath.Rel(cB, cA)
	return cA == cB || (eA == nil && !strings.HasPrefix(rA, "..")) || (eB == nil && !strings.HasPrefix(rB, ".."))
}

func ensureNoSymlinkPathComponents(p string) error {
	abs, err := filepath.Abs(p)
	if err != nil {
		return err
	}
	cur := filepath.VolumeName(abs) + string(filepath.Separator)
	for _, seg := range strings.Split(strings.TrimPrefix(abs, cur), string(filepath.Separator)) {
		if seg == "" || seg == "." {
			continue
		}
		cur = filepath.Join(cur, seg)
		if fi, err := os.Lstat(cur); err == nil && (fi.Mode()&os.ModeSymlink != 0 || (cur != abs && !fi.IsDir())) {
			return fmt.Errorf("路径校验失败 (符号链接或非目录祖先): %s", cur)
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func validateCacheMigrationTree(root string) error {
	fi, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("迁移源必须是普通目录: %s (err: %v)", root, err)
	}
	hasMarker := cache.HasRootMarker(root)

	return filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || p == root {
			return walkErr
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("迁移目录包含符号链接或特殊文件: %s", p)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		slashRel := filepath.ToSlash(rel)
		if d.IsDir() {
			if !cache.IsConfirmedCacheDirectory(slashRel) {
				return fmt.Errorf("迁移目录包含无法确认的目录: %s", rel)
			}
			return nil
		}
		if info, err := d.Info(); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("迁移目录包含非普通文件: %s (err: %v)", p, err)
		} else if slashRel == cache.RootMarkerFileName {
			if !hasMarker {
				return fmt.Errorf("缓存根标记无效: %s", p)
			}
			return nil
		} else if config.IsSystemJunk(filepath.Base(p)) || cache.IsAcceleratorTempFile(slashRel) || cache.IsAcceleratorQuarantineFile(slashRel) {
			return nil
		}
		extPath, assetSlash := p+".ext", slashRel
		if strings.HasSuffix(slashRel, ".ext") {
			extPath, assetSlash = p, strings.TrimSuffix(slashRel, ".ext")
			if fi, err := os.Lstat(strings.TrimSuffix(p, ".ext")); err != nil || !fi.Mode().IsRegular() {
				if !cache.IsConfirmedCacheDirectory(filepath.ToSlash(filepath.Dir(slashRel))) {
					return fmt.Errorf("迁移目录包含未知路径的孤儿元数据: %s", rel)
				}
				return nil
			}
		} else if fi, err := os.Lstat(extPath); err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("迁移目录包含未纳入元数据确权的文件 (缺少有效 .ext): %s", rel)
		}
		if metaBytes, err := os.ReadFile(extPath); err != nil {
			return fmt.Errorf("迁移目录包含无法读取的元数据: %s", rel)
		} else if _, ok := cache.VerifyTier1Meta(assetSlash, metaBytes); !ok {
			if strings.HasSuffix(slashRel, ".ext") {
				return fmt.Errorf("迁移目录包含无法配对验证的元数据: %s", rel)
			}
			return fmt.Errorf("迁移目录包含无法确认归属的文件: %s", rel)
		}
		return nil
	})
}

func hasConfirmedCacheAssets(dir string) (bool, error) {
	hasMarker, hasAssets := cache.HasRootMarker(dir), false
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || path == dir || entry.IsDir() {
			return walkErr
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 && cache.IsConfirmedCacheFileWithInfo(dir, path, info, hasMarker) {
			hasAssets = true
			return filepath.SkipAll
		}
		return nil
	})
	return hasAssets, err
}

func FindResidualMigrationBackups(parentDir string) ([]string, error) {
	entries, err := os.ReadDir(parentDir)
	if err != nil { return nil, err }
	var backups []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".gbf-cache-old-") {
			backups = append(backups, filepath.Join(parentDir, e.Name()))
		}
	}
	sort.Strings(backups)
	return backups, nil
}

func RecoverResidualMigrationBackup(backupDir, targetDir string) error {
	bFi, err := os.Lstat(backupDir)
	if err != nil || bFi.Mode()&os.ModeSymlink != 0 || !bFi.IsDir() || !strings.HasPrefix(filepath.Base(backupDir), ".gbf-cache-old-") {
		return fmt.Errorf("备份目录无效: %s (err: %v)", backupDir, err)
	}
	if err := validateEmptyMigrationTarget(targetDir); err != nil { return err }
	if _, err := os.Lstat(targetDir); err == nil && removeFunc(targetDir) != nil {
		return fmt.Errorf("移除空目标目录以便恢复失败: %w", err)
	}
	return renameFunc(backupDir, targetDir)
}

func readDirRegular(dir, label string) ([]os.DirEntry, error) {
	fi, err := os.Lstat(dir)
	if os.IsNotExist(err) { return nil, nil }
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return nil, fmt.Errorf("%s不是普通目录: %s (err: %v)", label, dir, err)
	}
	return os.ReadDir(dir)
}

func validateCacheOverwriteTarget(root string) error {
	return validateCacheMigrationTree(root)
}

func validateEmptyMigrationTarget(target string) error {
	entries, err := readDirRegular(target, "迁移目标")
	if err != nil || len(entries) == 0 { return err }
	return fmt.Errorf("迁移目标目录并非空目录，拒绝覆盖现有内容: %s", target)
}

func validateTargetForMode(dir, mode string) (string, error) {
	if mode == "overwrite" {
		if err := validateCacheOverwriteTarget(dir); err != nil {
			return ErrCodeMigrationTargetUnconfirmed, fmt.Errorf("目标目录包含无法确认为 GBF 缓存的文件，拒绝覆盖现有内容: %w", err)
		}
		return "", nil
	}
	if err := validateEmptyMigrationTarget(dir); err != nil { return ErrCodeMigrationTargetNotEmpty, err }
	return "", nil
}

func parseMigrateRequestMode(r io.Reader) (string, error) {
	if r == nil { return "migrate", nil }
	body, err := io.ReadAll(io.LimitReader(r, maxMigrateBodySize+1))
	if err != nil || len(body) > maxMigrateBodySize {
		return "", fmt.Errorf("读取请求体失败或大小超过限制 (%d 字节)", maxMigrateBodySize)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 { return "migrate", nil }
	var req migrateStandardRequest
	var extra json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if dec.Decode(&req) != nil || dec.Decode(&extra) != io.EOF {
		return "", fmt.Errorf("请求体 JSON 格式无效或包含多余字段: %v", err)
	}
	if req.Mode == "" || req.Mode == "migrate" || req.Mode == "switch_only" || req.Mode == "overwrite" {
		if req.Mode == "" { return "migrate", nil }
		return req.Mode, nil
	}
	return "", fmt.Errorf("不支持的操作模式: %q", req.Mode)
}

func copyDirectoryTree(src, dst string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	srcDot := filepath.Clean(src) + string(filepath.Separator) + "."
	if out, err := exec.CommandContext(ctx, "cp", "-cR", srcDot, dst).CombinedOutput(); err != nil {
		if fbOut, fbErr := exec.CommandContext(ctx, "cp", "-R", srcDot, dst).CombinedOutput(); fbErr != nil {
			return fmt.Errorf("复制缓存至暂存目录失败: %v: %s (clone: %s)", fbErr, fbOut, out)
		}
	}
	return nil
}

func stageMigrationDir(src, parentDir string) (string, func(), error) {
	if err := os.MkdirAll(parentDir, 0755); err != nil { return "", nil, fmt.Errorf("创建标准缓存父目录失败: %w", err) }
	stageDir, err := os.MkdirTemp(parentDir, ".gbf-cache-migration-*")
	if err != nil { return "", nil, fmt.Errorf("创建迁移暂存目录失败: %w", err) }
	cleanup := func() { _ = os.RemoveAll(stageDir) }

	if fi, err := os.Lstat(src); err == nil && fi.IsDir() {
		if err := copyDirectoryTree(src, stageDir); err != nil {
			cleanup()
			return "", nil, err
		}
		_ = filepath.WalkDir(stageDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() { return err }
			slashRel := filepath.ToSlash(strings.TrimPrefix(p, stageDir+string(filepath.Separator)))
			if _, e := os.Stat(strings.TrimSuffix(p, ".ext")); (strings.HasSuffix(slashRel, ".ext") && os.IsNotExist(e)) ||
				cache.IsAcceleratorTempFile(slashRel) || cache.IsAcceleratorQuarantineFile(slashRel) {
				_ = os.Remove(p)
			}
			return nil
		})
	}
	if err := validateCacheMigrationTree(stageDir); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("迁移暂存目录复核失败，未切换配置: %w", err)
	}
	return stageDir, cleanup, nil
}

func validateMigrationPreflight(src, tgt, mode string) (int, string, error) {
	if mode == "overwrite" && isSameOrNestedPath(src, tgt) {
		return http.StatusBadRequest, ErrCodeMigrationInvalidMode, errors.New("源目录与目标目录相同或存在路径嵌套，拒绝覆盖操作")
	}
	if err := ensureNoSymlinkPathComponents(src); err != nil {
		return http.StatusBadRequest, "", fmt.Errorf("迁移源路径校验失败: %w", err)
	}
	code := ""
	if mode == "overwrite" {
		code = ErrCodeMigrationSourceUnconfirmed
	}
	if fi, err := os.Lstat(src); err != nil {
		if mode == "overwrite" {
			return http.StatusBadRequest, code, fmt.Errorf("源缓存目录不存在或无法访问: %w", err)
		} else if !os.IsNotExist(err) {
			return http.StatusInternalServerError, "", err
		}
	} else if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return http.StatusBadRequest, code, errors.New("迁移源必须是普通目录，拒绝处理符号链接或文件")
	} else if err := validateCacheMigrationTree(src); err != nil {
		return http.StatusConflict, code, fmt.Errorf("迁移已停止，源目录包含无法确认归属的数据，原目录保持不变: %v", err)
	} else if mode == "overwrite" {
		if hasAssets, err := hasConfirmedCacheAssets(src); err != nil {
			return http.StatusInternalServerError, code, fmt.Errorf("检查源缓存内容失败: %w", err)
		} else if !hasAssets {
			return http.StatusConflict, code, errors.New("源缓存目录为空或未包含任何有效缓存素材，拒绝覆盖已有目标缓存")
		}
	}
	if tgtCode, err := validateTargetForMode(tgt, mode); err != nil {
		return http.StatusConflict, tgtCode, err
	}
	return 0, "", nil
}

func (c *ControlServer) finishMigration(w http.ResponseWriter, taskID, mode, targetDir, oldBackupDir string) {
	c.cacheMgr.SetCacheBase(targetDir)
	msg := "缓存已成功迁移至 macOS 标准目录"
	if mode == "switch_only" { msg = "缓存目录已成功切换至 macOS 标准目录" }
	resp := map[string]interface{}{"ok": true, "mode": mode, "new_dir": targetDir, "message": msg}
	if oldBackupDir != "" {
		if rmErr := removeAllFunc(oldBackupDir); rmErr != nil {
			resp["warning"], resp["backup_dir"] = fmt.Sprintf("旧目标目录清理失败，请手动清理: %s (%v)", oldBackupDir, rmErr), oldBackupDir
			if c.stats != nil { c.stats.Log("WARN", fmt.Sprintf("[CACHE] %s", resp["warning"])) }
		}
	}
	c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": targetDir}, "")
	if c.stats != nil { c.stats.Log("INFO", fmt.Sprintf("[CACHE] %s: %s", msg, targetDir)) }
	c.sendJSON(w, http.StatusOK, resp)
}

func (c *ControlServer) commitTargetDir(targetDir string) error {
	cand := c.cfgMgr.Candidate()
	cand.CacheDir = targetDir
	return c.cfgMgr.Commit(cand)
}

func (c *ControlServer) handleMigrateStandardCache(w http.ResponseWriter, req *http.Request) {
	taskID, _, ok := c.tryBeginCacheTask("migrate_standard")
	if !ok {
		c.sendTaskConflict(w)
		return
	}
	targetDir := standardCacheDirProvider()
	parentDir := filepath.Dir(targetDir)
	fail := func(status int, code, msg string, kv ...string) {
		c.finishCacheTask(taskID, "migrate_standard", "failed", false, nil, msg)
		resp := map[string]interface{}{"ok": false, "error": msg}
		if code != "" {
			resp["code"] = code
			if code == ErrCodeMigrationTargetUnconfirmed || code == ErrCodeMigrationTargetNotEmpty {
				resp["target_dir"] = targetDir
			}
		}
		for i := 0; i+1 < len(kv); i += 2 {
			resp[kv[i]] = kv[i+1]
		}
		c.sendJSON(w, status, resp)
	}

	if targetDir == "" {
		fail(http.StatusInternalServerError, "", "无法获取当前用户的 Home 目录")
		return
	}
	reqMode, err := parseMigrateRequestMode(req.Body)
	if err != nil {
		fail(http.StatusBadRequest, ErrCodeMigrationInvalidMode, err.Error())
		return
	}
	sourceDir := c.cfgMgr.Get().CacheDir
	if filepath.Clean(sourceDir) == filepath.Clean(targetDir) {
		c.finishCacheTask(taskID, "migrate_standard", "completed", false, map[string]string{"new_dir": sourceDir}, "")
		c.sendJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "new_dir": sourceDir, "message": "当前缓存目录已是 macOS 标准目录"})
		return
	}

	checkTarget := func() bool {
		if err := config.ValidateCacheDir(targetDir); err != nil {
			fail(http.StatusBadRequest, "", fmt.Sprintf("标准缓存目标路径校验失败: %v", err))
			return false
		} else if err := ensureNoSymlinkPathComponents(targetDir); err != nil {
			fail(http.StatusBadRequest, "", fmt.Sprintf("迁移目标路径校验失败: %v", err))
			return false
		}
		return true
	}
	if !checkTarget() {
		return
	}

	var stageDir string
	targetCreated := false
	if reqMode == "switch_only" {
		if fi, err := os.Lstat(targetDir); os.IsNotExist(err) {
			if err := os.MkdirAll(targetDir, 0755); err != nil {
				fail(http.StatusInternalServerError, "", fmt.Sprintf("创建标准缓存目录失败: %v", err))
				return
			}
			targetCreated = true
		} else if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			fail(http.StatusBadRequest, "", "迁移目标不是普通目录")
			return
		}
	} else {
		if status, code, err := validateMigrationPreflight(sourceDir, targetDir, reqMode); err != nil {
			fail(status, code, err.Error())
			return
		}
		sDir, cleanupStage, err := stageMigrationDir(sourceDir, filepath.Dir(targetDir))
		if err != nil {
			fail(http.StatusInternalServerError, "", err.Error())
			return
		}
		defer cleanupStage()
		stageDir = sDir
	}

	if beforeMigrationApplyHook != nil {
		beforeMigrationApplyHook()
	}
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	if filepath.Clean(c.cfgMgr.Candidate().CacheDir) != filepath.Clean(sourceDir) {
		if targetCreated {
			_ = removeFunc(targetDir)
		}
		fail(http.StatusConflict, ErrCodeMigrationSourceChanged, "操作期间源缓存目录已被其他操作修改")
		return
	}
	if reqMode == "switch_only" {
		if err := c.commitTargetDir(targetDir); err != nil {
			if targetCreated {
				_ = removeFunc(targetDir)
			}
			fail(http.StatusInternalServerError, ErrCodeMigrationConfigCommitFailed, fmt.Sprintf("配置提交失败: %v", err))
			return
		}
		c.finishMigration(w, taskID, "switch_only", targetDir, "")
		return
	}
	if !checkTarget() {
		return
	} else if errCode, err := validateTargetForMode(targetDir, reqMode); err != nil {
		fail(http.StatusConflict, errCode, err.Error())
		return
	}

	var oldBackupDir string
	rollback := func(err error, status int, code, msg, rbMsg string) {
		if oldBackupDir != "" {
			if rbErr := renameFunc(oldBackupDir, targetDir); rbErr != nil {
				fail(http.StatusInternalServerError, ErrCodeMigrationRollbackFailed, fmt.Sprintf("%s: %v (恢复错误: %v, 原缓存备份保留于: %s)", rbMsg, err, rbErr, oldBackupDir), "backup_dir", oldBackupDir)
				return
			}
		}
		fail(status, code, msg)
	}

	if _, err := os.Lstat(targetDir); err == nil {
		oldBackupDir = filepath.Join(parentDir, fmt.Sprintf(".gbf-cache-old-%d", time.Now().UnixNano()))
		if err := renameFunc(targetDir, oldBackupDir); err != nil {
			fail(http.StatusInternalServerError, ErrCodeMigrationReplacementFailed, fmt.Sprintf("重命名现有目标目录至备份失败: %v", err))
			return
		} else if errCode, err := validateTargetForMode(oldBackupDir, reqMode); err != nil {
			rollback(err, http.StatusConflict, errCode, err.Error(), "现有目标目录确权复核失败且原目录恢复失败")
			return
		}
	}

	if err := renameFunc(stageDir, targetDir); err != nil {
		rollback(err, http.StatusInternalServerError, ErrCodeMigrationReplacementFailed, fmt.Sprintf("提交迁移目录失败: %v", err), "重命名暂存目录失败且回滚备份失败")
		return
	} else if err := c.commitTargetDir(targetDir); err != nil {
		_ = removeAllFunc(targetDir)
		rollback(err, http.StatusInternalServerError, ErrCodeMigrationConfigCommitFailed, fmt.Sprintf("配置持久化失败: %v", err), "配置持久化失败且恢复旧目录失败")
		return
	}

	c.finishMigration(w, taskID, reqMode, targetDir, oldBackupDir)
}
