package cache

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClassifyCacheEntry(t *testing.T) {
	cases := []struct {
		name     string
		ns       string
		key      string
		expected string
	}{
		// Priority 1: versioned
		{"versioned assets js", "gbf", "assets/1728446123/js/app.js", CategoryVersioned},
		{"versioned assets_en js", "gbf", "assets_en/1728446123/js/app.js", CategoryVersioned},
		{"versioned assets_jp css", "gbf", "assets_jp/1728446123/css/style.css", CategoryVersioned},
		{"versioned audio inside version dir", "gbf", "assets/1728446123/sound/bgm/battle.mp3", CategoryVersioned},
		{"versioned non-gbf namespace", "game-a.mbga.jp", "assets/1728446123/js/bundle.js", CategoryVersioned},
		{"non-numeric version dir is not versioned", "gbf", "assets/v1728446123/js/app.js", CategoryOther},
		{"leading zero version dir is not versioned", "gbf", "assets/012345/js/app.js", CategoryOther},
		{"unsupported prefix version dir is other", "gbf", "assets_kr/1728446123/js/app.js", CategoryOther},

		// Priority 2: battle_cjs
		{"battle_cjs standard", "gbf", "assets/img/sp/cjs/npc_3040001000_01.js", CategoryBattleCJS},
		{"battle_cjs en", "gbf", "assets_en/img/sp/cjs/enemy_1001.png", CategoryBattleCJS},
		{"battle_cjs fallback without assets prefix", "gbf", "img/sp/cjs/summon_2040001.js", CategoryBattleCJS},

		// Priority 3: story
		{"story quest scene", "gbf", "assets/img/sp/quest/scene/character/body/3040001000.png", CategoryStory},
		{"story sidestory", "gbf", "assets/img/sp/sidestory/banner/side_01.png", CategoryStory},
		{"story comic", "gbf", "assets_en/img/sp/comic/episode_100.jpg", CategoryStory},
		{"story audio inside quest scene", "gbf", "assets/img/sp/quest/scene/voice/scene_01.mp3", CategoryStory},

		// Priority 4: core_ui
		{"core_ui event common", "gbf", "assets/img/sp/event/common/parts/btn_ok.png", CategoryCoreUI},
		{"core_ui quest assist", "gbf", "assets/img/sp/quest/assist/bg_list.png", CategoryCoreUI},
		{"core_ui quest select", "gbf", "assets/img/sp/quest/select/chapter_bg.png", CategoryCoreUI},
		{"core_ui sp ui", "gbf", "assets/img/sp/ui/icon/weapon.png", CategoryCoreUI},
		{"core_ui sp mypage", "gbf", "assets/img/sp/mypage/bg_town.jpg", CategoryCoreUI},
		{"core_ui sp arcarum", "gbf", "assets/img/sp/arcarum/map_01.png", CategoryCoreUI},
		{"core_ui sp arcarum2", "gbf", "assets/img/sp/arcarum2/map_02.png", CategoryCoreUI},
		{"core_ui sp replicard", "gbf", "assets/img/sp/replicard/stage.png", CategoryCoreUI},
		{"core_ui sprite", "gbf", "assets/img/sprite/sheet_01.png", CategoryCoreUI},
		{"core_ui fallback without assets prefix", "gbf", "img/sp/ui/header.png", CategoryCoreUI},

		// Priority 5: event_gacha (and segment boundary verification)
		{"event_gacha specific event", "gbf", "assets/img/sp/event/teamraid075/top_logo.png", CategoryEventGacha},
		{"event_gacha common prefix but not common segment", "gbf", "assets/img/sp/event/common_campaign_2026/banner.png", CategoryEventGacha},
		{"event_gacha gacha", "gbf", "assets/img/sp/gacha/header_legend.png", CategoryEventGacha},
		{"event_gacha banner", "gbf", "assets/img/sp/banner/camp_01.png", CategoryEventGacha},
		{"event_gacha campaign", "gbf", "assets/img/sp/campaign/summer/top.png", CategoryEventGacha},
		{"event_gacha loginbonus", "gbf", "assets/img/sp/loginbonus/stamp.png", CategoryEventGacha},

		// Priority 6: audio
		{"audio sound dir", "gbf", "assets/sound/voice/3040001_attack.mp3", CategoryAudio},
		{"audio sound fallback", "gbf", "sound/bgm/main_theme.ogg", CategoryAudio},
		{"audio by extension outside sound dir", "gbf", "assets/movie/audio_track.m4a", CategoryAudio},

		// Priority 7: other
		{"other unknown quest subdir", "gbf", "assets/img/sp/quest/unknown_future_mode/icon.png", CategoryOther},
		{"other arcarum prefix mismatch", "gbf", "assets/img/sp/arcarum_special_promo/banner.png", CategoryOther},
		{"other unsupported language prefix", "gbf", "assets_kr/sound/voice/test.mp3", CategoryOther},
		{"other non-gbf namespace non-versioned", "game-a.mbga.jp", "assets/img/sp/ui/btn.png", CategoryOther},
		{"other font file", "gbf", "assets/font/gbf_font.woff2", CategoryOther},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyCacheEntry(tc.ns, tc.key)
			if got != tc.expected {
				t.Fatalf("ClassifyCacheEntry(%q, %q) = %q, want %q", tc.ns, tc.key, got, tc.expected)
			}
		})
	}
}

