package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func seedControlTestCache(t *testing.T, baseDir string) {
	t.Helper()
	writePair := func(relPath, content string) {
		full := filepath.Join(baseDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
		ext := fmt.Sprintf(`{"ct":"image/png","lm":"Mon, 01 Jan 2026 00:00:00 GMT","cl":%d}`, len(content))
		if err := os.WriteFile(full+".ext", []byte(ext), 0644); err != nil {
			t.Fatalf("write ext %s: %v", full, err)
		}
	}

	writePair("assets/1710000001/js/app.js", "console.log('v1');")
	writePair("assets/1710000002/js/app.js", "console.log('v2');")
	writePair("assets/1710000003/js/app.js", "console.log('v3');")
	writePair("assets/img/sp/quest/scene/character/body/3040001000.png", "PNG-STORY-BODY")
	writePair("assets/img/sp/cjs/npc_3040001000_01.js", "CJS-BATTLE")
	writePair("assets/img/sp/ui/icon/skill/1.png", "PNG-CORE-UI")
	writePair("assets/img/sp/event/common/btn.png", "PNG-EVENT-COMMON")
	writePair("assets/img/sp/event/teamraid075/top.png", "PNG-EVENT-RAID")
	writePair("assets/sound/voice/3040001000_v_001.mp3", "MP3-VOICE")
	writePair("assets/unknown_folder/misc.bin", "MISC-OTHER")
	writePair("hosts/gbf_mbga_static/mbga/banner/top.png", "MBGA-BANNER")
}

func waitForControlCacheTaskIdle(t *testing.T, ctrl *ControlServer) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req := httptest.NewRequest(http.MethodGet, "/api/cache/task-status", nil)
		req.Host = "127.0.0.1:8125"
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("task-status expected 200, got %d", w.Code)
		}
		var status map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatalf("unmarshal task-status: %v", err)
		}
		activeID, _ := status["active_task_id"].(string)
		if activeID == "" {
			return status
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for background cache task to finish")
	return nil
}

