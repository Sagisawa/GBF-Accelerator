package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/proxy"
)

const (
	maxBreakdownControlBodyBytes = 128 * 1024
)

func (c *ControlServer) hasActiveCacheTaskLocked() bool {
	return c.activeTaskID != "" ||
		c.isAuditing ||
		c.isSlimming ||
		c.isBoosting ||
		c.isScanning ||
		c.isInvalidating ||
		c.isClearingCategory
}

func (c *ControlServer) setTaskFlagLocked(taskType string, val bool) {
	switch taskType {
	case "audit":
		c.isAuditing = val
	case "slim":
		c.isSlimming = val
	case "boost":
		c.isBoosting = val
	case "scan":
		c.isScanning = val
	case "invalidate":
		c.isInvalidating = val
	case "clear_category":
		c.isClearingCategory = val
	}
}

func (c *ControlServer) tryBeginCacheTask(taskType string) (string, chan struct{}, bool) {
	c.cacheTaskMu.Lock()
	defer c.cacheTaskMu.Unlock()

	if c.hasActiveCacheTaskLocked() {
		return "", nil, false
	}

	c.taskSeq++
	nowMS := time.Now().UnixMilli()
	taskID := fmt.Sprintf("%s-%d-%d", taskType, nowMS, c.taskSeq)
	cancelCh := make(chan struct{})

	c.activeTaskID = taskID
	c.activeTaskType = taskType
	c.activeTaskStartedMS = nowMS
	c.cacheCancelCh = cancelCh
	c.setTaskFlagLocked(taskType, true)

	return taskID, cancelCh, true
}

func (c *ControlServer) finishCacheTask(taskID, taskType, status string, cancelled bool, result interface{}, errMsg string) {
	c.cacheTaskMu.Lock()
	defer c.cacheTaskMu.Unlock()

	if c.activeTaskID != taskID {
		return
	}

	nowMS := time.Now().UnixMilli()
	c.setTaskFlagLocked(taskType, false)
	c.activeTaskID = ""
	c.activeTaskType = ""
	c.lastTaskSummary = map[string]interface{}{
		"task_id":         taskID,
		"task_type":       taskType,
		"status":          status,
		"cancelled":       cancelled,
		"started_at_ms":   c.activeTaskStartedMS,
		"completed_at_ms": nowMS,
		"result":          result,
		"error":           errMsg,
	}
}

func (c *ControlServer) sendTaskConflict(w http.ResponseWriter) {
	c.cacheTaskMu.Lock()
	activeType := c.activeTaskType
	activeID := c.activeTaskID
	c.cacheTaskMu.Unlock()

	c.sendJSON(w, http.StatusConflict, map[string]interface{}{
		"ok":             false,
		"error":          "当前有缓存维护任务正在进行中，请稍候完成后再试",
		"active_task":    activeType,
		"active_task_id": activeID,
	})
}

func decodeStrictJSONBody(req *http.Request, maxBytes int64, dst interface{}) error {
	if req.Body == nil {
		return errors.New("请求正文不能为空")
	}
	limited := io.LimitReader(req.Body, maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("读取请求正文失败: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return fmt.Errorf("请求正文超出大小上限 (%d 字节)", maxBytes)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return errors.New("请求正文不能为空")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("JSON 格式校验失败: %w", err)
	}
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("JSON 包含多余数据段")
	}
	return nil
}

// handleCacheBreakdown handles GET /api/cache/breakdown.
// It returns the latest completed scan snapshot without re-walking disk on poll/open.
func (c *ControlServer) handleCacheBreakdown(w http.ResponseWriter, req *http.Request) {
	customKeep := 0
	if keepStr := strings.TrimSpace(req.URL.Query().Get("keep")); keepStr != "" {
		k, err := strconv.Atoi(keepStr)
		if err != nil || k <= 0 || k > 1000 {
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": "保留版本数量参数 keep 必须为 1~1000 之间的正整数",
			})
			return
		}
		customKeep = k
	}

	latest, active := c.cacheMgr.GetBreakdownSnapshot(customKeep)

	c.cacheTaskMu.Lock()
	isScanning := c.isScanning
	scanProg := c.scanProgress
	activeTaskID := c.activeTaskID
	activeTaskType := c.activeTaskType
	c.cacheTaskMu.Unlock()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":               true,
		"cache_base":       c.cacheMgr.GetCacheBase(),
		"has_snapshot":     latest != nil,
		"snapshot":         latest,
		"is_scanning":      isScanning,
		"scan_progress":    scanProg,
		"active_scan":      active,
		"active_task_id":   activeTaskID,
		"active_task_type": activeTaskType,
	})
}