func TestBreakdownScanSearchAndRetentionPreview(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	writeAsset := func(relPath string, data []byte, withExt bool) {
		t.Helper()
		full := filepath.Join(tmpDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}
		if err := os.WriteFile(full, data, 0644); err != nil {
			t.Fatalf("write failed: %v", err)
		}
		if withExt {
			fi, _ := os.Stat(full)
			_ = writeDiskExtFile(full, diskMeta{
				ContentType: "application/octet-stream",
				Version:     1,
				Verified:    true,
				Size:        int64(len(data)),
				MTimeNano:   fi.ModTime().UnixNano(),
			})
		}
	}

	// Populate 3 versions in gbf/assets, 2 versions in gbf/assets_en, 1 version in hosts/sub.example.com/assets
	writeAsset("assets/100/js/a.js", []byte("console.log(100);"), true)
	writeAsset("assets/200/js/a.js", []byte("console.log(200);"), true)
	writeAsset("assets/300/js/a.js", []byte("console.log(300);"), false)
	writeAsset("assets_en/10/js/en.js", []byte("console.log('en10');"), true)
	writeAsset("assets_en/20/js/en.js", []byte("console.log('en20');"), false)
	writeAsset("hosts/sub.example.com/assets/500/js/h.js", []byte("console.log('host500');"), true)

	// Populate non-versioned categories
	writeAsset("assets/img/sp/cjs/effect.js", []byte("var cjs=1;"), true)
	writeAsset("assets/img/sp/quest/scene/ch1.png", []byte("\x89PNG\r\n\x1a\nstory"), true)
	writeAsset("assets/img/sp/ui/btn.png", []byte("\x89PNG\r\n\x1a\ncoreui"), true)
	writeAsset("img/sp/ui/btn.png", []byte("\x89PNG\r\n\x1a\ncoreui_alias"), false) // GBF fallback physical alias
	writeAsset("assets/img/sp/event/raid01/top.png", []byte("\x89PNG\r\n\x1a\nevent"), false)
	writeAsset("assets/sound/bgm/ost.mp3", []byte("ID3audiobytes"), true)
	writeAsset("hosts/sub.example.com/custom/data.json", []byte(`{"ok":true}`), false)

	// Orphan .ext file and excluded .tmp. / .quarantine files
	orphanExt := filepath.Join(tmpDir, "assets", "img", "sp", "orphan.png.ext")
	_ = os.MkdirAll(filepath.Dir(orphanExt), 0755)
	_ = os.WriteFile(orphanExt, []byte(`{"v":1}`), 0644)

	tmpFile := filepath.Join(tmpDir, "assets", "img", "sp", "writing.png.tmp.123.456.1")
	_ = os.WriteFile(tmpFile, []byte("inflight"), 0644)

	quarFile := filepath.Join(tmpDir, "assets", "js", "set-error-handler.js.quarantine.999")
	_ = os.MkdirAll(filepath.Dir(quarFile), 0755)
	_ = os.WriteFile(quarFile, []byte("quarantined"), 0644)

	var lastProg ScanProgress
	snap := mgr.ScanBreakdownWithProgress(func(p ScanProgress) {
		lastProg = p
	}, nil)

	if snap == nil || !snap.Valid || snap.Status != "completed" {
		t.Fatalf("expected valid completed snapshot, got %+v", snap)
	}
	if !lastProg.Done || lastProg.Status != "completed" {
		t.Fatalf("expected completed progress callback, got %+v", lastProg)
	}

	// Verify scan did NOT warm RAM SLRU or ResidentPool
	if ramItems, _ := mgr.Stats(); ramItems != 0 {
		t.Fatalf("expected 0 RAM items after scan, got %d", ramItems)
	}

	// 13 managed data files, 8 paired .ext files, 1 orphan .ext, 2 excluded (.tmp. + .quarantine)
	if snap.AssetFiles != 13 {
		t.Fatalf("expected 13 asset files, got %d", snap.AssetFiles)
	}
	if snap.MetadataFiles != 8 {
		t.Fatalf("expected 8 paired metadata files, got %d", snap.MetadataFiles)
	}
	if snap.OrphanMetadataFiles != 1 {
		t.Fatalf("expected 1 orphan metadata file, got %d", snap.OrphanMetadataFiles)
	}
	if snap.ExcludedFiles != 2 {
		t.Fatalf("expected 2 excluded files, got %d", snap.ExcludedFiles)
	}

	// Check retention preview for keep=2:
	// gbf:assets has 3 versions (300, 200, 100) -> keeps [300, 200], prunes [100] (1 dir, 1 asset file, 1 meta file)
	// gbf:assets_en has 2 versions (20, 10) -> keeps [20, 10], prunes []
	// sub.example.com:assets has 1 version (500) -> keeps [500], prunes []
	prev2, ok := snap.RetentionPreviews["2"]
	if !ok {
		t.Fatalf("missing retention preview for keep=2")
	}
	if prev2.PruneDirs != 1 || prev2.PruneAssetFiles != 1 || prev2.PruneMetadataFiles != 1 {
		t.Fatalf("unexpected keep=2 preview: %+v", prev2)
	}

	// Search by full URL with query string (query string must be ignored)
	hostNorm := func(h string) (string, bool) {
		if strings.Contains(h, "granbluefantasy.jp") {
			return "gbf", true
		}
		return strings.ToLower(h), true
	}
	sr, err := mgr.SearchSnapshot(snap.SnapshotID, "https://prd-game-a-granbluefantasy.akamaized.net/assets/img/sp/ui/btn.png?v=999", 50, func(h string) (string, bool) {
		if strings.Contains(h, "granbluefantasy") {
			return "gbf", true
		}
		return hostNorm(h)
	})
	if err != nil {
		t.Fatalf("SearchSnapshot URL failed: %v", err)
	}
	if sr.TotalMatches != 2 {
		t.Fatalf("expected 2 matches (primary + fallback alias) for btn.png, got %d", sr.TotalMatches)
	}
	if !sr.Items[0].HasAlias || sr.Items[0].AliasKey == "" {
		t.Fatalf("expected HasAlias=true on matched item, got %+v", sr.Items[0])
	}

	// Search bounds validation
	if _, err := mgr.SearchSnapshot(snap.SnapshotID, "   ", 50, nil); !errors.Is(err, ErrEmptySearchQuery) {
		t.Fatalf("expected ErrEmptySearchQuery, got %v", err)
	}
	if _, err := mgr.SearchSnapshot(snap.SnapshotID, "btn", 201, nil); !errors.Is(err, ErrInvalidSearchLimit) {
		t.Fatalf("expected ErrInvalidSearchLimit, got %v", err)
	}
}