func TestCacheBreakdownScanSearchAndTargetedDeleteAPIs(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheDir := filepath.Join(tempDir, "cache")
	seedControlTestCache(t, cacheDir)

	cacheMgr := cache.NewManager(cacheDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	doReq := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Host = "127.0.0.1:8125"
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, r)
		return w
	}

	// 1. Before scan: GET /api/cache/breakdown returns has_snapshot=false without scanning disk
	wBefore := doReq(http.MethodGet, "/api/cache/breakdown", "")
	if wBefore.Code != http.StatusOK {
		t.Fatalf("breakdown before scan expected 200, got %d", wBefore.Code)
	}
	var beforeData map[string]interface{}
	if err := json.Unmarshal(wBefore.Body.Bytes(), &beforeData); err != nil {
		t.Fatalf("unmarshal breakdown: %v", err)
	}
	if beforeData["has_snapshot"] != false {
		t.Fatalf("expected has_snapshot=false before scan, got %v", beforeData["has_snapshot"])
	}

	// Search before scan must return 409 Conflict
	wSearchNoSnap := doReq(http.MethodGet, "/api/cache/search?q=3040001000", "")
	if wSearchNoSnap.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict searching before scan, got %d", wSearchNoSnap.Code)
	}

	// 2. Start scan: POST /api/cache/breakdown/scan returns 202 Accepted + task_id
	wScan := doReq(http.MethodPost, "/api/cache/breakdown/scan", "")
	if wScan.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for scan start, got %d: %s", wScan.Code, wScan.Body.String())
	}
	var scanStart map[string]interface{}
	if err := json.Unmarshal(wScan.Body.Bytes(), &scanStart); err != nil {
		t.Fatalf("unmarshal scan start: %v", err)
	}
	taskID, _ := scanStart["task_id"].(string)
	if !strings.HasPrefix(taskID, "scan-") {
		t.Fatalf("expected scan task_id, got %q", taskID)
	}

	waitForControlCacheTaskIdle(t, ctrl)

	// 3. After scan: GET /api/cache/breakdown?keep=2 returns completed snapshot & custom preview
	wAfter := doReq(http.MethodGet, "/api/cache/breakdown?keep=2", "")
	if wAfter.Code != http.StatusOK {
		t.Fatalf("breakdown after scan expected 200, got %d", wAfter.Code)
	}
	var afterData struct {
		OK          bool                     `json:"ok"`
		HasSnapshot bool                     `json:"has_snapshot"`
		Snapshot    *cache.BreakdownSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(wAfter.Body.Bytes(), &afterData); err != nil {
		t.Fatalf("unmarshal breakdown snapshot: %v", err)
	}
	if !afterData.HasSnapshot || afterData.Snapshot == nil || !afterData.Snapshot.Valid {
		t.Fatalf("expected valid snapshot after scan, got %+v", afterData)
	}
	snapID := afterData.Snapshot.SnapshotID
	if afterData.Snapshot.AssetFiles != 11 {
		t.Fatalf("expected 11 asset files, got %d", afterData.Snapshot.AssetFiles)
	}
	prev2, ok := afterData.Snapshot.RetentionPreviews["2"]
	if !ok || prev2.Keep != 2 || prev2.PruneDirs != 1 {
		t.Fatalf("expected retention preview keep=2 with 1 pruned dir, got %+v", afterData.Snapshot.RetentionPreviews)
	}

	// 4. Search by URL and keyword
	wSearch := doReq(http.MethodGet, "/api/cache/search?q=https://prd-game-a1-granbluefantasy.akamaized.net/assets/img/sp/quest/scene/character/body/3040001000.png%3Fv%3D1&limit=10", "")
	if wSearch.Code != http.StatusOK {
		t.Fatalf("search expected 200, got %d: %s", wSearch.Code, wSearch.Body.String())
	}
	var searchRes cache.SearchResponse
	if err := json.Unmarshal(wSearch.Body.Bytes(), &searchRes); err != nil {
		t.Fatalf("unmarshal search response: %v", err)
	}
	if searchRes.TotalMatches != 1 || len(searchRes.Items) != 1 {
		t.Fatalf("expected 1 matched item, got %d", searchRes.TotalMatches)
	}
	targetItem := searchRes.Items[0]
	if targetItem.Category != cache.CategoryStory {
		t.Fatalf("expected story category, got %q", targetItem.Category)
	}

	// Empty query must be rejected with 400
	if wEmpty := doReq(http.MethodGet, "/api/cache/search?q=", ""); wEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty search query, got %d", wEmpty.Code)
	}
	// Out-of-range limit must be rejected with 400
	if wLimit := doReq(http.MethodGet, "/api/cache/search?q=png&limit=201", ""); wLimit.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit=201, got %d", wLimit.Code)
	}

	// 5. Targeted delete via POST /api/cache/invalidate
	invBody, _ := json.Marshal(map[string]interface{}{
		"snapshot_id": snapID,
		"entries": []map[string]interface{}{
			{
				"ns":  targetItem.NS,
				"key": targetItem.Key,
			},
		},
	})
	wInv := doReq(http.MethodPost, "/api/cache/invalidate", string(invBody))
	if wInv.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for invalidate, got %d: %s", wInv.Code, wInv.Body.String())
	}
	taskStatus := waitForControlCacheTaskIdle(t, ctrl)
	summary, _ := taskStatus["last_task_summary"].(map[string]interface{})
	if summary["task_type"] != "invalidate" || summary["status"] != "completed" {
		t.Fatalf("expected completed invalidate task summary, got %+v", summary)
	}

	// Snapshot must now be marked stale (Valid=false), so repeating invalidate with old snapID returns 409 Conflict
	wInvStale := doReq(http.MethodPost, "/api/cache/invalidate", string(invBody))
	if wInvStale.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when using invalidated snapshot_id, got %d", wInvStale.Code)
	}
}