// handleCacheBreakdownScan handles POST /api/cache/breakdown/scan.
// Starts an asynchronous single-worker breakdown scan and returns 202 Accepted with task_id.
func (c *ControlServer) handleCacheBreakdownScan(w http.ResponseWriter, req *http.Request) {
	taskID, cancelCh, ok := c.tryBeginCacheTask("scan")
	if !ok {
		c.sendTaskConflict(w)
		return
	}

	c.cacheTaskMu.Lock()
	c.scanProgress = cache.ScanProgress{
		Phase:  "starting",
		Status: "running",
		Done:   false,
	}
	c.cacheTaskMu.Unlock()

	if c.stats != nil {
		c.stats.Log("INFO", fmt.Sprintf("[CACHE-SCAN] Starting disk cache breakdown scan (task_id=%s)...", taskID))
	}

	go func() {
		var finalStatus = "failed"
		var finalCancelled = false
		var finalResult interface{}
		var finalErr string

		defer func() {
			c.finishCacheTask(taskID, "scan", finalStatus, finalCancelled, finalResult, finalErr)
		}()

		snap := c.cacheMgr.ScanBreakdownWithProgress(func(p cache.ScanProgress) {
			c.cacheTaskMu.Lock()
			if c.activeTaskID == taskID {
				c.scanProgress = p
			}
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("cache_scan_progress", map[string]interface{}{
				"task_id":  taskID,
				"progress": p,
			})
		}, cancelCh)

		if snap != nil {
			finalStatus = snap.Status
			finalCancelled = (snap.Status == "cancelled")
			finalErr = snap.ErrorMessage
			summary := map[string]interface{}{
				"snapshot_id":           snap.SnapshotID,
				"status":                snap.Status,
				"valid":                 snap.Valid,
				"asset_files":           snap.AssetFiles,
				"asset_bytes":           snap.AssetBytes,
				"metadata_files":        snap.MetadataFiles,
				"metadata_bytes":        snap.MetadataBytes,
				"orphan_metadata_files": snap.OrphanMetadataFiles,
				"orphan_metadata_bytes": snap.OrphanMetadataBytes,
				"excluded_files":        snap.ExcludedFiles,
				"excluded_bytes":        snap.ExcludedBytes,
				"duration_ms":           snap.DurationMS,
				"completed_at_ms":       snap.CompletedAtMS,
				"error":                 snap.ErrorMessage,
			}
			finalResult = summary
			c.cacheTaskMu.Lock()
			c.lastScanResult = summary
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("cache_scan_done", map[string]interface{}{
				"task_id": taskID,
				"result":  summary,
			})
			if c.stats != nil {
				c.stats.Log("INFO", fmt.Sprintf("[CACHE-SCAN] Scan %s (task_id=%s): %d asset files (%.2f MiB), %d .ext files",
					snap.Status, taskID, snap.AssetFiles, float64(snap.AssetBytes)/(1024*1024), snap.MetadataFiles))
			}
		}
	}()

	c.sendJSON(w, http.StatusAccepted, map[string]interface{}{
		"ok":      true,
		"task_id": taskID,
		"task":    "scan",
		"status":  "running",
		"message": "磁盘缓存分类扫描已在后台启动",
	})
}

// handleCacheSearch handles GET /api/cache/search.
func (c *ControlServer) handleCacheSearch(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	snapshotID := strings.TrimSpace(q.Get("snapshot_id"))
	queryStr := q.Get("q")
	if queryStr == "" {
		queryStr = q.Get("query")
	}

	limit := cache.DefaultSearchLimit
	if limitStr := strings.TrimSpace(q.Get("limit")); limitStr != "" {
		parsedLimit, err := strconv.Atoi(limitStr)
		if err != nil {
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": "搜索数量参数 limit 必须为整数",
			})
			return
		}
		limit = parsedLimit
	}

	res, err := c.cacheMgr.SearchSnapshot(snapshotID, queryStr, limit, proxy.NormalizeAssetNamespace)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, cache.ErrSnapshotNotFound) || errors.Is(err, cache.ErrSnapshotStale) {
			status = http.StatusConflict
		}
		c.sendJSON(w, status, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, res)
}