func TestInvalidationConcurrencyBoundaries(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	validJS := []byte("console.log('old_version');")
	headers := map[string]string{"content-type": "application/javascript"}

	// 1. Pre-deletionpersistTask must NOT restore deleted entry on disk or diskMeta
	item, cleanKey, filePath, gen, staleTok, ok := mgr.saveRAMInternal("gbf", "/assets/js/test_race.js", headers, validJS, false, false)
	if !ok || item == nil {
		t.Fatalf("saveRAMInternal failed")
	}

	// Invalidate the entry before saveToDiskWithToken runs
	mgr.Invalidate("gbf", "/assets/js/test_race.js")

	// Now attempt to execute the pre-deletion persist task with staleTok
	if saved := mgr.saveToDiskWithToken(filePath, headers, validJS, gen, staleTok); saved {
		t.Fatalf("expected pre-deletion saveToDiskWithToken to be rejected after Invalidate")
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("expected file to remain absent on disk after stale persist rejection")
	}
	if _, ok := mgr.getDiskMeta(makeRAMKey("gbf", cleanKey)); ok {
		t.Fatalf("expected diskMeta to remain empty after stale persist rejection")
	}

	// 2. Pre-deletion disk read must NOT backfill RAM or diskMeta after Invalidate
	if !mgr.Save("/assets/js/read_race.js", headers, validJS) {
		t.Fatalf("Save failed")
	}
	mgr.ClearRAM()
	mgr.clearDiskMeta()

	rFilePath, _ := mgr.ResolvePathWithNamespace("gbf", "/assets/js/read_race.js")
	preReadTok := mgr.acquireOpToken("gbf", "assets/js/read_race.js")

	// Simulate file read completing while Invalidate happens concurrently before backfill commit
	loadedItem, err := mgr.loadAndValidateDiskItemWithToken("gbf", "assets/js/read_race.js", rFilePath, preReadTok)
	if err != nil || loadedItem == nil {
		t.Fatalf("expected loadAndValidateDiskItemWithToken to succeed before deletion: %v", err)
	}
	mgr.Invalidate("gbf", "/assets/js/read_race.js")

	// Now verify that an operation carrying preReadTok is rejected and cannot backfill diskMeta or RAM
	if mgr.isOpTokenValid(preReadTok) {
		t.Fatalf("expected preReadTok to be invalid after Invalidate")
	}
	if _, err := mgr.loadAndValidateDiskItemWithToken("gbf", "assets/js/read_race.js", rFilePath, preReadTok); err == nil {
		t.Fatalf("expected load after file deletion to fail")
	}

	// 3. Pre-deletion temp file transaction must NOT commit after Invalidate
	fTmp, tmpPath, tmpGen, err := mgr.CreateDiskTempFile("gbf", "/assets/js/stream_race.js")
	if err != nil {
		t.Fatalf("CreateDiskTempFile failed: %v", err)
	}
	_, _ = fTmp.Write(validJS)
	_ = fTmp.Close()

	// Invalidate while temp file is in flight
	mgr.Invalidate("gbf", "/assets/js/stream_race.js")

	if committedItem, committed := mgr.CommitDiskTempFile("gbf", "/assets/js/stream_race.js", headers, tmpPath, int64(len(validJS)), tmpGen); committed || committedItem != nil {
		t.Fatalf("expected CommitDiskTempFile to reject temp file created before Invalidate")
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected rejected temp file to be cleaned up")
	}

	// 4. Post-deletion new write for the same key MUST succeed normally
	newJS := []byte("console.log('new_version_after_delete');")
	if !mgr.Save("/assets/js/test_race.js", headers, newJS) {
		t.Fatalf("expected post-deletion Save to succeed")
	}
	gotItem, src := mgr.Get("/assets/js/test_race.js")
	if gotItem == nil || string(gotItem.Data) != string(newJS) {
		t.Fatalf("expected new version from cache (src=%s), got %+v", src, gotItem)
	}

	// 5. Ring buffer bound check (> maxInvalidSeqEntries invalidations)
	unrelatedTok := mgr.acquireOpToken("gbf", "assets/js/unrelated_survivor.js")
	shard, _ := mgr.tokenShardFor("gbf", "assets/js/unrelated_survivor.js")
	for i := 0; i < maxInvalidSeqEntries+200; i++ {
		shard.mu.Lock()
		bumpInvalidateTokenLocked(shard, fmt.Sprintf("gbf:synthetic_key_%d", i))
		if len(shard.invalidSeq) > maxInvalidSeqEntries {
			shard.mu.Unlock()
			t.Fatalf("invalidSeq map exceeded bound %d: got %d", maxInvalidSeqEntries, len(shard.invalidSeq))
		}
		shard.mu.Unlock()
	}
	if mgr.isOpTokenValid(unrelatedTok) {
		t.Fatalf("expected token older than ring buffer wrap-around to be invalidated by minValidSeq")
	}
	freshTok := mgr.acquireOpToken("gbf", "assets/js/unrelated_survivor.js")
	if !mgr.isOpTokenValid(freshTok) {
		t.Fatalf("expected fresh token after ring wrap-around to be valid")
	}
}

