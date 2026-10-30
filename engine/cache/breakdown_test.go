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
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatal(err)
	}

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
			ns := "gbf"
			if strings.HasPrefix(relPath, "hosts/") {
				parts := strings.SplitN(relPath, "/", 3)
				if len(parts) >= 2 {
					ns = parts[1]
				}
			}
			_ = writeDiskExtFile(full, diskMeta{
				ContentType: "application/octet-stream",
				Namespace:   ns,
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
	writeAsset("hosts/sub.example.com/assets/img/sp/ui/data.png", []byte("\x89PNG\r\n\x1a\nhostui"), false)

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
	expectedExcludedBytes := int64(len("inflight") + len("quarantined"))
	if snap.ExcludedBytes != expectedExcludedBytes {
		t.Fatalf("expected %d excluded bytes, got %d", expectedExcludedBytes, snap.ExcludedBytes)
	}

	// Verify independent exclusion of both temp file and quarantine file:
	// Neither must appear as a managed asset in entryByNSKey or Entries, but both must remain intact on disk.
	if _, exists := snap.entryByNSKey[makeNSKey("gbf", "assets/img/sp/writing.png.tmp.123.456.1")]; exists {
		t.Fatalf("temporary .tmp. file must not be indexed in snapshot entries")
	}
	if _, exists := snap.entryByNSKey[makeNSKey("gbf", "assets/js/set-error-handler.js.quarantine.999")]; exists {
		t.Fatalf("quarantine file must not be indexed in snapshot entries")
	}
	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("temporary file %s must remain on disk intact: %v", tmpFile, err)
	}
	if _, err := os.Stat(quarFile); err != nil {
		t.Fatalf("quarantine file %s must remain on disk intact: %v", quarFile, err)
	}

	// Check default retention preview for keep=8 and computeRetentionPreview for keep=2:
	// gbf:assets has 3 versions (300, 200, 100) -> keep=2 keeps [300, 200], prunes [100] (1 dir, 1 asset file, 1 meta file)
	// gbf:assets_en has 2 versions (20, 10) -> keeps [20, 10], prunes []
	// sub.example.com:assets has 1 version (500) -> keeps [500], prunes []
	prev8, ok := snap.RetentionPreviews["8"]
	if !ok || prev8.Keep != 8 || prev8.TotalVersionDirs != 6 {
		t.Fatalf("missing or unexpected default retention preview for keep=8: %+v", snap.RetentionPreviews)
	}
	prev2 := ComputeVersionRetentionPreview(snap.VersionGroups, 2)
	if prev2.PruneDirs != 1 || prev2.PruneAssetFiles != 1 || prev2.PruneMetadataFiles != 1 {
		t.Fatalf("unexpected keep=2 preview: %+v", prev2)
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

	// Clearing core_ui category should delete both physical files of dual.png (primary + fallback alias)
	delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryCoreUI}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress(core_ui) failed: %v", err)
	}
	if delRes.DeletedAssetFiles != 2 || delRes.DeletedMetaFiles != 2 {
		t.Fatalf("expected 2 asset files and 2 meta files deleted for dual alias, got %+v", delRes)
	}

	// Snapshot must now be marked stale
	latestSnap, _ := mgr.GetBreakdownSnapshot()
	if latestSnap == nil || latestSnap.Valid || !latestSnap.Stale {
		t.Fatalf("expected snapshot to be marked stale after deletion, got %+v", latestSnap)
	}

	// Re-scan: let's scan and then modify s1.png to test category cleanup skipping changed files
	snap2 := mgr.ScanBreakdownWithProgress(nil, nil)
	storyPath, _ := mgr.ResolvePathWithNamespace("gbf", "assets/img/sp/quest/scene/s1.png")
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

	// 2. Create 1024 simultaneous active temp transactions to reach capacity in tempTokens
	txs := make([]tx, 0, maxActiveTempTokens)
	for i := 0; i < maxActiveTempTokens; i++ {
		k := fmt.Sprintf("/assets/js/overflow_%d.js", i)
		f, tp, g, err := mgr.CreateDiskTempFile("gbf", k)
		if err != nil {
			t.Fatalf("CreateDiskTempFile failed at %d: %v", i, err)
		}
		_, _ = f.Write(validJS)
		_ = f.Close()
		txs = append(txs, tx{key: k, tmpPath: tp, gen: g})
	}

	// Verify the 1025th simultaneous active temp file is safely rejected without evicting any existing active token
	overflowKey := "/assets/js/overflow_1024.js"
	if fOverflow, tpOverflow, _, err := mgr.CreateDiskTempFile("gbf", overflowKey); err == nil {
		_ = fOverflow.Close()
		mgr.DiscardDiskTempFile(tpOverflow)
		t.Fatalf("expected 1025th simultaneous CreateDiskTempFile to fail when all 1024 tokens are active")
	}

	mgr.tempTokensMu.Lock()
	for i := range txs {
		if _, exists := mgr.tempTokens[filepath.Clean(txs[i].tmpPath)]; !exists {
			mgr.tempTokensMu.Unlock()
			t.Fatalf("expected active temp token %d (%s) to be preserved at capacity", i, txs[i].tmpPath)
		}
	}
	mgr.tempTokensMu.Unlock()

	// Attempting CommitDiskTempFile on a temp file without a registered token MUST fail closed and remove the temp file
	unregisteredTmp := filepath.Join(tmpDir, "assets", "js", "unregistered.js.tmp.1.1.1")
	if err := os.WriteFile(unregisteredTmp, validJS, 0644); err != nil {
		t.Fatalf("WriteFile unregisteredTmp failed: %v", err)
	}
	mgr.Invalidate("gbf", overflowKey)
	if item, committed := mgr.CommitDiskTempFile("gbf", overflowKey, headers, unregisteredTmp, int64(len(validJS)), txs[0].gen); committed || item != nil {
		t.Fatalf("expected CommitDiskTempFile to reject missing-token transaction for %s", overflowKey)
	}
	if _, err := os.Stat(unregisteredTmp); !os.IsNotExist(err) {
		t.Fatalf("expected unregistered temp file to be removed on rejected commit, got err=%v", err)
	}
	if got, _ := mgr.GetWithNamespace("gbf", overflowKey); got != nil {
		t.Fatalf("stale payload was written to cache after missing-token commit for %s", overflowKey)
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

	// Concurrent snapshot reader worker
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stopCh:
				return
			default:
				_, _ = mgr.GetBreakdownSnapshot()
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

	// 1. Create 3 versions where newest version 300 contains confirmed asset and an inflight .tmp. file
	jsHeaders := map[string]string{"content-type": "application/javascript"}
	jsBody := []byte("console.log('v');")
	mgr.SaveWithNamespace("gbf", "assets/100/js/app.js", jsHeaders, jsBody)
	mgr.SaveWithNamespace("gbf", "assets/200/js/app.js", jsHeaders, jsBody)
	mgr.SaveWithNamespace("gbf", "assets/300/js/app.js", jsHeaders, jsBody)
	ver300Dir := filepath.Join(tmpDir, "assets", "300", "js")
	if err := os.WriteFile(filepath.Join(ver300Dir, "app.js.tmp.1.2.3"), []byte("partial"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	previewKeep2 := ComputeVersionRetentionPreview(snap.VersionGroups, 2)
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
		expectedSub := "cache root must be an ordinary directory, not a symlink or file"
		if !strings.Contains(err.Error(), expectedSub) {
			t.Fatalf("expected ValidateDestructiveCacheDir error containing %q, got: %v", expectedSub, err)
		}
	} else {
		if badRes == nil || badRes.Status != "failed" || badRes.FailedCount == 0 || len(badRes.Failures) == 0 {
			t.Fatalf("expected Status=failed and FailedCount>0 when cache base is a regular file, got %+v", badRes)
		}
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
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatalf("EnsureRootMarker failed: %v", err)
	}
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	// Write legacy unprefixed path 300/js/app.js (classified as CategoryOther)
	// SaveWithNamespace produces valid Tier-1 .ext sidecar with matching size, mtimeNano, and ns: "gbf"
	if !mgr.SaveWithNamespace("gbf", "300/js/app.js", headers, []byte("legacy_other")) {
		t.Fatalf("SaveWithNamespace 300/js/app.js failed")
	}
	// Write standard versioned path assets/300/js/app.js (classified as CategoryVersioned)
	if !mgr.SaveWithNamespace("gbf", "assets/300/js/app.js", headers, []byte("versioned_asset")) {
		t.Fatalf("SaveWithNamespace assets/300/js/app.js failed")
	}

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

	// 300/js/app.js must be gone along with its paired .ext metadata
	if _, err := os.Stat(filepath.Join(tmpDir, "300", "js", "app.js")); !os.IsNotExist(err) {
		t.Fatalf("expected 300/js/app.js to be deleted, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "300", "js", "app.js.ext")); !os.IsNotExist(err) {
		t.Fatalf("expected 300/js/app.js.ext to be deleted, stat err: %v", err)
	}

	// assets/300/js/app.js (versioned) and its .ext sidecar must be preserved!
	if _, err := os.Stat(filepath.Join(tmpDir, "assets", "300", "js", "app.js")); err != nil {
		t.Fatalf("expected versioned assets/300/js/app.js to be preserved, stat err: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "assets", "300", "js", "app.js.ext")); err != nil {
		t.Fatalf("expected versioned assets/300/js/app.js.ext to be preserved, stat err: %v", err)
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

	orphanExtPath := filepath.Join(tmpDir, "assets", "100", "js", "newly_fetched.js.ext")
	orphanExtData := []byte(`{"ct":"application/javascript","cl":123}`)
	if err := os.WriteFile(orphanExtPath, orphanExtData, 0644); err != nil {
		t.Fatalf("WriteFile orphan .ext failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot, got %+v", snap)
	}
	preview := ComputeVersionRetentionPreview(snap.VersionGroups, 2)
	if len(preview.Groups) != 1 {
		t.Fatalf("expected one stale version group, got %d", len(preview.Groups))
	}
	pairedMetaInfo, err := os.Stat(filepath.Join(tmpDir, "assets", "100", "js", "app.js.ext"))
	if err != nil {
		t.Fatal(err)
	}
	expectedDeleteBytes := int64(len(body)) + pairedMetaInfo.Size()
	if preview.EstimatedDeleteBytes != expectedDeleteBytes {
		t.Fatalf("preview should count only confirmed asset and paired metadata: got %d, want %d", preview.EstimatedDeleteBytes, expectedDeleteBytes)
	}

	// Files introduced after the snapshot and orphan metadata must never be deleted.
	lateData := filepath.Join(tmpDir, "assets", "100", "js", "late.js")
	if err := os.WriteFile(lateData, []byte("new user data"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.PruneStaleVersionsDetailed(2, snap.SnapshotID, nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed failed: %v", err)
	}
	if res.SkippedChangedCount != 0 {
		t.Fatalf("orphan ext must not enter deletion candidates: %+v", res)
	}
	for _, keep := range []string{lateData, orphanExtPath} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected %s to survive: %v", keep, err)
		}
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
	if snap.ReadErrors == 0 && snap.SkippedCount == 0 {
		t.Fatalf("expected ScanBreakdownWithProgress to skip unconfirmed file or record ReadErrors when assets_en is a regular file")
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
			item, src := mgr.GetWithNamespace("gbf", "/assets/1728446123/js/bench.js")
			if item == nil || src != "DISK" {
				b.Fatalf("expected DISK hit, got item=%v src=%q", item, src)
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

	// Part 1: Unicode NBSP (\u00a0) in segment (non-versioned path classified as CategoryOther)
	nbspKey := "/assets/js/\u00a0app.js"
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
	// Form A: gbf hosts prefix entrypoint ("gbf", "/hosts/sub.example.com/assets/js/app.js")
	hostsURL := "/hosts/sub.example.com/assets/js/app.js"
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

	// Form B: direct host namespace entrypoint ("sub.example.com", "assets/js/direct.js")
	directKey := "assets/js/direct.js"
	if !mgr.SaveWithNamespace("sub.example.com", directKey, headers, []byte("console.log('direct_host');")) {
		t.Fatalf("SaveWithNamespace for direct host namespace failed")
	}
	fDirect, tmpDirect, genDirect, err := mgr.CreateDiskTempFile("sub.example.com", directKey)
	if err != nil {
		t.Fatalf("CreateDiskTempFile for direct host namespace failed: %v", err)
	}
	_, _ = fDirect.Write([]byte("console.log('stale_direct_temp');"))
	_ = fDirect.Close()
	_, _, fileDirect, genPersistDirect, tokPersistDirect, ok := mgr.saveRAMInternal("sub.example.com", directKey, headers, []byte("console.log('stale_direct_persist');"), false, true)
	if !ok {
		t.Fatalf("saveRAMInternal for direct host namespace failed")
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, []string{"gbf", "sub.example.com"}, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
	}
	if delRes.DeletedAssetFiles != 3 {
		t.Fatalf("expected 3 deleted asset files (nbsp + hostsURL + directKey), got %+v", delRes)
	}

	// Pre-deletion temp commits and async persist writes must all be rejected for both entry forms
	if _, committed := mgr.CommitDiskTempFile("gbf", nbspKey, headers, tmpNbsp, 31, genNbsp); committed {
		t.Fatalf("expected stale NBSP temp commit to be rejected after deletion")
	}
	if mgr.saveToDiskWithToken(fileNbsp, headers, []byte("console.log('stale_nbsp_persist');"), genPersistNbsp, tokPersistNbsp) {
		t.Fatalf("expected stale NBSP persist write to be rejected after deletion")
	}
	// Form A rejection check
	if _, committed := mgr.CommitDiskTempFile("gbf", hostsURL, headers, tmpHosts, 32, genHosts); committed {
		t.Fatalf("expected stale hosts-prefixed temp commit to be rejected after deletion")
	}
	if mgr.saveToDiskWithToken(fileHosts, headers, []byte("console.log('stale_hosts_persist');"), genPersistHosts, tokPersistHosts) {
		t.Fatalf("expected stale hosts-prefixed persist write to be rejected after deletion")
	}
	// Form B rejection check
	if _, committed := mgr.CommitDiskTempFile("sub.example.com", directKey, headers, tmpDirect, 33, genDirect); committed {
		t.Fatalf("expected stale direct host temp commit to be rejected after deletion")
	}
	if mgr.saveToDiskWithToken(fileDirect, headers, []byte("console.log('stale_direct_persist');"), genPersistDirect, tokPersistDirect) {
		t.Fatalf("expected stale direct host persist write to be rejected after deletion")
	}

	if item, _ := mgr.GetWithNamespace("gbf", nbspKey); item != nil {
		t.Fatalf("expected NBSP item to be absent after deletion, got %+v", item)
	}
	if item, _ := mgr.GetWithNamespace("gbf", hostsURL); item != nil {
		t.Fatalf("expected hosts-prefixed item to be absent after deletion, got %+v", item)
	}
	if item, _ := mgr.GetWithNamespace("sub.example.com", directKey); item != nil {
		t.Fatalf("expected direct host item to be absent after deletion, got %+v", item)
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
	mgr.SaveWithNamespace("gbf", "assets/js/victim.js", headers, payload)
	mgr.SaveWithNamespace("gbf", "assets/100/js/old_ver.js", headers, payload)
	mgr.SaveWithNamespace("gbf", "assets/200/js/keep.js", headers, payload)

	origFi, err := os.Stat(filepath.Join(cacheRoot, "assets", "js", "victim.js"))
	if err != nil {
		t.Fatalf("Stat victim.js failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	// 1. Replace intermediate directory assets/js with a symlink/junction to externalDir containing an identical file
	externalDir := filepath.Join(parentDir, "external_target")
	if err := os.MkdirAll(externalDir, 0755); err != nil {
		t.Fatalf("MkdirAll externalDir failed: %v", err)
	}
	extVictim := filepath.Join(externalDir, "victim.js")
	if err := os.WriteFile(extVictim, payload, 0644); err != nil {
		t.Fatalf("WriteFile extVictim failed: %v", err)
	}
	_ = os.Chtimes(extVictim, origFi.ModTime(), origFi.ModTime())

	jsDir := filepath.Join(cacheRoot, "assets", "js")
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
		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress returned unexpected error: %v", err)
		}
		if delRes.FailedCount == 0 {
			t.Fatalf("expected ClearCategoriesWithProgress to fail closed on symlinked ancestor directory")
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
	if err := os.MkdirAll(filepath.Join(cacheRoot, "assets", "js"), 0755); err != nil {
		t.Fatalf("MkdirAll new cacheRoot failed: %v", err)
	}
	newVictim := filepath.Join(cacheRoot, "assets", "js", "victim.js")
	if err := os.WriteFile(newVictim, payload, 0644); err != nil {
		t.Fatalf("WriteFile newVictim failed: %v", err)
	}
	_ = os.Chtimes(newVictim, origFi.ModTime(), origFi.ModTime())

	delRes2, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress returned unexpected error: %v", err)
	}
	if delRes2 == nil || delRes2.Status != "failed" || delRes2.FailedCount == 0 {
		t.Fatalf("expected ClearCategoriesWithProgress to fail closed on replaced cache root directory, got %+v", delRes2)
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

func TestAsyncSaveHostsPrefixAndCrossNamespaceIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	bodyCDN1 := []byte("console.log('from_cdn1_hosts_path');")
	bodyCDN2 := []byte("console.log('from_cdn2_direct_path');")

	// 1. Save via SaveRAMWithNamespace on external host with a "/hosts/cdn2.example.com/..." URL path
	if _, ok := mgr.SaveRAMWithNamespace("cdn1.example.com", "/hosts/cdn2.example.com/assets/100/js/app.js", headers, bodyCDN1); !ok {
		t.Fatalf("SaveRAMWithNamespace for cdn1.example.com failed")
	}
	// 2. Save via SavePrefetchWithNamespace on cdn2.example.com with "/assets/100/js/app.js"
	if _, ok := mgr.SavePrefetchWithNamespace("cdn2.example.com", "/assets/100/js/app.js", headers, bodyCDN2); !ok {
		t.Fatalf("SavePrefetchWithNamespace for cdn2.example.com failed")
	}

	// Verify RAM isolation
	item1, _ := mgr.GetWithNamespace("cdn1.example.com", "/hosts/cdn2.example.com/assets/100/js/app.js")
	item2, _ := mgr.GetWithNamespace("cdn2.example.com", "/assets/100/js/app.js")
	if item1 == nil || !bytes.Equal(item1.Data, bodyCDN1) {
		t.Fatalf("expected cdn1.example.com to return bodyCDN1, got %v", item1)
	}
	if item2 == nil || !bytes.Equal(item2.Data, bodyCDN2) {
		t.Fatalf("expected cdn2.example.com to return bodyCDN2, got %v", item2)
	}

	// 3. Test GBF "/hosts/cdn2.example.com/..." vs external namespace "cdn2.example.com" "/..."
	bodyGBFHosts := []byte("console.log('from_gbf_hosts_path');")
	if _, ok := mgr.SaveRAMWithNamespace("gbf", "/hosts/cdn2.example.com/assets/100/js/shared.js", headers, bodyGBFHosts); !ok {
		t.Fatalf("SaveRAMWithNamespace for gbf /hosts/... failed")
	}

	// External namespace "cdn2.example.com" must NOT hit GBF's "/hosts/cdn2.example.com/assets/100/js/shared.js" in RAM or Disk
	if crossItem, _ := mgr.GetWithNamespace("cdn2.example.com", "/assets/100/js/shared.js"); crossItem != nil {
		t.Fatalf("expected cdn2.example.com miss before saving its own copy, got %+v", crossItem)
	}
	if mgr.HasCacheWithNamespace("cdn2.example.com", "/assets/100/js/shared.js") {
		t.Fatalf("expected HasCacheWithNamespace false for cdn2.example.com before saving its own copy")
	}

	// Wait for async disk persistence to complete, disable RAM, and verify disk ownership check rejects cross-namespace read
	deadline := time.Now().Add(2 * time.Second)
	sharedPath := filepath.Join(tmpDir, "hosts", "cdn2.example.com", "assets", "100", "js", "shared.js")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sharedPath + ".ext"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.SetRAMEnabled(false)
	mgr.ClearRAM()

	if gbfDiskItem, _ := mgr.GetWithNamespace("gbf", "/hosts/cdn2.example.com/assets/100/js/shared.js"); gbfDiskItem == nil || !bytes.Equal(gbfDiskItem.Data, bodyGBFHosts) {
		t.Fatalf("expected gbf disk hit for /hosts/cdn2.example.com/..., got %v", gbfDiskItem)
	}
	if crossDiskItem, _ := mgr.GetWithNamespace("cdn2.example.com", "/assets/100/js/shared.js"); crossDiskItem != nil {
		t.Fatalf("expected cdn2.example.com disk lookup to reject file owned by gbf namespace, got %+v", crossDiskItem)
	}
	if crossMeta, _ := mgr.VerifyAndGetMetadataWithNamespace("cdn2.example.com", "/assets/100/js/shared.js"); crossMeta != nil {
		t.Fatalf("expected cdn2.example.com VerifyAndGetMetadataWithNamespace to reject file owned by gbf namespace, got %+v", crossMeta)
	}
}

func TestPinnedParentRootPreventsCheckToUnlinkSwap(t *testing.T) {
	parentDir := t.TempDir()
	cacheRoot := filepath.Join(parentDir, "cache_root")
	if err := os.MkdirAll(cacheRoot, 0755); err != nil {
		t.Fatalf("MkdirAll cacheRoot failed: %v", err)
	}
	mgr := NewManager(cacheRoot, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	payload := []byte("console.log('pinned_unlink_test');")
	if !mgr.SaveWithNamespace("gbf", "assets/js/target.js", headers, payload) {
		t.Fatalf("SaveWithNamespace failed")
	}

	origPath := filepath.Join(cacheRoot, "assets", "js", "target.js")
	origFi, err := os.Stat(origPath)
	if err != nil {
		t.Fatalf("Stat origPath failed: %v", err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	// Prepare a replacement directory tree that will be swapped into the path string right before parentRoot.Remove()
	swappedOutRoot := filepath.Join(parentDir, "cache_root_moved")
	replacementRoot := filepath.Join(parentDir, "cache_root_replacement")
	replacementFile := filepath.Join(replacementRoot, "assets", "js", "target.js")
	if err := os.MkdirAll(filepath.Dir(replacementFile), 0755); err != nil {
		t.Fatalf("MkdirAll replacement dir failed: %v", err)
	}
	if err := os.WriteFile(replacementFile, payload, 0644); err != nil {
		t.Fatalf("WriteFile replacementFile failed: %v", err)
	}
	_ = os.Chtimes(replacementFile, origFi.ModTime(), origFi.ModTime())

	swapped := false
	testBeforeUnlinkHook = func(targetPath string) {
		if !swapped && strings.HasSuffix(targetPath, "target.js") {
			// Attempt to rename cacheRoot -> swappedOutRoot and replacementRoot -> cacheRoot right before unlink!
			// On POSIX this succeeds even while parentRoot fd is open; on Windows os.Rename may fail with sharing violation because parentRoot holds the directory handle open (which itself blocks the swap!).
			if err := os.Rename(cacheRoot, swappedOutRoot); err == nil {
				_ = os.Rename(replacementRoot, cacheRoot)
				swapped = true
			}
		}
	}
	defer func() {
		testBeforeUnlinkHook = nil
	}()

	_, err = mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
	}

	// Regardless of whether the OS allowed renaming the open parent directory (POSIX) or blocked the rename via handle sharing (Windows),
	// the victim file in the replacement tree must NEVER be deleted!
	victimPath := replacementFile
	if swapped {
		victimPath = filepath.Join(cacheRoot, "assets", "js", "target.js")
	}
	if _, err := os.Stat(victimPath); err != nil {
		t.Fatalf("victim file in replacement tree was deleted across check-to-unlink window (swapped=%v): %v", swapped, err)
	}
}

func TestEpochMTimeOrphanExtModificationDetected(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('version_keep');")
	mgr.SaveWithNamespace("gbf", "assets/100/js/old.js", headers, body)
	mgr.SaveWithNamespace("gbf", "assets/200/js/new.js", headers, body)

	orphanExt := filepath.Join(tmpDir, "assets", "100", "js", "epoch_orphan.js.ext")
	if err := os.WriteFile(orphanExt, []byte(`{"ct":"application/javascript","v":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(0, 0)
	if err := os.Chtimes(orphanExt, epoch, epoch); err != nil {
		t.Fatal(err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}
	newTime := time.Unix(1700000100, 0)
	if err := os.Chtimes(orphanExt, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	res, err := mgr.PruneStaleVersionsDetailed(1, snap.SnapshotID, nil, nil)
	if err != nil {
		t.Fatalf("PruneStaleVersionsDetailed failed: %v", err)
	}
	if res.SkippedChangedCount != 0 {
		t.Fatalf("orphan sidecar should never enter deletion set: %+v", res)
	}
	if _, err := os.Stat(orphanExt); err != nil {
		t.Fatalf("orphan .ext should remain: %v", err)
	}
}

func TestConcurrentEmptyDirPruneAndTempFileSave(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('concurrent_dir_prune_save');")

	for iter := 0; iter < 15; iter++ {
		if !mgr.SaveWithNamespace("gbf", "assets/img/sp/quest/scene/old_item.js", headers, body) {
			t.Fatalf("iter %d: initial SaveWithNamespace failed", iter)
		}
		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("iter %d: expected valid snapshot", iter)
		}

		var wg sync.WaitGroup
		start := make(chan struct{})
		var createErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _ = mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryStory}, nil, nil, nil)
		}()
		go func() {
			defer wg.Done()
			<-start
			f, tmpPath, gen, err := mgr.CreateDiskTempFile("gbf", "assets/img/sp/quest/scene/new_item.js")
			if err != nil {
				createErr = err
				return
			}
			_, _ = f.Write(body)
			_ = f.Close()
			_, _ = mgr.CommitDiskTempFile("gbf", "assets/img/sp/quest/scene/new_item.js", headers, tmpPath, int64(len(body)), gen)
		}()
		close(start)
		wg.Wait()

		if createErr != nil {
			t.Fatalf("iter %d: CreateDiskTempFile raced with empty directory cleanup: %v", iter, createErr)
		}
	}
}

func TestBoostPrewarmAndWarmupNamespaceIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	writer := NewManager(tmpDir, 32)
	headers := map[string]string{"content-type": "application/javascript"}
	extBody := []byte("console.log('ext_cdn_boost_payload');")
	gbfHostsBody := []byte("console.log('gbf_hosts_boost_payload');")

	if !writer.SaveWithNamespace("cdn.example.com", "assets/js/app.js", headers, extBody) {
		t.Fatalf("SaveWithNamespace cdn.example.com failed")
	}
	if !writer.SaveWithNamespace("gbf", "hosts/cdn.example.com/assets/js/gbf_only.js", headers, gbfHostsBody) {
		t.Fatalf("SaveWithNamespace gbf hosts/... failed")
	}
	writer.Close()

	reader := NewManager(tmpDir, 32)
	defer reader.Close()
	if loaded := reader.Warmup(100); loaded != 2 {
		t.Fatalf("expected Warmup to load 2 items, got %d", loaded)
	}

	prog := reader.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if prog.State != BoostStateCompleted || !prog.Done {
		t.Fatalf("PrewarmBoostPoolWithProgress unexpected state: %+v", prog)
	}

	item, src := reader.GetWithNamespace("cdn.example.com", "assets/js/app.js")
	if item == nil {
		t.Fatalf("expected cdn.example.com/assets/js/app.js to hit after PrewarmBoostPool")
	}
	if src != "RAM-BOOST" {
		t.Fatalf("expected source RAM-BOOST for cdn.example.com/assets/js/app.js, got %q", src)
	}
	if !bytes.Equal(item.Data, extBody) {
		t.Fatalf("unexpected payload for cdn.example.com: %q", string(item.Data))
	}

	if crossItem, _ := reader.GetWithNamespace("gbf", "hosts/cdn.example.com/assets/js/app.js"); crossItem != nil {
		t.Fatalf("expected gbf lookup on external namespace file to miss")
	}

	gbfItem, gbfSrc := reader.GetWithNamespace("gbf", "hosts/cdn.example.com/assets/js/gbf_only.js")
	if gbfItem == nil {
		t.Fatalf("expected gbf hosts/cdn.example.com/assets/js/gbf_only.js to hit")
	}
	if gbfSrc != "RAM-BOOST" {
		t.Fatalf("expected source RAM-BOOST for gbf hosts/... item, got %q", gbfSrc)
	}
	if !bytes.Equal(gbfItem.Data, gbfHostsBody) {
		t.Fatalf("unexpected payload for gbf hosts/...: %q", string(gbfItem.Data))
	}

	if crossExtItem, _ := reader.GetWithNamespace("cdn.example.com", "assets/js/gbf_only.js"); crossExtItem != nil {
		t.Fatalf("expected external namespace lookup on gbf-owned hosts/... file to miss")
	}
}

func TestConcurrentDiskOnlyCrossNamespaceCollisionRace(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()
	mgr.SetRAMEnabled(false)

	headers := map[string]string{"content-type": "application/javascript"}
	// Identical byte length to maximize collision potential if ownership checks race
	gbfBody := []byte("console.log('GBF_OWNED_DATA!!');")
	extBody := []byte("console.log('EXT_OWNED_DATA!!');")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan string, 16)

	reportErr := func(msg string) {
		select {
		case errCh <- msg:
		default:
		}
	}

	wg.Add(4)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				mgr.SaveWithNamespace("gbf", "hosts/cdn.example.com/race.js", headers, gbfBody)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				mgr.SaveWithNamespace("cdn.example.com", "race.js", headers, extBody)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if item, _ := mgr.GetWithNamespace("gbf", "hosts/cdn.example.com/race.js"); item != nil {
					if !bytes.Equal(item.Data, gbfBody) {
						reportErr("gbf reader received non-gbf payload: " + string(item.Data))
						return
					}
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if item, _ := mgr.GetWithNamespace("cdn.example.com", "race.js"); item != nil {
					if !bytes.Equal(item.Data, extBody) {
						reportErr("cdn.example.com reader received non-ext payload: " + string(item.Data))
						return
					}
				}
			}
		}
	}()

	time.Sleep(180 * time.Millisecond)
	close(stop)
	wg.Wait()

	select {
	case msg := <-errCh:
		t.Fatal(msg)
	default:
	}
}

func TestDeleteSnapshotSkipsModifiedLingeringExtWhenDataMissing(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('lingering_ext_test');")
	if !mgr.SaveWithNamespace("gbf", "assets/js/lingering.js", headers, body) {
		t.Fatalf("SaveWithNamespace failed")
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid snapshot")
	}

	dataPath := filepath.Join(tmpDir, "assets", "js", "lingering.js")
	extPath := dataPath + ".ext"
	if err := os.Remove(dataPath); err != nil {
		t.Fatalf("Remove dataPath failed: %v", err)
	}
	// Modify the .ext file after scan
	if err := os.WriteFile(extPath, []byte(`{"ct":"application/javascript","ns":"gbf","v":1,"extra":true}`), 0644); err != nil {
		t.Fatalf("WriteFile extPath failed: %v", err)
	}

	res, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
	if err != nil {
		t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
	}
	if res.SkippedChangedCount != 1 {
		t.Fatalf("expected SkippedChangedCount=1 when lingering .ext was modified after scan, got %+v", res)
	}
	if _, err := os.Stat(extPath); err != nil {
		t.Fatalf("expected modified .ext to remain on disk, got err=%v", err)
	}
}

func TestDeleteSnapshotSkipsModifiedOrReplacedPairedExtWhenDataPresent(t *testing.T) {
	t.Run("ModifiedInPlace", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		headers := map[string]string{"content-type": "application/javascript"}
		body := []byte("console.log('mod_in_place_test');")
		if !mgr.SaveWithNamespace("gbf", "assets/js/mod_ext.js", headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		dataPath := filepath.Join(tmpDir, "assets", "js", "mod_ext.js")
		extPath := dataPath + ".ext"

		// Modify .ext content, size, and mtime after snapshot while preserving matching size/mtime for data file
		time.Sleep(10 * time.Millisecond)
		dataFi, _ := os.Stat(dataPath)
		newMeta := []byte(fmt.Sprintf(`{"ct":"application/javascript","ns":"gbf","v":1,"updated":true,"extra":"large_payload_to_change_size","size":%d,"mtime":%d}`, dataFi.Size(), dataFi.ModTime().UnixNano()))
		if err := os.WriteFile(extPath, newMeta, 0644); err != nil {
			t.Fatalf("WriteFile extPath failed: %v", err)
		}

		res, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if res.DeletedAssetFiles != 1 {
			t.Fatalf("expected data file to be deleted (DeletedAssetFiles=1), got %+v", res)
		}
		if res.DeletedMetaFiles != 0 {
			t.Fatalf("expected modified .ext NOT to be deleted (DeletedMetaFiles=0), got %+v", res)
		}
		if res.SkippedChangedCount != 1 {
			t.Fatalf("expected SkippedChangedCount=1 for modified .ext, got %+v", res)
		}
		if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
			t.Fatalf("expected data file to be removed from disk, got err=%v", err)
		}
		if data, err := os.ReadFile(extPath); err != nil || !bytes.Equal(data, newMeta) {
			t.Fatalf("expected modified .ext to remain intact on disk, got err=%v, data=%s", err, string(data))
		}
	})

	t.Run("ReplacedWithNewFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		headers := map[string]string{"content-type": "application/javascript"}
		body := []byte("console.log('replace_ext_test');")
		if !mgr.SaveWithNamespace("gbf", "assets/js/replace_ext.js", headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		dataPath := filepath.Join(tmpDir, "assets", "js", "replace_ext.js")
		extPath := dataPath + ".ext"

		// Read existing ext, remove it, and recreate as a new file so physical file identity changes
		origMeta, err := os.ReadFile(extPath)
		if err != nil {
			t.Fatalf("ReadFile extPath failed: %v", err)
		}
		if err := os.Remove(extPath); err != nil {
			t.Fatalf("Remove extPath failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
		if err := os.WriteFile(extPath, origMeta, 0644); err != nil {
			t.Fatalf("WriteFile extPath failed: %v", err)
		}

		res, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if res.DeletedAssetFiles != 1 {
			t.Fatalf("expected data file to be deleted (DeletedAssetFiles=1), got %+v", res)
		}
		if res.DeletedMetaFiles != 0 {
			t.Fatalf("expected replaced .ext NOT to be deleted, got %+v", res)
		}
		if res.SkippedChangedCount != 1 {
			t.Fatalf("expected SkippedChangedCount=1 for replaced .ext, got %+v", res)
		}
		if _, err := os.Stat(extPath); err != nil {
			t.Fatalf("expected replaced .ext to remain on disk, got err=%v", err)
		}
	})

	t.Run("ExtAppearedAfterSnapshot", func(t *testing.T) {
		tmpDir := t.TempDir()
		if err := EnsureRootMarker(tmpDir); err != nil {
			t.Fatalf("EnsureRootMarker failed: %v", err)
		}
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		dataPath := filepath.Join(tmpDir, "assets", "js", "no_ext.js")
		if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
			t.Fatalf("MkdirAll failed: %v", err)
		}
		if err := os.WriteFile(dataPath, []byte("console.log('no_ext');"), 0644); err != nil {
			t.Fatalf("WriteFile dataPath failed: %v", err)
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Create a .ext file after snapshot was taken with matching size/mtime
		extPath := dataPath + ".ext"
		noExtFi, _ := os.Stat(dataPath)
		newExtData := []byte(fmt.Sprintf(`{"ct":"application/javascript","v":1,"size":%d,"mtime":%d}`, noExtFi.Size(), noExtFi.ModTime().UnixNano()))
		if err := os.WriteFile(extPath, newExtData, 0644); err != nil {
			t.Fatalf("WriteFile extPath failed: %v", err)
		}

		res, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if res.DeletedAssetFiles != 1 {
			t.Fatalf("expected data file to be deleted, got %+v", res)
		}
		if res.DeletedMetaFiles != 0 {
			t.Fatalf("expected newly appeared .ext NOT to be deleted, got %+v", res)
		}
		if res.SkippedChangedCount != 1 {
			t.Fatalf("expected SkippedChangedCount=1 for new .ext, got %+v", res)
		}
		if _, err := os.Stat(extPath); err != nil {
			t.Fatalf("expected new .ext to remain on disk, got err=%v", err)
		}
	})
}

func TestAuditAndRepair_PreservesActiveStreamingTempFileAndRemovesOrphan(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 32)
	defer mgr.Close()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatal(err)
	}

	headers := map[string]string{"content-type": "application/javascript"}
	activeKey := "/assets/js/active_stream.js"
	activeBody := []byte("console.log('active_stream_payload');")

	// 1. Create an active streaming temp file via CreateDiskTempFile
	f, tmpPath, gen, err := mgr.CreateDiskTempFile("gbf", activeKey)
	if err != nil {
		t.Fatalf("CreateDiskTempFile failed: %v", err)
	}
	if _, err := f.Write(activeBody); err != nil {
		_ = f.Close()
		t.Fatalf("Write to active temp file failed: %v", err)
	}
	// Close the file handle (simulating the window between tmpFile.Close() and CommitDiskTempFile in proxy.go)
	if err := f.Close(); err != nil {
		t.Fatalf("Close active temp file failed: %v", err)
	}

	// 2. Create an unregistered orphan .tmp. file in the same directory
	orphanTmpPath := filepath.Join(filepath.Dir(tmpPath), "orphan.js.tmp.9999.12345.1")
	if err := os.WriteFile(orphanTmpPath, []byte("orphan_temp"), 0644); err != nil {
		t.Fatalf("WriteFile orphanTmpPath failed: %v", err)
	}

	// 3. Create an attributable PNG, then corrupt it without changing its size or mtime.
	corruptKey := "assets/img/sp/ui/corrupt_boost.png"
	corruptPath, ok := mgr.resolvePathWithNamespace("gbf", corruptKey)
	if !ok {
		t.Fatal("failed to resolve corrupt cache path")
	}
	validPNG := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	if !mgr.Save(corruptKey, map[string]string{"content-type": "image/png"}, validPNG) {
		t.Fatal("failed to save confirmed cache file")
	}
	originalInfo, err := os.Stat(corruptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corruptPath, bytes.Repeat([]byte("x"), len(validPNG)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(corruptPath, originalInfo.ModTime(), originalInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	rGen := mgr.residentPool.NextGeneration()
	mgr.residentPool.ResetForGen(rGen)
	const residentItemBytes = int64(4 * 1024 * 1024)
	if !mgr.residentPool.TryAcquireBudget(residentItemBytes, rGen) {
		t.Fatalf("TryAcquireBudget failed")
	}
	mgr.residentPool.SetWithGen(makeRAMKey("gbf", corruptKey), &CacheItem{
		Key:  makeRAMKey("gbf", corruptKey),
		Data: make([]byte, residentItemBytes),
		Size: residentItemBytes,
	}, rGen)
	slruCapBefore := mgr.ramCache.MaxBytes()

	// 4. Run AuditAndRepairWithProgress while the streaming temp file is still uncommitted
	auditRes := mgr.AuditAndRepairWithProgress(nil, nil)
	if auditRes["corrupted"] != 1 {
		t.Fatalf("expected 1 corrupted file, got %+v", auditRes)
	}

	// Verify orphan .tmp. file was removed
	if _, err := os.Stat(orphanTmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan temp file to be removed by AuditAndRepair, got err=%v", err)
	}

	// Verify active streaming .tmp. file was preserved and can be committed cleanly
	if _, err := os.Stat(tmpPath); err != nil {
		t.Fatalf("expected active streaming temp file to be preserved by AuditAndRepair, got err=%v", err)
	}
	committedItem, committed := mgr.CommitDiskTempFile("gbf", activeKey, headers, tmpPath, int64(len(activeBody)), gen)
	if !committed || committedItem == nil {
		t.Fatalf("expected CommitDiskTempFile to succeed after AuditAndRepair")
	}
	gotItem, source := mgr.GetWithNamespace("gbf", activeKey)
	if gotItem == nil || !bytes.Equal(gotItem.Data, activeBody) {
		t.Fatalf("expected committed file to be readable from cache (source=%s), got %+v", source, gotItem)
	}

	// Verify corrupted item was evicted from ResidentPool and SLRU budget synced back up
	if mgr.ramBudget.ResidentBytes() != 0 {
		t.Fatalf("expected ResidentBytes to be 0 after corrupted resident item removal, got %d", mgr.ramBudget.ResidentBytes())
	}
	if mgr.ramCache.MaxBytes() <= slruCapBefore {
		t.Fatalf("expected SLRU MaxBytes to increase after freeing resident bytes (before=%d, after=%d)", slruCapBefore, mgr.ramCache.MaxBytes())
	}
}

func TestTempTokenCapacityBoundary_PreservesActiveFilesUnderAudit(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, 64)
	defer mgr.Close()
	if err := EnsureRootMarker(tmpDir); err != nil {
		t.Fatal(err)
	}

	headers := map[string]string{"content-type": "application/javascript"}
	validJS := []byte("console.log('capacity_boundary_active_temp');")

	type activeTemp struct {
		key     string
		tmpPath string
		gen     uint64
	}

	active := make([]activeTemp, 0, maxActiveTempTokens)
	for i := 0; i < maxActiveTempTokens; i++ {
		k := fmt.Sprintf("/assets/js/cap_active_%d.js", i)
		f, tp, g, err := mgr.CreateDiskTempFile("gbf", k)
		if err != nil {
			t.Fatalf("CreateDiskTempFile failed at %d: %v", i, err)
		}
		if _, err := f.Write(validJS); err != nil {
			_ = f.Close()
			t.Fatalf("Write failed at %d: %v", i, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("Close failed at %d: %v", i, err)
		}
		active = append(active, activeTemp{key: k, tmpPath: tp, gen: g})
	}

	// Attempt 32 extra registrations while registry is full of live temp files:
	// all must fail cleanly without leaving orphan .tmp. files or evicting any of the 1024 active tokens.
	for i := 0; i < 32; i++ {
		extraKey := fmt.Sprintf("/assets/js/cap_extra_%d.js", i)
		f, tp, _, err := mgr.CreateDiskTempFile("gbf", extraKey)
		if err == nil {
			_ = f.Close()
			mgr.DiscardDiskTempFile(tp)
			t.Fatalf("expected overflow CreateDiskTempFile %d to fail when 1024 active tokens exist", i)
		}
	}

	// Create an unregistered orphan .tmp. file alongside the 1024 active temp files
	orphanPath := filepath.Join(filepath.Dir(active[0].tmpPath), "orphan_cap.js.tmp.9999.1.1")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0644); err != nil {
		t.Fatalf("WriteFile orphanPath failed: %v", err)
	}

	// Run AuditAndRepair: orphan .tmp. must be removed, while all 1024 active .tmp. files must be preserved
	mgr.AuditAndRepairWithProgress(nil, nil)

	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan temp file to be removed by AuditAndRepair, got err=%v", err)
	}

	for i, item := range active {
		if _, err := os.Stat(item.tmpPath); err != nil {
			t.Fatalf("expected active temp file %d (%s) to be preserved by AuditAndRepair, got err=%v", i, item.tmpPath, err)
		}
	}

	// If one temp file disappears from disk externally, the capacity sweep must reclaim its slot and allow a new registration
	_ = os.Remove(active[0].tmpPath)
	reclaimedKey := "/assets/js/cap_reclaimed.js"
	fRec, tpRec, genRec, err := mgr.CreateDiskTempFile("gbf", reclaimedKey)
	if err != nil {
		t.Fatalf("expected CreateDiskTempFile to succeed after sweeping missing temp file slot: %v", err)
	}
	_, _ = fRec.Write(validJS)
	_ = fRec.Close()
	active[0] = activeTemp{key: reclaimedKey, tmpPath: tpRec, gen: genRec}

	// Verify active temp files can still commit cleanly after audit
	for _, idx := range []int{0, 1, 512, maxActiveTempTokens - 1} {
		item := active[idx]
		committedItem, ok := mgr.CommitDiskTempFile("gbf", item.key, headers, item.tmpPath, int64(len(validJS)), item.gen)
		if !ok || committedItem == nil {
			t.Fatalf("expected CommitDiskTempFile to succeed for index %d (%s)", idx, item.key)
		}
	}

	// Clean up remaining uncommitted temp files
	for _, item := range active {
		mgr.DiscardDiskTempFile(item.tmpPath)
	}
}

func TestBreakdown_ClearCategories_SkippedChangedAndAlreadyMissingConsistency(t *testing.T) {
	t.Run("SkippedChangedPreservesNewMemoryAndInFlightTokens", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		headers := map[string]string{"content-type": "application/javascript"}
		key := "assets/js/changing.js"
		v1Body := []byte("console.log('v1');")
		v2Body := []byte("console.log('v2_updated_after_scan');")
		v3Body := []byte("console.log('v3_inflight_after_v2');")

		if !mgr.SaveWithNamespace("gbf", key, headers, v1Body) {
			t.Fatalf("SaveWithNamespace v1 failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Update the asset to v2 on disk, in RAM, in ResidentPool, and in diskMeta after the snapshot
		time.Sleep(15 * time.Millisecond)
		if !mgr.SaveWithNamespace("gbf", key, headers, v2Body) {
			t.Fatalf("SaveWithNamespace v2 failed")
		}
		rGen := mgr.residentPool.NextGeneration()
		mgr.residentPool.ResetForGen(rGen)
		ramKey := makeRAMKey("gbf", key)
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        v2Body,
			ContentType: "application/javascript",
			Size:        int64(len(v2Body)),
		})

		// Also stage an in-flight v3 RAM save / persistence token acquired after v2
		_, _, fileV3, genV3, tokV3, ok := mgr.saveRAMInternal("gbf", key, headers, v3Body, false, true)
		if !ok {
			t.Fatalf("saveRAMInternal v3 failed")
		}
		// Re-populate residentPool with v2 so we can verify neither ramCache(v3), residentPool, nor diskMeta(v2) are wiped
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        v2Body,
			ContentType: "application/javascript",
			Size:        int64(len(v2Body)),
		})
		mgr.setDiskMeta(ramKey, diskMetaEntry{
			size:      int64(len(v2Body)),
			namespace: "gbf",
			exists:    true,
			hasMeta:   true,
			verified:  true,
		})

		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if delRes.SkippedChangedCount != 1 || delRes.DeletedAssetFiles != 0 {
			t.Fatalf("expected SkippedChangedCount=1 and DeletedAssetFiles=0, got %+v", delRes)
		}

		// 1. RAM cache must still contain v3
		if ramItem, ok := mgr.ramCache.Get(ramKey); !ok || ramItem == nil || !bytes.Equal(ramItem.Data, v3Body) {
			t.Fatalf("expected RAM cache entry to be preserved on SkippedChanged, got %+v", ramItem)
		}
		// 2. ResidentPool must still contain its entry
		if resItem, ok := mgr.residentPool.Get(ramKey); !ok || resItem == nil {
			t.Fatalf("expected ResidentPool entry to be preserved on SkippedChanged")
		}
		// 3. diskMeta must still be present
		if _, ok := mgr.getDiskMeta(ramKey); !ok {
			t.Fatalf("expected diskMeta entry to be preserved on SkippedChanged")
		}
		// 4. In-flight persistence token for v3 must still be valid and succeed in persisting to disk
		if !mgr.saveToDiskWithToken(fileV3, headers, v3Body, genV3, tokV3) {
			t.Fatalf("expected post-snapshot in-flight persistence token to remain valid after SkippedChanged")
		}
	})

	t.Run("AlreadyMissingClearsMemoryAndInvalidatesStaleToken", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		headers := map[string]string{"content-type": "application/javascript"}
		key := "assets/js/vanished.js"
		body := []byte("console.log('vanished');")

		if !mgr.SaveWithNamespace("gbf", key, headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Acquire a pre-deletion persistence token before the file vanishes
		_, _, staleFile, staleGen, staleTok, ok := mgr.saveRAMInternal("gbf", key, headers, body, false, true)
		if !ok {
			t.Fatalf("saveRAMInternal failed")
		}

		// Externally remove the disk file and its .ext while leaving RAM / ResidentPool / diskMeta populated
		diskPath := filepath.Join(tmpDir, "assets", "js", "vanished.js")
		_ = os.Remove(diskPath)
		_ = os.Remove(diskPath + ".ext")

		rGen := mgr.residentPool.NextGeneration()
		mgr.residentPool.ResetForGen(rGen)
		ramKey := makeRAMKey("gbf", key)
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        body,
			ContentType: "application/javascript",
			Size:        int64(len(body)),
		})
		mgr.setDiskMeta(ramKey, diskMetaEntry{
			size:      int64(len(body)),
			namespace: "gbf",
			exists:    true,
			hasMeta:   true,
			verified:  true,
		})

		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if delRes.AlreadyMissingCount != 1 {
			t.Fatalf("expected AlreadyMissingCount=1, got %+v", delRes)
		}

		// Memory states must be cleared and stale token invalidated even though disk file was AlreadyMissing
		if _, ok := mgr.ramCache.Get(ramKey); ok {
			t.Fatalf("expected RAM cache entry to be cleared when disk file is AlreadyMissing")
		}
		if _, ok := mgr.residentPool.Get(ramKey); ok {
			t.Fatalf("expected ResidentPool entry to be cleared when disk file is AlreadyMissing")
		}
		if _, ok := mgr.getDiskMeta(ramKey); ok {
			t.Fatalf("expected diskMeta entry to be cleared when disk file is AlreadyMissing")
		}
		if mgr.saveToDiskWithToken(staleFile, headers, body, staleGen, staleTok) {
			t.Fatalf("expected pre-deletion token to be invalidated when disk file is AlreadyMissing")
		}
	})

	t.Run("PathBoundaryFailurePreservesMemoryAndTokens", func(t *testing.T) {
		parentDir := t.TempDir()
		cacheRoot := filepath.Join(parentDir, "cache_root")
		if err := os.MkdirAll(cacheRoot, 0755); err != nil {
			t.Fatalf("MkdirAll cacheRoot failed: %v", err)
		}
		mgr := NewManager(cacheRoot, 32)
		defer mgr.Close()

		headers := map[string]string{"content-type": "application/javascript"}
		key := "assets/js/boundary.js"
		body := []byte("console.log('boundary');")
		if !mgr.SaveWithNamespace("gbf", key, headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Replace intermediate directory assets/js with a regular file so openPinnedParentRoot fails with a non-ENOENT error
		jsDir := filepath.Join(cacheRoot, "assets", "js")
		if err := os.RemoveAll(jsDir); err != nil {
			t.Fatalf("RemoveAll jsDir failed: %v", err)
		}
		if err := os.WriteFile(jsDir, []byte("not_a_directory"), 0644); err != nil {
			t.Fatalf("WriteFile blocker failed: %v", err)
		}

		// Populate RAM, ResidentPool, diskMeta, and acquire an active token
		_, _, _, _, activeTok, ok := mgr.saveRAMInternal("gbf", key, headers, body, false, true)
		if !ok {
			t.Fatalf("saveRAMInternal failed")
		}
		ramKey := makeRAMKey("gbf", key)
		rGen := mgr.residentPool.NextGeneration()
		mgr.residentPool.ResetForGen(rGen)
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        body,
			ContentType: "application/javascript",
			Size:        int64(len(body)),
		})
		mgr.setDiskMeta(ramKey, diskMetaEntry{
			size:      int64(len(body)),
			namespace: "gbf",
			exists:    true,
			hasMeta:   true,
			verified:  true,
		})

		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if delRes.FailedCount != 1 {
			t.Fatalf("expected FailedCount=1 on path boundary failure, got %+v", delRes)
		}

		if _, ok := mgr.ramCache.Get(ramKey); !ok {
			t.Fatalf("expected RAM cache entry to be preserved on path boundary failure")
		}
		if _, ok := mgr.residentPool.Get(ramKey); !ok {
			t.Fatalf("expected ResidentPool entry to be preserved on path boundary failure")
		}
		if _, ok := mgr.getDiskMeta(ramKey); !ok {
			t.Fatalf("expected diskMeta entry to be preserved on path boundary failure")
		}
		shard := mgr.tokenShardByCanon(activeTok.canonKey)
		shard.mu.Lock()
		tokValid := isTokenValidLocked(shard, activeTok, mgr.generation)
		shard.mu.Unlock()
		if !tokValid {
			t.Fatalf("expected active token to remain valid on path boundary failure")
		}
	})
}

func TestBreakdownAndCategoryCleanupExcludeUnconfirmedFiles(t *testing.T) {
	base := t.TempDir()
	if err := EnsureRootMarker(base); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(base, 16)
	defer mgr.Close()

	validRel := "assets/img/ui/icon/weapon/confirmed.png"
	validBody := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRconfirmed")
	if !mgr.Save(validRel, map[string]string{"content-type": "image/png"}, validBody) {
		t.Fatal("failed to save confirmed asset")
	}
	validPath := filepath.Join(base, filepath.FromSlash(validRel))

	userPath := filepath.Join(base, "assets", "personal_data", "avatar.png")
	if err := os.MkdirAll(filepath.Dir(userPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, validBody, 0644); err != nil {
		t.Fatal(err)
	}

	badPair := filepath.Join(base, "assets", "img", "ui", "icon", "weapon", "unverified.png")
	if err := os.WriteFile(badPair, validBody, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPair+".ext", []byte("{malformed metadata"), 0644); err != nil {
		t.Fatal(err)
	}

	snap := mgr.ScanBreakdownWithProgress(nil, nil)
	if snap == nil || !snap.Valid {
		t.Fatalf("expected valid scan snapshot, got %+v", snap)
	}
	if snap.AssetFiles != 1 {
		t.Fatalf("only the confirmed cache asset should be counted, got %d", snap.AssetFiles)
	}
	category := ClassifyCacheEntry("gbf", validRel)
	if !IsSelectableCleanupCategory(category) {
		t.Fatalf("unexpected unselectable category %q", category)
	}
	if _, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{category}, []string{"gbf"}, nil, nil); err != nil {
		t.Fatalf("category cleanup failed: %v", err)
	}
	if _, err := os.Stat(validPath); !os.IsNotExist(err) {
		t.Fatalf("confirmed asset should have been removed, got %v", err)
	}
	for _, keep := range []string{userPath, badPair, badPair + ".ext"} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("unconfirmed file %s was not preserved: %v", keep, err)
		}
	}
}

func TestAlreadyMissingSidecarValidationAndMemoryLifecycle(t *testing.T) {
	headers := map[string]string{"content-type": "application/javascript"}
	body := []byte("console.log('already_missing_lifecycle');")

	t.Run("FileMissing_SidecarUnchanged_ClearsMemory_PreservesSidecar", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		key := "assets/js/missing_sidecar_ok.js"
		if !mgr.SaveWithNamespace("gbf", key, headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Warm RAM and acquire persistence token
		_, _, staleFile, staleGen, staleTok, ok := mgr.saveRAMInternal("gbf", key, headers, body, false, true)
		if !ok {
			t.Fatalf("saveRAMInternal failed")
		}
		ramKey := makeRAMKey("gbf", key)
		rGen := mgr.residentPool.Generation()
		mgr.residentPool.ResetForGen(rGen)
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        body,
			ContentType: "application/javascript",
			Size:        int64(len(body)),
		})
		mgr.setDiskMeta(ramKey, diskMetaEntry{
			size:      int64(len(body)),
			namespace: "gbf",
			exists:    true,
			hasMeta:   true,
			verified:  true,
		})

		// Remove ONLY the data file, leave .ext intact on disk
		dataPath := filepath.Join(tmpDir, "assets", "js", "missing_sidecar_ok.js")
		extPath := dataPath + ".ext"
		if err := os.Remove(dataPath); err != nil {
			t.Fatalf("Remove dataPath failed: %v", err)
		}

		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if delRes.AlreadyMissingCount != 1 || delRes.SkippedChangedCount != 0 {
			t.Fatalf("expected AlreadyMissingCount=1 and SkippedChangedCount=0, got %+v", delRes)
		}

		// Memory states must be cleared and token invalidated
		if _, ok := mgr.ramCache.Get(ramKey); ok {
			t.Fatalf("expected RAM cache entry to be cleared when sidecar is unchanged")
		}
		if _, ok := mgr.residentPool.Get(ramKey); ok {
			t.Fatalf("expected ResidentPool entry to be cleared when sidecar is unchanged")
		}
		if _, ok := mgr.getDiskMeta(ramKey); ok {
			t.Fatalf("expected diskMeta entry to be cleared when sidecar is unchanged")
		}
		if mgr.saveToDiskWithToken(staleFile, headers, body, staleGen, staleTok) {
			t.Fatalf("expected pre-deletion token to be invalidated when sidecar is unchanged")
		}

		// Orphan .ext must remain on disk intact
		if _, err := os.Stat(extPath); err != nil {
			t.Fatalf("expected orphan .ext file to remain on disk: %v", err)
		}
	})

	t.Run("FileMissing_SidecarModified_PreservesMemoryAndTokens", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		key := "assets/js/missing_sidecar_mod.js"
		if !mgr.SaveWithNamespace("gbf", key, headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Warm RAM and acquire persistence token
		_, _, staleFile, staleGen, staleTok, ok := mgr.saveRAMInternal("gbf", key, headers, body, false, true)
		if !ok {
			t.Fatalf("saveRAMInternal failed")
		}
		ramKey := makeRAMKey("gbf", key)
		rGen := mgr.residentPool.Generation()
		mgr.residentPool.ResetForGen(rGen)
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        body,
			ContentType: "application/javascript",
			Size:        int64(len(body)),
		})
		mgr.setDiskMeta(ramKey, diskMetaEntry{
			size:      int64(len(body)),
			namespace: "gbf",
			exists:    true,
			hasMeta:   true,
			verified:  true,
		})

		// Remove data file and modify .ext
		dataPath := filepath.Join(tmpDir, "assets", "js", "missing_sidecar_mod.js")
		extPath := dataPath + ".ext"
		if err := os.Remove(dataPath); err != nil {
			t.Fatalf("Remove dataPath failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
		modExt := []byte(`{"ct":"application/javascript","ns":"gbf","v":1,"updated":true,"extra":"payload_to_change_size"}`)
		if err := os.WriteFile(extPath, modExt, 0644); err != nil {
			t.Fatalf("WriteFile extPath failed: %v", err)
		}

		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if delRes.SkippedChangedCount != 1 || delRes.AlreadyMissingCount != 0 {
			t.Fatalf("expected SkippedChangedCount=1 and AlreadyMissingCount=0, got %+v", delRes)
		}

		// Memory states and token must be preserved because sidecar changed
		if _, ok := mgr.ramCache.Get(ramKey); !ok {
			t.Fatalf("expected RAM cache entry to be preserved when sidecar changed")
		}
		if _, ok := mgr.residentPool.Get(ramKey); !ok {
			t.Fatalf("expected ResidentPool entry to be preserved when sidecar changed")
		}
		if _, ok := mgr.getDiskMeta(ramKey); !ok {
			t.Fatalf("expected diskMeta entry to be preserved when sidecar changed")
		}
		if !mgr.isOpTokenValid(staleTok) {
			t.Fatalf("expected persistence token to remain valid when sidecar changed")
		}
		_ = staleFile
		_ = staleGen

		// Modified .ext must remain on disk intact
		if data, err := os.ReadFile(extPath); err != nil || !bytes.Equal(data, modExt) {
			t.Fatalf("expected modified .ext file to remain on disk: %v", err)
		}
	})

	t.Run("FileMissing_AbnormalSidecar_PreservesMemoryAndReportsFailure", func(t *testing.T) {
		tmpDir := t.TempDir()
		mgr := NewManager(tmpDir, 32)
		defer mgr.Close()

		key := "assets/js/missing_sidecar_abnormal.js"
		if !mgr.SaveWithNamespace("gbf", key, headers, body) {
			t.Fatalf("SaveWithNamespace failed")
		}

		snap := mgr.ScanBreakdownWithProgress(nil, nil)
		if snap == nil || !snap.Valid {
			t.Fatalf("expected valid snapshot")
		}

		// Warm RAM and acquire persistence token
		_, _, _, _, staleTok, ok := mgr.saveRAMInternal("gbf", key, headers, body, false, true)
		if !ok {
			t.Fatalf("saveRAMInternal failed")
		}
		ramKey := makeRAMKey("gbf", key)
		rGen := mgr.residentPool.Generation()
		mgr.residentPool.ResetForGen(rGen)
		mgr.residentPool.Set(ramKey, &CacheItem{
			Key:         ramKey,
			Data:        body,
			ContentType: "application/javascript",
			Size:        int64(len(body)),
		})
		mgr.setDiskMeta(ramKey, diskMetaEntry{
			size:      int64(len(body)),
			namespace: "gbf",
			exists:    true,
			hasMeta:   true,
			verified:  true,
		})

		// Remove data file and replace .ext with a directory
		dataPath := filepath.Join(tmpDir, "assets", "js", "missing_sidecar_abnormal.js")
		extPath := dataPath + ".ext"
		if err := os.Remove(dataPath); err != nil {
			t.Fatalf("Remove dataPath failed: %v", err)
		}
		if err := os.Remove(extPath); err != nil {
			t.Fatalf("Remove extPath failed: %v", err)
		}
		if err := os.Mkdir(extPath, 0755); err != nil {
			t.Fatalf("Mkdir extPath failed: %v", err)
		}

		delRes, err := mgr.ClearCategoriesWithProgress(snap.SnapshotID, []string{CategoryOther}, nil, nil, nil)
		if err != nil {
			t.Fatalf("ClearCategoriesWithProgress failed: %v", err)
		}
		if delRes.FailedCount != 1 {
			t.Fatalf("expected FailedCount=1 for abnormal sidecar, got %+v", delRes)
		}

		// Memory states and token must be preserved because sidecar is abnormal
		if _, ok := mgr.ramCache.Get(ramKey); !ok {
			t.Fatalf("expected RAM cache entry to be preserved when sidecar is abnormal")
		}
		if _, ok := mgr.residentPool.Get(ramKey); !ok {
			t.Fatalf("expected ResidentPool entry to be preserved when sidecar is abnormal")
		}
		if _, ok := mgr.getDiskMeta(ramKey); !ok {
			t.Fatalf("expected diskMeta entry to be preserved when sidecar is abnormal")
		}
		if !mgr.isOpTokenValid(staleTok) {
			t.Fatalf("expected persistence token to remain valid when sidecar is abnormal")
		}
	})
}