type invalidateRequestPayload struct {
	SnapshotID string                    `json:"snapshot_id"`
	Entries    []cache.DeleteTargetEntry `json:"entries"`
}

// handleCacheInvalidate handles POST /api/cache/invalidate.
// Validates snapshot identity and entry list, then runs controlled deletion in background (202 Accepted).
func (c *ControlServer) handleCacheInvalidate(w http.ResponseWriter, req *http.Request) {
	var payload invalidateRequestPayload
	if err := decodeStrictJSONBody(req, maxBreakdownControlBodyBytes, &payload); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	payload.SnapshotID = strings.TrimSpace(payload.SnapshotID)
	if payload.SnapshotID == "" {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": "必须提供有效的 snapshot_id",
		})
		return
	}
	if len(payload.Entries) == 0 {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrEmptyCleanupSelection.Error(),
		})
		return
	}
	if len(payload.Entries) > cache.MaxInvalidateBatchSize {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrBatchLimitExceeded.Error(),
		})
		return
	}

	// Validate each entry format and snapshot validity before starting task
	for _, entry := range payload.Entries {
		if strings.TrimSpace(entry.Key) == "" {
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": "删除条目列表包含空路径",
			})
			return
		}
	}

	latest, _ := c.cacheMgr.GetBreakdownSnapshot(0)
	if latest == nil {
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrSnapshotNotFound.Error(),
		})
		return
	}
	if !latest.Valid || latest.SnapshotID != payload.SnapshotID {
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrSnapshotStale.Error(),
		})
		return
	}

	candidates, initialFailures := cache.ExpandSnapshotTargets(latest, payload.Entries)
	if len(candidates) == 0 && len(initialFailures) > 0 {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":       false,
			"error":    initialFailures[0].Reason,
			"failures": initialFailures,
		})
		return
	}

	taskID, cancelCh, ok := c.tryBeginCacheTask("invalidate")
	if !ok {
		c.sendTaskConflict(w)
		return
	}

	c.cacheTaskMu.Lock()
	c.cleanupProgress = cache.CleanupProgress{
		TaskType:   "invalidate",
		SnapshotID: payload.SnapshotID,
		TotalFiles: len(candidates),
		Done:       false,
	}
	c.cacheTaskMu.Unlock()

	if c.stats != nil {
		c.stats.Log("INFO", fmt.Sprintf("[CACHE-INVALIDATE] Starting targeted cache deletion (task_id=%s, requested=%d)...",
			taskID, len(payload.Entries)))
	}

	go func() {
		var finalStatus = "failed"
		var finalCancelled = false
		var finalResult interface{}
		var finalErr string

		defer func() {
			c.finishCacheTask(taskID, "invalidate", finalStatus, finalCancelled, finalResult, finalErr)
		}()

		res, err := c.cacheMgr.DeleteSnapshotEntriesWithProgress(payload.SnapshotID, payload.Entries, func(p cache.CleanupProgress) {
			c.cacheTaskMu.Lock()
			if c.activeTaskID == taskID {
				c.cleanupProgress = p
			}
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("cache_cleanup_progress", map[string]interface{}{
				"task_id":  taskID,
				"progress": p,
			})
		}, cancelCh)

		if err != nil {
			finalStatus = "failed"
			finalErr = err.Error()
			if res == nil {
				res = &cache.DeleteResult{
					TaskType:    "invalidate",
					SnapshotID:  payload.SnapshotID,
					Status:      "failed",
					FailedCount: 1,
					Failures: []cache.DeleteFailure{{
						Reason: err.Error(),
					}},
					Done:    true,
					Message: err.Error(),
				}
			}
		}
		if res != nil {
			finalStatus = res.Status
			finalCancelled = res.Cancelled
			finalResult = res
			c.cacheTaskMu.Lock()
			c.lastCleanupResult = res
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("cache_cleanup_done", map[string]interface{}{
				"task_id": taskID,
				"result":  res,
			})
			if c.stats != nil {
				c.stats.Log("INFO", fmt.Sprintf("[CACHE-INVALIDATE] Completed (task_id=%s): deleted %d assets (%d meta), freed %.2f MiB, skipped %d, failed %d",
					taskID, res.DeletedAssetFiles, res.DeletedMetaFiles, res.FreedMB, res.SkippedChangedCount, res.FailedCount))
			}
		}
	}()

	c.sendJSON(w, http.StatusAccepted, map[string]interface{}{
		"ok":              true,
		"task_id":         taskID,
		"task":            "invalidate",
		"status":          "running",
		"candidate_files": len(candidates),
		"message":         "定点缓存删除任务已在后台启动",
	})
}