func TestControlledDeletionCategoryCleanupAndSlim(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	headers := map[string]string{"content-type": "image/png"}
	pngData := []byte("\x89PNG\r\n\x1a\nvalid_png_payload")

	// Save both GBF primary and fallback alias on disk
	if !mgr.SaveWithNamespace("gbf", "assets/img/sp/ui/dual.png", headers, pngData) {
		t.Fatalf("Save primary failed")
	}
	if !mgr.SaveWithNamespace("gbf", "img/sp/ui/dual.png", headers, pngData) {
		t.Fatalf("Save alias failed")
	}
	// Save story and event assets
	if !mgr.SaveWithNamespace("gbf", "assets/img/sp/quest/scene/s1.png", headers, pngData) {
		t.Fatalf("Save story failed")
	}
	if !mgr.SaveWithNamespace("gbf", "assets/img/sp/event/ev01/banner.png", headers, pngData) {
		t.Fatalf("Save event failed")
	}
	// Save non-GBF asset
	if !mgr.SaveWithNamespace("other.host", "assets/img/sp/event/ev01/banner.png", headers, pngData) {
		t.Fatalf("Save other.host failed")
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	// Modify story asset after snapshot to verify SkippedChangedCount
	storyPath, _ := mgr.ResolvePathWithNamespace("gbf", "assets/img/sp/quest/scene/s1.png")
	time.Sleep(15 * time.Millisecond)
	if err := os.WriteFile(storyPath, append(pngData, []byte("_modified")...), 0644); err != nil {
		t.Fatalf("modify story file failed: %v", err)
	}

	// Targeted deletion of dual.png should delete both physical files (primary + fallback alias)
	delRes, err := mgr.DeleteSnapshotEntriesWithProgress(snap.SnapshotID, []DeleteTargetEntry{
		{NS: "gbf", Key: "assets/img/sp/ui/dual.png"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("DeleteSnapshotEntriesWithProgress failed: %v", err)
	}
	if delRes.DeletedAssetFiles != 2 || delRes.DeletedMetaFiles != 2 {
		t.Fatalf("expected 2 asset files and 2 meta files deleted for dual alias, got %+v", delRes)
	}

	// Snapshot must now be marked stale
	latestSnap, _ := mgr.GetBreakdownSnapshot(0)
	if latestSnap == nil || latestSnap.Valid || !latestSnap.Stale {
		t.Fatalf("expected snapshot to be marked stale after deletion, got %+v", latestSnap)
	}

	// Re-scan: s1.png was modified earlier; let's scan and then modify s1.png again to test category cleanup skipping changed files
	snap2 := mgr.ScanBreakdownWithProgress(nil, nil)
	time.Sleep(15 * time.Millisecond)
	_ = os.WriteFile(storyPath, append(pngData, []byte("_modified_twice")...), 0644)

	// Reject versioned category in ClearCategoriesWithProgress
	if _, err := mgr.ClearCategoriesWithProgress(snap2.SnapshotID, []string{CategoryVersioned}, nil, nil, nil); !errors.Is(err, ErrVersionedCatDisallowed) {
		t.Fatalf("expected ErrVersionedCatDisallowed, got %v", err)
	}

	// Clear story and event_gacha in default (gbf) namespace
	catRes, err := mgr.ClearCategoriesWithProgress(snap2.SnapshotID, []string{CategoryStory, CategoryEventGacha}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
	}
	if catRes.DeletedAssetFiles != 1 || catRes.SkippedChangedCount != 1 {
		t.Fatalf("expected 1 deleted (event) and 1 skipped (modified story), got %+v", catRes)
	}
	// Verify modified story file is still on disk
	if _, err := os.Stat(storyPath); err != nil {
		t.Fatalf("expected modified story file to be preserved on disk: %v", err)
	}

	// Verify version pruning preserves .tmp.* and .quarantine files inside stale version directories
	jsHeaders := map[string]string{"content-type": "application/javascript"}
	jsBody := []byte("console.log('ver');")
	for _, v := range []string{"100", "200", "300"} {
		mgr.SaveWithNamespace("gbf", fmt.Sprintf("assets/%s/js/main.js", v), jsHeaders, jsBody)
	}
	// Place a .tmp. file inside version 100 (which will be pruned when keeping 2)
	tmpInVer100 := filepath.Join(tmpDir, "assets", "100", "js", "inflight.js.tmp.1.1.1")
	_ = os.WriteFile(tmpInVer100, []byte("inflight"), 0644)

	snap3 := mgr.ScanBreakdownWithProgress(nil, nil)
	slimRes, err := mgr.PruneStaleVersionsDetailed(2, snap3.SnapshotID, nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed failed: %v", err)
	}
	if slimRes.DeletedAssetFiles != 1 || slimRes.DeletedMetaFiles != 1 {
		t.Fatalf("expected 1 asset + 1 meta deleted from version 100, got %+v", slimRes)
	}
	// Version 100 dir must NOT be deleted because tmpInVer100 is inside it
	if slimRes.DeletedDirs != 0 || slimRes.RemainingDirs != 1 {
		t.Fatalf("expected RemainingDirs=1 and DeletedDirs=0 when .tmp. file present, got %+v", slimRes)
	}
	if _, err := os.Stat(tmpInVer100); err != nil {
		t.Fatalf("expected .tmp. file in version 100 to be preserved: %v", err)
	}
}

func TestTempTokenEvictionAndDiscardFailClosed(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	validJS := []byte("console.log('stale_temp_payload');")

	type tx struct {
		key     string
		tmpPath string
		gen     uint64
	}

	// 1. Verify DiscardDiskTempFile reclaims token registration on aborted downloads
	for i := 0; i < 1100; i++ {
		k := fmt.Sprintf("/assets/js/aborted_%d.js", i)
		f, tp, _, err := mgr.CreateDiskTempFile("gbf", k)
		if err != nil {
			t.Fatalf("CreateDiskTempFile failed at %d: %v", i, err)
		}
		_ = f.Close()
		mgr.DiscardDiskTempFile(tp)
	}
	mgr.tempTokensMu.Lock()
	remainingAfterDiscard := len(mgr.tempTokens)
	mgr.tempTokensMu.Unlock()
	if remainingAfterDiscard != 0 {
		t.Fatalf("expected 0 registered temp tokens after DiscardDiskTempFile, got %d", remainingAfterDiscard)
	}

	// 2. Create 1025 simultaneous active temp transactions to force eviction in tempTokens
	txs := make([]tx, 0, 1025)
	for i := 0; i < 1025; i++ {
		k := fmt.Sprintf("/assets/js/overflow_%d.js", i)
		f, tp, g, err := mgr.CreateDiskTempFile("gbf", k)
		if err != nil {
			t.Fatalf("CreateDiskTempFile overflow failed at %d: %v", i, err)
		}
		_, _ = f.Write(validJS)
		_ = f.Close()
		txs = append(txs, tx{key: k, tmpPath: tp, gen: g})
	}

	// Find an evicted transaction
	var evictedTx *tx
	mgr.tempTokensMu.Lock()
	for i := range txs {
		if _, exists := mgr.tempTokens[txs[i].tmpPath]; !exists {
			evictedTx = &txs[i]
			break
		}
	}
	mgr.tempTokensMu.Unlock()

	if evictedTx == nil {
		t.Fatalf("expected at least one evicted temp token after 1025 simultaneous creations")
	}

	// Invalidate the evicted key and attempt to commit: MUST fail closed and remove temp file
	mgr.Invalidate("gbf", evictedTx.key)
	if item, committed := mgr.CommitDiskTempFile("gbf", evictedTx.key, headers, evictedTx.tmpPath, int64(len(validJS)), evictedTx.gen); committed || item != nil {
		t.Fatalf("expected CommitDiskTempFile to reject evicted/missing token transaction for %s", evictedTx.key)
	}
	if got, _ := mgr.GetWithNamespace("gbf", evictedTx.key); got != nil {
		t.Fatalf("stale payload was written to cache after token eviction for %s", evictedTx.key)
	}

	// Clean up remaining temp files
	for _, item := range txs {
		mgr.DiscardDiskTempFile(item.tmpPath)
	}
}

func TestConcurrentScanSetCacheBaseAndSearchNoDeadlockOrRace(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	mgr := NewManager(dirA, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('concurrent_race_test');")
	for i := 0; i < 20; i++ {
		mgr.SaveWithNamespace("gbf", fmt.Sprintf("assets/100/js/file_%d.js", i), headers, body)
	}
	mgr.SetCacheBase(dirB)
	for i := 0; i < 20; i++ {
		mgr.SaveWithNamespace("gbf", fmt.Sprintf("assets/200/js/file_%d.js", i), headers, body)
	}
	mgr.SetCacheBase(dirA)

	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// Scanner worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				_ = mgr.ScanBreakdownWithProgress(nil, nil)
			}
		}
	}()

	// Directory switcher & invalidator worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		toggle := false
		for {
			select {
			case <-stopCh:
				return
			default:
				if toggle {
					mgr.SetCacheBase(dirA)
				} else {
					mgr.SetCacheBase(dirB)
				}
				toggle = !toggle
				mgr.InvalidateSnapshots("concurrent test invalidation")
			}
		}
	}()

	// Concurrent searcher & snapshot reader worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				_, _ = mgr.SearchSnapshot("", "file_", 20, nil)
				_, _ = mgr.GetBreakdownSnapshot(4)
			}
		}
	}()

	time.Sleep(250 * time.Millisecond)
	close(stopCh)
	wg.Wait()
}