func TestCacheCategoryCleanupAndSlimPreviewAPIs(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheDir := filepath.Join(tempDir, "cache")
	seedControlTestCache(t, cacheDir)

	cacheMgr := cache.NewManager(cacheDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	doReq := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Host = "127.0.0.1:8125"
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, r)
		return w
	}

	// Run scan first
	if w := doReq(http.MethodPost, "/api/cache/breakdown/scan", ""); w.Code != http.StatusAccepted {
		t.Fatalf("scan start failed: %d", w.Code)
	}
	waitForControlCacheTaskIdle(t, ctrl)

	latest, _ := cacheMgr.GetBreakdownSnapshot(0)
	if latest == nil {
		t.Fatalf("expected snapshot")
	}

	// 1. Reject `versioned` category in /api/cache/clear-category with 400 Bad Request
	wVer := doReq(http.MethodPost, "/api/cache/clear-category", fmt.Sprintf(`{"snapshot_id":%q,"categories":["versioned"]}`, latest.SnapshotID))
	if wVer.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when clearing versioned category via clear-category, got %d", wVer.Code)
	}

	// 2. Clear `event_gacha` and `audio` (defaulting to gbf namespace; gbf_mbga_static must remain untouched)
	wClearCat := doReq(http.MethodPost, "/api/cache/clear-category", fmt.Sprintf(`{"snapshot_id":%q,"categories":["event_gacha","audio"]}`, latest.SnapshotID))
	if wClearCat.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for clear-category, got %d: %s", wClearCat.Code, wClearCat.Body.String())
	}
	waitForControlCacheTaskIdle(t, ctrl)

	// Verify event/common/btn.png (core_ui) and gbf_mbga_static are still on disk, while teamraid075 and sound are gone
	if _, err := os.Stat(filepath.Join(cacheDir, "assets/img/sp/event/common/btn.png")); err != nil {
		t.Errorf("core_ui event/common/btn.png should NOT be deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "hosts/gbf_mbga_static/mbga/banner/top.png")); err != nil {
		t.Errorf("non-gbf namespace should NOT be deleted by default: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "assets/img/sp/event/teamraid075/top.png")); !os.IsNotExist(err) {
		t.Errorf("event_gacha teamraid075/top.png should be deleted")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "assets/sound/voice/3040001000_v_001.mp3")); !os.IsNotExist(err) {
		t.Errorf("audio file should be deleted")
	}

	// 3. Re-scan and test POST /api/cache/slim with keep=2
	if w := doReq(http.MethodPost, "/api/cache/breakdown/scan", ""); w.Code != http.StatusAccepted {
		t.Fatalf("rescan failed: %d", w.Code)
	}
	waitForControlCacheTaskIdle(t, ctrl)
	latest2, _ := cacheMgr.GetBreakdownSnapshot(0)

	wSlim := doReq(http.MethodPost, "/api/cache/slim", fmt.Sprintf(`{"keep":2,"snapshot_id":%q}`, latest2.SnapshotID))
	if wSlim.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for slim, got %d: %s", wSlim.Code, wSlim.Body.String())
	}
	statusAfterSlim := waitForControlCacheTaskIdle(t, ctrl)
	slimRes, _ := statusAfterSlim["last_slim_result"].(map[string]interface{})
	if slimRes["deleted_dirs"] != float64(1) {
		t.Fatalf("expected deleted_dirs=1, got %+v", slimRes)
	}
	// Compatible deleted_files includes data + .ext (2 files), and split fields are also present
	if slimRes["deleted_files"] != float64(2) || slimRes["deleted_asset_files"] != float64(1) || slimRes["deleted_meta_files"] != float64(1) {
		t.Fatalf("unexpected slim file counts: %+v", slimRes)
	}
}