type clearCategoryRequestPayload struct {
	SnapshotID string   `json:"snapshot_id"`
	Categories []string `json:"categories"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// handleCacheClearCategory handles POST /api/cache/clear-category.
// Validates category whitelist (rejecting versioned) and snapshot identity, then runs in background (202 Accepted).
func (c *ControlServer) handleCacheClearCategory(w http.ResponseWriter, req *http.Request) {
	var payload clearCategoryRequestPayload
	if err := decodeStrictJSONBody(req, 64*1024, &payload); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	payload.SnapshotID = strings.TrimSpace(payload.SnapshotID)
	if payload.SnapshotID == "" {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": "必须提供有效的 snapshot_id",
		})
		return
	}
	if len(payload.Categories) == 0 {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrEmptyCleanupSelection.Error(),
		})
		return
	}

	for _, cat := range payload.Categories {
		trimmed := strings.TrimSpace(cat)
		if trimmed == cache.CategoryVersioned {
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": cache.ErrVersionedCatDisallowed.Error(),
			})
			return
		}
		if !cache.IsSelectableCleanupCategory(trimmed) {
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": fmt.Sprintf("%s: %s", cache.ErrInvalidCategory.Error(), trimmed),
			})
			return
		}
	}

	for _, ns := range payload.Namespaces {
		raw := strings.TrimSpace(ns)
		if raw != "" && (strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\:`) || raw == "hosts") {
			c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
				"ok":    false,
				"error": fmt.Sprintf("无效的缓存命名空间: %s", raw),
			})
			return
		}
	}

	latest, _ := c.cacheMgr.GetBreakdownSnapshot(0)
	if latest == nil {
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrSnapshotNotFound.Error(),
		})
		return
	}
	if !latest.Valid || latest.SnapshotID != payload.SnapshotID {
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": cache.ErrSnapshotStale.Error(),
		})
		return
	}

	taskID, cancelCh, ok := c.tryBeginCacheTask("clear_category")
	if !ok {
		c.sendTaskConflict(w)
		return
	}

	c.cacheTaskMu.Lock()
	c.cleanupProgress = cache.CleanupProgress{
		TaskType:   "clear_category",
		SnapshotID: payload.SnapshotID,
		Done:       false,
	}
	c.cacheTaskMu.Unlock()

	if c.stats != nil {
		c.stats.Log("INFO", fmt.Sprintf("[CACHE-CLEAR-CAT] Starting category cleanup (task_id=%s, categories=%v)...",
			taskID, payload.Categories))
	}

	go func() {
		var finalStatus = "failed"
		var finalCancelled = false
		var finalResult interface{}
		var finalErr string

		defer func() {
			c.finishCacheTask(taskID, "clear_category", finalStatus, finalCancelled, finalResult, finalErr)
		}()

		res, err := c.cacheMgr.ClearCategoriesWithProgress(
			payload.SnapshotID,
			payload.Categories,
			payload.Namespaces,
			func(p cache.CleanupProgress) {
				c.cacheTaskMu.Lock()
				if c.activeTaskID == taskID {
					c.cleanupProgress = p
				}
				c.cacheTaskMu.Unlock()
				c.broadcastEvent("cache_cleanup_progress", map[string]interface{}{
					"task_id":  taskID,
					"progress": p,
				})
			},
			cancelCh,
		)
		if err != nil {
			finalStatus = "failed"
			finalErr = err.Error()
			if res == nil {
				res = &cache.DeleteResult{
					TaskType:    "clear_category",
					SnapshotID:  payload.SnapshotID,
					Status:      "failed",
					FailedCount: 1,
					Failures: []cache.DeleteFailure{{
						Reason: err.Error(),
					}},
					Done:    true,
					Message: err.Error(),
				}
			}
		}
		if res != nil {
			finalStatus = res.Status
			finalCancelled = res.Cancelled
			finalResult = res
			c.cacheTaskMu.Lock()
			c.lastCleanupResult = res
			c.cacheTaskMu.Unlock()
			c.broadcastEvent("cache_cleanup_done", map[string]interface{}{
				"task_id": taskID,
				"result":  res,
			})
			if c.stats != nil {
				c.stats.Log("INFO", fmt.Sprintf("[CACHE-CLEAR-CAT] Completed (task_id=%s): deleted %d assets (%d meta), freed %.2f MiB, skipped %d, failed %d",
					taskID, res.DeletedAssetFiles, res.DeletedMetaFiles, res.FreedMB, res.SkippedChangedCount, res.FailedCount))
			}
		}
	}()

	c.sendJSON(w, http.StatusAccepted, map[string]interface{}{
		"ok":         true,
		"task_id":    taskID,
		"task":       "clear_category",
		"status":     "running",
		"categories": payload.Categories,
		"message":    "按分类清理缓存任务已在后台启动",
	})
}