func TestSlimRootValidationErrorAndUnifiedVersionDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	// 1. Create 3 versions where newest version 300 only contains a .tmp. file
	jsHeaders := map[string]string{"content-type": "application/javascript"}
	jsBody := []byte("console.log('v');")
	mgr.SaveWithNamespace("gbf", "assets/100/js/app.js", jsHeaders, jsBody)
	mgr.SaveWithNamespace("gbf", "assets/200/js/app.js", jsHeaders, jsBody)
	ver300Dir := filepath.Join(tmpDir, "assets", "300", "js")
	if err := os.MkdirAll(ver300Dir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ver300Dir, "app.js.tmp.1.2.3"), []byte("partial"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	previewKeep2 := snap.RetentionPreviews["2"]
	if previewKeep2.TotalVersionDirs != 3 || previewKeep2.PruneDirs != 1 {
		t.Fatalf("expected preview with keep=2 to discover all 3 version dirs and mark 1 for pruning, got %+v", previewKeep2)
	}

	// Live slim with keep=2 should prune version 100 (1 dir) matching the preview
	slimRes, err := mgr.PruneStaleVersionsDetailed(2, "", nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed live failed: %v", err)
	}
	if slimRes.Status != "completed" || slimRes.DeletedDirs != 1 {
		t.Fatalf("expected live slim to delete 1 stale version dir (100), got %+v", slimRes)
	}

	// 2. Point cache base to a regular file and verify PruneStaleVersionsDetailed reports Status="failed"
	regularFile := filepath.Join(tmpDir, "not_a_dir.bin")
	if err := os.WriteFile(regularFile, []byte("file"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	mgr.SetCacheBase(regularFile)

	badRes, err := mgr.PruneStaleVersionsDetailed(2, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error return: %v", err)
	}
	if badRes.Status != "failed" || badRes.FailedCount == 0 || len(badRes.Failures) == 0 {
		t.Fatalf("expected Status=failed and FailedCount>0 when cache base is a regular file, got %+v", badRes)
	}
}

func TestPathNormalizationConsistencyForInvalidation(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	// 1. In-flight disk stream created with unclean path (whitespace, trailing dot, mixed-case namespace)
	uncleanPath := "//assets/img/sp/quest/\t3040001000.png."
	cleanPath := "assets/img/sp/quest/3040001000.png"
	f, tmpPath, gen, err := mgr.CreateDiskTempFile("GBF", uncleanPath)
	if err != nil {
		t.Fatalf("CreateDiskTempFile failed: %v", err)
	}
	_, _ = f.Write([]byte("stale_unclean_payload"))
	_ = f.Close()

	// Targeted invalidation using clean canonical path
	mgr.Invalidate("gbf", cleanPath)

	// Commit with the unclean path must be rejected because both map to the same canonical key
	headers := map[string]string{"content-type": "image/png"}
	if _, ok := mgr.CommitDiskTempFile("GBF", uncleanPath, headers, tmpPath, 21, gen); ok {
		t.Fatalf("expected CommitDiskTempFile with unclean path to be rejected after Invalidate on canonical path")
	}
	if item, _ := mgr.GetWithNamespace("gbf", cleanPath); item != nil {
		t.Fatalf("stale unclean temp file should not have been committed to cache")
	}

	// 2. acquireOpToken with unclean path must also be invalidated by canonical Invalidate
	tok := mgr.acquireOpToken("GBF", "/assets//img/./sp/quest/\n999.png.")
	mgr.Invalidate("gbf", "assets/img/sp/quest/999.png")
	if mgr.isOpTokenValid(tok) {
		t.Fatalf("expected opToken acquired with unclean path to be invalidated by canonical Invalidate")
	}
}

func TestPruneStaleVersionsSnapshotRootBindingAndCacheBaseSwitch(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	mgr := NewManager(dirA, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('version_payload');")
	for _, d := range []string{dirA, dirB} {
		mgr.SetCacheBase(d)
		mgr.SaveWithNamespace("gbf", "assets/100/js/app.js", headers, body)
		mgr.SaveWithNamespace("gbf", "assets/200/js/app.js", headers, body)
		mgr.SaveWithNamespace("gbf", "assets/300/js/app.js", headers, body)
	}

	// Switch back to dirA and scan snapshot
	mgr.SetCacheBase(dirA)
	snapA := mgr.ScanBreakdownWithProgress(nil, nil)
	if snapA == nil || !snapA.Valid {
		t.Fatalf("expected valid snapshot on dirA")
	}

	// Switch to dirB before attempting to prune using snapA
	mgr.SetCacheBase(dirB)
	if _, err := mgr.PruneStaleVersionsDetailed(2, snapA.SnapshotID, nil, nil); err == nil {
		t.Fatalf("expected PruneStaleVersionsDetailed with snapA after SetCacheBase(dirB) to fail")
	}

	// Also verify deleteSingleVerifiedEntry directly rejects deletion if CacheBase/gen changed
	idx, ok := snapA.entryByNSKey[makeNSKey("gbf", "assets/100/js/app.js")]
	if !ok {
		t.Fatalf("expected entryFromA in snapA")
	}
	entryFromA := snapA.Entries[idx]
	absBaseA, realBaseA, _, _ := resolveContainedRealBase(snapA.CacheBase)
	res := &DeleteResult{}
	mgr.deleteSingleVerifiedEntry(absBaseA, realBaseA, snapA.Generation, snapA.CacheBase, entryFromA, res, make(map[string]struct{}))
	if !res.Cancelled || res.DeletedAssetFiles != 0 {
		t.Fatalf("expected deleteSingleVerifiedEntry to cancel and delete 0 files when cache base switched, got %+v", res)
	}

	// Verify dirB's assets/100/js/app.js is completely intact
	if _, err := os.Stat(filepath.Join(dirB, "assets", "100", "js", "app.js")); err != nil {
		t.Fatalf("dirB assets/100/js/app.js must remain untouched: %v", err)
	}
}

func TestClearCategoriesPreservesVersionedFallbackPartner(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	// Write legacy unprefixed path 300/js/app.js (classified as CategoryOther)
	mgr.SaveWithNamespace("gbf", "300/js/app.js", headers, []byte("legacy_other"))
	// Write standard versioned path assets/300/js/app.js (classified as CategoryVersioned)
	mgr.SaveWithNamespace("gbf", "assets/300/js/app.js", headers, []byte("versioned_asset"))

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	res, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, []string{"gbf"}, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
	}
	if res.DeletedAssetFiles != 1 {
		t.Fatalf("expected exactly 1 asset file (300/js/app.js) deleted, got %d", res.DeletedAssetFiles)
	}

	// 300/js/app.js must be gone
	if _, err := os.Stat(filepath.Join(tmpDir, "300", "js", "app.js")); !os.IsNotExist(err) {
		t.Fatalf("expected 300/js/app.js to be deleted, stat err: %v", err)
	}
	// assets/300/js/app.js (versioned) must be preserved!
	if _, err := os.Stat(filepath.Join(tmpDir, "assets", "300", "js", "app.js")); err != nil {
		t.Fatalf("expected versioned assets/300/js/app.js to be preserved, stat err: %v", err)
	}
}

func TestOrphanExtInVersionDirsPreviewAndVerifiedPrune(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('v');")
	mgr.SaveWithNamespace("gbf", "assets/100/js/app.js", headers, body)
	mgr.SaveWithNamespace("gbf", "assets/200/js/app.js", headers, body)
	mgr.SaveWithNamespace("gbf", "assets/300/js/app.js", headers, body)

	// Create an orphan .ext inside version 100
	orphanExtPath := filepath.Join(tmpDir, "assets", "100", "js", "newly_fetched.js.ext")
	orphanExtData := []byte(`{"ct":"application/javascript","cl":123}`)
	if err := os.WriteFile(orphanExtPath, orphanExtData, 0644); err != nil {
		t.Fatalf("WriteFile orphan .ext failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	prev2 := snap.RetentionPreviews["2"]
	// Version 100 has app.js + app.js.ext + newly_fetched.js.ext (2 meta files in version 100)
	if len(prev2.Groups) != 1 {
		t.Fatalf("expected 1 version group, got %d", len(prev2.Groups))
	}
	if prev2.EstimatedDeleteBytes < int64(len(body)+len(orphanExtData)) {
		t.Fatalf("expected RetentionPreview EstimatedDeleteBytes to include orphan .ext bytes, got %d", prev2.EstimatedDeleteBytes)
	}

	// Simulate a new data file arriving for newly_fetched.js after scan before prune
	pairedDataPath := filepath.Join(tmpDir, "assets", "100", "js", "newly_fetched.js")
	if err := os.WriteFile(pairedDataPath, []byte("fresh_data"), 0644); err != nil {
		t.Fatalf("WriteFile paired data failed: %v", err)
	}

	slimRes, err := mgr.PruneStaleVersionsDetailed(2, snap.SnapshotID, nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed failed: %v", err)
	}
	if slimRes.SkippedChangedCount < 1 {
		t.Fatalf("expected orphan .ext with newly created paired data file to be skipped, got %+v", slimRes)
	}
	// Both newly_fetched.js and newly_fetched.js.ext must still exist
	if _, err := os.Stat(pairedDataPath); err != nil {
		t.Fatalf("expected paired data file to survive: %v", err)
	}
	if _, err := os.Stat(orphanExtPath); err != nil {
		t.Fatalf("expected .ext paired with new data file to survive: %v", err)
	}
}

func TestVersionDirDiscoveryErrorsAndLateOldGenTempToken(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	mgr.SaveWithNamespace("gbf", "assets/100/js/app.js", headers, []byte("v100"))
	mgr.SaveWithNamespace("gbf", "assets/200/js/app.js", headers, []byte("v200"))

	// Create a regular file named "assets_en" where a directory is expected
	badPrefixPath := filepath.Join(tmpDir, "assets_en")
	if err := os.WriteFile(badPrefixPath, []byte("not_a_directory"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap.ReadErrors == 0 {
		t.Fatalf("expected ScanBreakdownWithProgress to record ReadErrors when assets_en is a regular file")
	}

	slimRes, err := mgr.PruneStaleVersionsDetailed(1, snap.SnapshotID, nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed returned unexpected error: %v", err)
	}
	if slimRes.FailedCount == 0 || slimRes.Status == "completed" {
		t.Fatalf("expected PruneStaleVersionsDetailed to report failure when assets_en is a regular file, got %+v", slimRes)
	}

	// Verify late old-generation temp token registration is rejected and never sweeps current-generation tokens
	curGen := mgr.currentGeneration()
	mgr.registerTempFileToken("tmp-current", opToken{
		canonKey: "gbf:assets/200/js/live.js",
		seq:      1,
		gen:      curGen,
	})
	if ok := mgr.registerTempFileToken("tmp-stale-gen", opToken{
		canonKey: "gbf:assets/200/js/old.js",
		seq:      1,
		gen:      curGen - 1,
	}); ok {
		t.Fatalf("expected registerTempFileToken with stale generation to be rejected")
	}
	mgr.tempTokensMu.Lock()
	_, curStillExists := mgr.tempTokens["tmp-current"]
	mgr.tempTokensMu.Unlock()
	if !curStillExists {
		t.Fatalf("expected current-generation temp token to remain intact after stale-gen registration attempt")
	}
}

func BenchmarkCacheHitWithToken(b *testing.B) {
	tmpDir := b.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('benchmark_payload');")
	mgr.Save("/assets/1728446123/js/bench.js", headers, body)
	// Promote to protected RAM
	_, _ = mgr.Get("/assets/1728446123/js/bench.js")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			item, _ := mgr.Get("/assets/1728446123/js/bench.js")
			if item == nil {
				b.Fatal("unexpected cache miss")
			}
		}
	})
}

func BenchmarkDiskHitWithTokenValidation(b *testing.B) {
	tmpDir := b.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('benchmark_disk_payload');")
	mgr.Save("/assets/1728446123/js/bench.js", headers, body)
	mgr.SetRAMEnabled(false)
	mgr.ClearRAM()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			item, src := mgr.GetMetadataWithNamespace("gbf", "/assets/1728446123/js/bench.js")
			if item == nil || src != "DISK" {
				b.Fatalf("expected DISK metadata hit, got item=%v src=%q", item, src)
			}
		}
	})
}