func TestCacheTaskMutualExclusionCancelAndSecurityBoundaries(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheDir := filepath.Join(tempDir, "cache")
	seedControlTestCache(t, cacheDir)

	cacheMgr := cache.NewManager(cacheDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	// 1. Simulate active scan task with task_id
	ctrl.cacheTaskMu.Lock()
	ctrl.activeTaskID = "scan-test-100"
	ctrl.activeTaskType = "scan"
	ctrl.isScanning = true
	ctrl.cacheCancelCh = make(chan struct{})
	ctrl.cacheTaskMu.Unlock()

	doLoopback := func(method, path string, body []byte) *httptest.ResponseRecorder {
		var r *http.Request
		if body != nil {
			r = httptest.NewRequest(method, path, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Host = "127.0.0.1:8125"
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, r)
		return w
	}

	// All maintenance endpoints and CacheDir config change must return 409 Conflict while scan-test-100 is active
	conflictChecks := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/cache/breakdown/scan", ""},
		{http.MethodPost, "/api/cache/audit", ""},
		{http.MethodPost, "/api/cache/slim", ""},
		{http.MethodPost, "/api/cache/boost", ""},
		{http.MethodPost, "/api/cache/clear", ""},
		{http.MethodPost, "/api/config/apply", fmt.Sprintf(`{"cache_dir":%q}`, filepath.Join(tempDir, "other_cache"))},
	}
	for _, tc := range conflictChecks {
		var b []byte
		if tc.body != "" {
			b = []byte(tc.body)
		}
		w := doLoopback(tc.method, tc.path, b)
		if w.Code != http.StatusConflict {
			t.Errorf("%s %s expected 409 Conflict while task active, got %d: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// RAM-only clear (?ram_only=true) must still succeed without conflict
	if wRAM := doLoopback(http.MethodPost, "/api/cache/clear?ram_only=true", nil); wRAM.Code != http.StatusOK {
		t.Errorf("expected 200 OK for ram_only=true clear while disk task active, got %d", wRAM.Code)
	}

	// Cancel with wrong task_id must return 409 Conflict and NOT close cacheCancelCh
	wWrongCancel := doLoopback(http.MethodPost, "/api/cache/cancel-task", []byte(`{"task_id":"scan-wrong-999"}`))
	if wWrongCancel.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for wrong task_id cancel, got %d", wWrongCancel.Code)
	}

	// Cancel with matching task_id must return 200 OK and close cacheCancelCh
	wRightCancel := doLoopback(http.MethodPost, "/api/cache/cancel-task", []byte(`{"task_id":"scan-test-100"}`))
	if wRightCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for matching task_id cancel, got %d", wRightCancel.Code)
	}

	ctrl.finishCacheTask("scan-test-100", "scan", "cancelled", true, nil, "")

	// Cancel after task finished with old task_id must return 200 OK with already_finished=true
	wDoneCancel := doLoopback(http.MethodPost, "/api/cache/cancel-task", []byte(`{"task_id":"scan-test-100"}`))
	if wDoneCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for already finished task cancel, got %d", wDoneCancel.Code)
	}

	// 2. Non-Loopback RemoteAddr and disallowed Origin/Host must be rejected with 403 on all new endpoints
	newEndpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/cache/breakdown"},
		{http.MethodPost, "/api/cache/breakdown/scan"},
		{http.MethodGet, "/api/cache/search?q=png"},
		{http.MethodPost, "/api/cache/invalidate"},
		{http.MethodPost, "/api/cache/clear-category"},
	}
	for _, ep := range newEndpoints {
		// Non-loopback RemoteAddr
		rLAN := httptest.NewRequest(ep.method, ep.path, nil)
		rLAN.Host = "127.0.0.1:8125"
		rLAN.RemoteAddr = "192.168.1.88:54321"
		wLAN := httptest.NewRecorder()
		ctrl.handleRoute(wLAN, rLAN)
		if wLAN.Code != http.StatusForbidden {
			t.Errorf("%s %s with LAN RemoteAddr expected 403, got %d", ep.method, ep.path, wLAN.Code)
		}

		// Evil Origin
		rOrigin := httptest.NewRequest(ep.method, ep.path, nil)
		rOrigin.Host = "127.0.0.1:8125"
		rOrigin.RemoteAddr = "127.0.0.1:54321"
		rOrigin.Header.Set("Origin", "https://evil.example.com")
		wOrigin := httptest.NewRecorder()
		ctrl.handleRoute(wOrigin, rOrigin)
		if wOrigin.Code != http.StatusForbidden {
			t.Errorf("%s %s with evil Origin expected 403, got %d", ep.method, ep.path, wOrigin.Code)
		}
	}

	// 3. Batch size > 200 and oversized body > 128 KiB must be rejected with 400 Bad Request
	var entries201 []cache.DeleteTargetEntry
	for i := 0; i < 201; i++ {
		entries201 = append(entries201, cache.DeleteTargetEntry{NS: "gbf", Key: fmt.Sprintf("assets/img/%d.png", i)})
	}
	b201, _ := json.Marshal(map[string]interface{}{
		"snapshot_id": "snap-1-1",
		"entries":     entries201,
	})
	if w201 := doLoopback(http.MethodPost, "/api/cache/invalidate", b201); w201.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for 201 entries, got %d", w201.Code)
	}

	oversized := bytes.Repeat([]byte("a"), 130*1024)
	if wBig := doLoopback(http.MethodPost, "/api/cache/invalidate", oversized); wBig.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for >128KB payload, got %d", wBig.Code)
	}
}

func TestControlAsyncFailureResultSynthesis(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheDir := filepath.Join(tempDir, "cache")
	seedControlTestCache(t, cacheDir)

	cacheMgr := cache.NewManager(cacheDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)

	// Point cacheMgr to a regular file so live /api/cache/slim fails inside the background goroutine
	notADir := filepath.Join(tempDir, "not_a_dir.bin")
	if err := os.WriteFile(notADir, []byte("file"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	cacheMgr.SetCacheBase(notADir)

	req := httptest.NewRequest(http.MethodPost, "/api/cache/slim?keep=2", nil)
	req.Host = "127.0.0.1:8125"
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting slim, got %d", w.Code)
	}

	taskStatus := waitForControlCacheTaskIdle(t, ctrl)
	slimRes, _ := taskStatus["last_slim_result"].(map[string]interface{})
	if slimRes == nil || slimRes["status"] != "failed" {
		t.Fatalf("expected last_slim_result status=failed when cache root is invalid, got %+v", slimRes)
	}
	if slimRes["failed_count"] != float64(1) {
		t.Fatalf("expected last_slim_result failed_count=1, got %+v", slimRes)
	}
}