type slimRequestPayload struct {
	Keep       *int   `json:"keep,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`
}

func parseSlimKeepAndSnapshot(req *http.Request) (keep int, snapshotID string, err error) {
	keep = cache.DefaultVersionKeep
	hasQueryKeep := false

	if kStr := strings.TrimSpace(req.URL.Query().Get("keep")); kStr != "" {
		k, parseErr := strconv.Atoi(kStr)
		if parseErr != nil || k <= 0 || k > 1000 {
			return 0, "", errors.New("保留版本数量参数 keep 必须为 1~1000 之间的正整数")
		}
		keep = k
		hasQueryKeep = true
	}

	if req.Body != nil && req.ContentLength != 0 {
		limited := io.LimitReader(req.Body, 16*1024)
		raw, readErr := io.ReadAll(limited)
		if readErr != nil {
			return 0, "", fmt.Errorf("读取请求正文失败: %w", readErr)
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			var payload slimRequestPayload
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if decErr := dec.Decode(&payload); decErr != nil {
				return 0, "", fmt.Errorf("JSON 参数格式无效: %w", decErr)
			}
			if payload.Keep != nil {
				if *payload.Keep <= 0 || *payload.Keep > 1000 {
					return 0, "", errors.New("保留版本数量参数 keep 必须为 1~1000 之间的正整数")
				}
				if !hasQueryKeep {
					keep = *payload.Keep
				}
			}
			snapshotID = strings.TrimSpace(payload.SnapshotID)
		}
	}

	return keep, snapshotID, nil
}

func buildSlimResultMap(res *cache.DeleteResult) map[string]interface{} {
	if res == nil {
		return map[string]interface{}{
			"deleted_dirs":  0,
			"deleted_files": 0,
			"failed_count":  1,
			"freed_bytes":   int64(0),
			"freed_mb":      0.0,
			"status":        "failed",
			"message":       "版本清理任务未返回结果",
		}
	}
	freedMB := math.Round(float64(res.FreedBytes)/(1024*1024)*100) / 100
	return map[string]interface{}{
		"deleted_dirs":          res.DeletedDirs,
		"remaining_dirs":        res.RemainingDirs,
		"deleted_files":         res.DeletedFiles,
		"deleted_asset_files":   res.DeletedAssetFiles,
		"deleted_asset_bytes":   res.DeletedAssetBytes,
		"deleted_meta_files":    res.DeletedMetaFiles,
		"deleted_meta_bytes":    res.DeletedMetaBytes,
		"already_missing_count": res.AlreadyMissingCount,
		"skipped_changed_count": res.SkippedChangedCount,
		"failed_count":          res.FailedCount,
		"failures":              res.Failures,
		"freed_bytes":           res.FreedBytes,
		"freed_mb":              freedMB,
		"cancelled":             res.Cancelled,
		"status":                res.Status,
		"message":               res.Message,
		"snapshot_id":           res.SnapshotID,
	}
}