func BenchmarkNormalizeAndTokenCheck(b *testing.B) {
	tmpDir := b.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ns, key, ok := canonicalizeNSAndKey("gbf", "/assets/1728446123/js/bench.js")
			if !ok {
				b.Fatal("unexpected canonicalizeNSAndKey failure")
			}
			tok := mgr.acquireOpToken(ns, key)
			if !mgr.isOpTokenValid(tok) {
				b.Fatal("unexpected invalid token")
			}
		}
	})
}

func TestSpacePrefixedSegmentDistinctFromUnspaced(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	spacedBody := []byte("console.log('leading_space');")
	plainBody := []byte("console.log('plain_name');")

	if !mgr.SaveWithNamespace("gbf", "/assets/js/ app.js", headers, spacedBody) {
		t.Fatalf("expected SaveWithNamespace for '/assets/js/ app.js' to succeed")
	}
	if !mgr.SaveWithNamespace("gbf", "/assets/js/app.js", headers, plainBody) {
		t.Fatalf("expected SaveWithNamespace for '/assets/js/app.js' to succeed")
	}

	itemSpaced, _ := mgr.GetWithNamespace("gbf", "/assets/js/ app.js")
	itemPlain, _ := mgr.GetWithNamespace("gbf", "/assets/js/app.js")
	if itemSpaced == nil || !bytes.Equal(itemSpaced.Data, spacedBody) {
		t.Fatalf("expected '/assets/js/ app.js' to return spacedBody, got %v", itemSpaced)
	}
	if itemPlain == nil || !bytes.Equal(itemPlain.Data, plainBody) {
		t.Fatalf("expected '/assets/js/app.js' to return plainBody, got %v", itemPlain)
	}
}

func TestNBSPAndHostsPrefixCanonicalIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}

	// Part 1: Unicode NBSP (\u00a0) in segment
	nbspKey := "/assets/100/js/\u00a0app.js"
	if !mgr.SaveWithNamespace("gbf", nbspKey, headers, []byte("console.log('nbsp');")) {
		t.Fatalf("SaveWithNamespace with NBSP failed")
	}
	fNbsp, tmpNbsp, genNbsp, err := mgr.CreateDiskTempFile("gbf", nbspKey)
	if err != nil {
		t.Fatalf("CreateDiskTempFile with NBSP failed: %v", err)
	}
	_, _ = fNbsp.Write([]byte("console.log('stale_nbsp_temp');"))
	_ = fNbsp.Close()
	_, _, fileNbsp, genPersistNbsp, tokPersistNbsp, ok := mgr.saveRAMInternal("gbf", nbspKey, headers, []byte("console.log('stale_nbsp_persist');"), false, true)
	if !ok {
		t.Fatalf("saveRAMInternal with NBSP failed")
	}

	// Part 2: ("gbf", "/hosts/<subNS>/...") vs ("<subNS>", "...")
	hostsURL := "/hosts/sub.example.com/assets/100/js/app.js"
	if !mgr.SaveWithNamespace("gbf", hostsURL, headers, []byte("console.log('hosts_prefixed');")) {
		t.Fatalf("SaveWithNamespace with hosts/ prefix failed")
	}
	fHosts, tmpHosts, genHosts, err := mgr.CreateDiskTempFile("gbf", hostsURL)
	if err != nil {
		t.Fatalf("CreateDiskTempFile with hosts/ prefix failed: %v", err)
	}
	_, _ = fHosts.Write([]byte("console.log('stale_hosts_temp');"))
	_ = fHosts.Close()
	_, _, fileHosts, genPersistHosts, tokPersistHosts, ok := mgr.saveRAMInternal("gbf", hostsURL, headers, []byte("console.log('stale_hosts_persist');"), false, true)
	if !ok {
		t.Fatalf("saveRAMInternal with hosts/ prefix failed")
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	searchRes, err := mgr.SearchSnapshot(snap.SnapshotID, "app.js", 50, nil)
	if err != nil || len(searchRes.Items) != 2 {
		t.Fatalf("expected 2 items in search result, got %d (err=%v)", len(searchRes.Items), err)
	}

	var targets []DeleteTargetEntry
	for _, it := range searchRes.Items {
		targets = append(targets, DeleteTargetEntry{
			NS:  it.NS,
			Key: it.Key,
		})
	}

	delRes, err := mgr.DeleteSnapshotEntriesWithProgress(snap.SnapshotID, targets, nil, nil)
	if err != nil {
		t.Fatalf("DeleteSnapshotEntriesWithProgress failed: %v", err)
	}
	if delRes.DeletedAssetFiles != 2 {
		t.Fatalf("expected 2 deleted asset files, got %+v", delRes)
	}

	// Pre-deletion temp commits and async persist writes must all be rejected
	if _, committed := mgr.CommitDiskTempFile("gbf", nbspKey, headers, tmpNbsp, 31, genNbsp); committed {
		t.Fatalf("expected stale NBSP temp commit to be rejected after deletion")
	}
	if mgr.saveToDiskWithToken(fileNbsp, headers, []byte("console.log('stale_nbsp_persist');"), genPersistNbsp, tokPersistNbsp) {
		t.Fatalf("expected stale NBSP persist write to be rejected after deletion")
	}
	if _, committed := mgr.CommitDiskTempFile("gbf", hostsURL, headers, tmpHosts, 32, genHosts); committed {
		t.Fatalf("expected stale hosts-prefixed temp commit to be rejected after deletion")
	}
	if mgr.saveToDiskWithToken(fileHosts, headers, []byte("console.log('stale_hosts_persist');"), genPersistHosts, tokPersistHosts) {
		t.Fatalf("expected stale hosts-prefixed persist write to be rejected after deletion")
	}

	if item, _ := mgr.GetWithNamespace("gbf", nbspKey); item != nil {
		t.Fatalf("expected NBSP item to be absent after deletion, got %+v", item)
	}
	if item, _ := mgr.GetWithNamespace("gbf", hostsURL); item != nil {
		t.Fatalf("expected hosts-prefixed item to be absent after deletion, got %+v", item)
	}
	if item, _ := mgr.GetWithNamespace("sub.example.com", "/assets/100/js/app.js"); item != nil {
		t.Fatalf("expected sub.example.com item to be absent after deletion, got %+v", item)
	}

	// Verify reserved nested host namespaces ("hosts/gbf/..." and "hosts/hosts/...") are rejected by strict extraction
	if _, _, _, ok := extractStrictNamespaceAndKey(tmpDir, filepath.Join(tmpDir, "hosts", "gbf", "assets", "a.js")); ok {
		t.Fatalf("expected extractStrictNamespaceAndKey to reject hosts/gbf/...")
	}
	if _, _, _, ok := extractStrictNamespaceAndKey(tmpDir, filepath.Join(tmpDir, "hosts", "hosts", "assets", "a.js")); ok {
		t.Fatalf("expected extractStrictNamespaceAndKey to reject hosts/hosts/...")
	}
}

func TestAncestorReparseAndRootReplacementRejected(t *testing.T) {
	parentDir := t.TempDir()
	cacheRoot := filepath.Join(parentDir, "cache_root")
	if err := os.MkdirAll(cacheRoot, 0755); err != nil {
		t.Fatalf("MkdirAll cacheRoot failed: %v", err)
	}
	mgr := NewManager(cacheRoot, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	payload := []byte("console.log('v100');")
	mgr.SaveWithNamespace("gbf", "assets/100/js/victim.js", headers, payload)
	mgr.SaveWithNamespace("gbf", "assets/200/js/keep.js", headers, payload)

	origFi, err := os.Stat(filepath.Join(cacheRoot, "assets", "100", "js", "victim.js"))
	if err != nil {
		t.Fatalf("Stat victim.js failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	// 1. Replace intermediate directory assets/100/js with a symlink/junction to externalDir containing an identical file
	externalDir := filepath.Join(parentDir, "external_target")
	if err := os.MkdirAll(externalDir, 0755); err != nil {
		t.Fatalf("MkdirAll externalDir failed: %v", err)
	}
	extVictim := filepath.Join(externalDir, "victim.js")
	if err := os.WriteFile(extVictim, payload, 0644); err != nil {
		t.Fatalf("WriteFile extVictim failed: %v", err)
	}
	_ = os.Chtimes(extVictim, origFi.ModTime(), origFi.ModTime())

	jsDir := filepath.Join(cacheRoot, "assets", "100", "js")
	if err := os.RemoveAll(jsDir); err != nil {
		t.Fatalf("RemoveAll jsDir failed: %v", err)
	}

	linkCreated := false
	if err := os.Symlink(externalDir, jsDir); err == nil {
		linkCreated = true
	} else if runtime.GOOS == "windows" {
		if out, jErr := exec.Command("cmd", "/c", "mklink", "/J", jsDir, externalDir).CombinedOutput(); jErr == nil {
			linkCreated = true
		} else {
			t.Logf("skipping symlink/junction check (unable to create link: %v, %s)", jErr, string(out))
		}
	}

	if linkCreated {
		delRes, err := mgr.DeleteSnapshotEntriesWithProgress(snap.SnapshotID, []DeleteTargetEntry{
			{NS: "gbf", Key: "assets/100/js/victim.js"},
		}, nil, nil)
		if err != nil {
			t.Fatalf("DeleteSnapshotEntriesWithProgress returned unexpected error: %v", err)
		}
		if delRes.FailedCount == 0 {
			t.Fatalf("expected DeleteSnapshotEntriesWithProgress to fail closed on symlinked ancestor directory")
		}
		if _, err := os.Stat(extVictim); err != nil {
			t.Fatalf("external victim.js was deleted across symlinked ancestor: %v", err)
		}
		_ = os.Remove(jsDir)
	}

	// 2. Replace cacheRoot directory at the same path and verify root identity check rejects deletion/prune
	backupRoot := filepath.Join(parentDir, "cache_root_old")
	if err := os.Rename(cacheRoot, backupRoot); err != nil {
		t.Fatalf("Rename cacheRoot failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cacheRoot, "assets", "100", "js"), 0755); err != nil {
		t.Fatalf("MkdirAll new cacheRoot failed: %v", err)
	}
	newVictim := filepath.Join(cacheRoot, "assets", "100", "js", "victim.js")
	if err := os.WriteFile(newVictim, payload, 0644); err != nil {
		t.Fatalf("WriteFile newVictim failed: %v", err)
	}
	_ = os.Chtimes(newVictim, origFi.ModTime(), origFi.ModTime())

	delRes2, err := mgr.DeleteSnapshotEntriesWithProgress(snap.SnapshotID, []DeleteTargetEntry{
		{NS: "gbf", Key: "assets/100/js/victim.js"},
	}, nil, nil)
	if err != nil {
		t.Fatalf("DeleteSnapshotEntriesWithProgress returned unexpected error: %v", err)
	}
	if delRes2 == nil || delRes2.Status != "failed" || delRes2.FailedCount == 0 {
		t.Fatalf("expected DeleteSnapshotEntriesWithProgress to fail closed on replaced cache root directory, got %+v", delRes2)
	}
	slimRes, err := mgr.PruneStaleVersionsDetailed(1, snap.SnapshotID, nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed returned unexpected error: %v", err)
	}
	if slimRes == nil || slimRes.Status != "failed" || slimRes.FailedCount == 0 {
		t.Fatalf("expected PruneStaleVersionsDetailed to fail closed on replaced cache root directory, got %+v", slimRes)
	}
	if _, err := os.Stat(newVictim); err != nil {
		t.Fatalf("expected newVictim in replaced root directory to remain untouched: %v", err)
	}
}

func TestConcurrentOrphanExtPruneAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('concurrent_ext_test');")

	for iter := 0; iter < 15; iter++ {
		mgr.SaveWithNamespace("gbf", "assets/100/js/base.js", headers, body)
		mgr.SaveWithNamespace("gbf", "assets/200/js/base.js", headers, body)

		raceDataPath := filepath.Join(tmpDir, "assets", "100", "js", "race.js")
		raceExtPath := raceDataPath + ".ext"
		_ = os.Remove(raceDataPath)
		if err := os.WriteFile(raceExtPath, []byte(`{"ct":"application/javascript","v":1}`), 0644); err != nil {
			t.Fatalf("WriteFile orphan .ext failed: %v", err)
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot on iter %d", iter)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = mgr.PruneStaleVersionsDetailed(1, snap.SnapshotID, nil, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			mgr.SaveWithNamespace("gbf", "assets/100/js/race.js", headers, body)
		}()
		close(start)
		wg.Wait()

		if _, dataErr := os.Stat(raceDataPath); dataErr == nil {
			if _, extErr := os.Stat(raceExtPath); extErr != nil {
				t.Fatalf("iter %d: race.js exists after concurrent Save + Prune, but race.js.ext was deleted: %v", iter, extErr)
			}
		}
	}
}

