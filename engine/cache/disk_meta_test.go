package cache

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for file to persist: %s", path)
}

func TestDiskMetadataIndex_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(4 * 1024)
	headers := map[string]string{
		"content-type":  "image/png",
		"etag":          `"initial-etag"`,
		"last-modified": "Wed, 21 Oct 2026 07:28:00 GMT",
	}

	testPath := "/assets/img/sp/test_meta.png"
	cleanKey := "assets/img/sp/test_meta.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	// 1. Save asset to cache -> diskMetaIndex should be populated immediately
	if !mgr.Save(testPath, headers, payload) {
		t.Fatal("failed to save asset")
	}

	entry, ok := mgr.getDiskMeta(ramKey)
	if !ok {
		t.Fatal("expected diskMetaIndex to contain entry immediately after save")
	}
	if entry.contentType != "image/png" || entry.etag != `"initial-etag"` {
		t.Fatalf("unexpected meta entry: %+v", entry)
	}

	// 2. HasCacheWithNamespace should return true for existing asset
	if !mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to return true for existing asset")
	}

	// 3. Clear RAM and verify Disk Hit uses metadata from index
	mgr.ClearRAM()
	item, src := mgr.GetWithNamespace("gbf", testPath)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got item=%v, src=%s", item, src)
	}
	if item.ContentType != "image/png" || item.ETag != `"initial-etag"` {
		t.Fatalf("unexpected loaded item metadata: ContentType=%s, ETag=%s", item.ContentType, item.ETag)
	}

	// 4. Cold disk load test: simulate fresh process start with empty diskMetaIndex
	mgr.ClearRAM()
	mgr.clearDiskMeta()

	// Verify index is empty
	if _, ok := mgr.getDiskMeta(ramKey); ok {
		t.Fatal("expected diskMetaIndex to be cleared")
	}

	// Loading from disk must read .ext once and populate diskMetaIndex
	item2, src2 := mgr.GetWithNamespace("gbf", testPath)
	if item2 == nil || src2 != "DISK" {
		t.Fatalf("expected cold DISK hit, got src=%s", src2)
	}
	if entry2, ok := mgr.getDiskMeta(ramKey); !ok || entry2.etag != `"initial-etag"` {
		t.Fatalf("expected cold load to repopulate diskMetaIndex, got: %+v, ok=%v", entry2, ok)
	}

	// 5. Corrupt file test: auto-repair should remove both disk files and metadata index entry
	mgr.ClearRAM()
	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.WriteFile(diskFile, []byte("invalid_not_png_bytes"), 0644)

	itemBad, _ := mgr.GetWithNamespace("gbf", testPath)
	if itemBad != nil {
		t.Fatal("expected corrupted item to fail validation")
	}
	if _, ok := mgr.getDiskMeta(ramKey); ok {
		t.Fatal("expected invalid cache content to be deleted from diskMetaIndex")
	}

	// 6. Negative cache check: subsequent HasCacheWithNamespace must report false
	if mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to return false after corruption")
	}
}

func TestDiskMetadataIndex_ShardPruning(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	// Insert into shard until limit triggers pruning
	const count = 5000
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("bench_key_%d", i)
		mgr.setDiskMeta(key, diskMetaEntry{
			contentType: "image/png",
			etag:        fmt.Sprintf(`"%d"`, i),
		})
	}

	// Verify manager did not crash and memory remained bounded
	totalItems := 0
	for i := range mgr.diskMetaShards {
		shard := &mgr.diskMetaShards[i]
		shard.mu.RLock()
		totalItems += len(shard.items)
		shard.mu.RUnlock()
	}
	if totalItems == 0 || totalItems > count {
		t.Fatalf("unexpected total items in diskMetaShards: %d", totalItems)
	}
}

func TestDiskMetadataIndex_ColdStatPresence(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type":  "image/png",
		"etag":          `"cold-presence-etag"`,
		"last-modified": "Wed, 21 Oct 2026 07:28:00 GMT",
	}

	testPath := "/assets/img/sp/cold_presence.png"
	cleanKey := "assets/img/sp/cold_presence.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	if !mgr.Save(testPath, headers, payload) {
		t.Fatal("failed to save asset")
	}

	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	waitForFile(t, diskFile)

	// Clear memory state to simulate cold process restart
	mgr.ClearRAM()
	mgr.clearDiskMeta()

	// 1. HasCacheWithNamespace should stat disk, return true, and record presence in memory
	if !mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to return true for cold on-disk asset")
	}

	entry, ok := mgr.getDiskMeta(ramKey)
	if !ok {
		t.Fatal("expected getDiskMeta to have presence recorded after cold HasCacheWithNamespace")
	}
	if !entry.exists {
		t.Fatal("expected entry.exists to be true")
	}
	if entry.hasMeta {
		t.Fatal("expected entry.hasMeta to be false before first GetWithNamespace")
	}

	// 2. Subsequent HasCacheWithNamespace should return true and maintain recorded presence
	if !mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected subsequent HasCacheWithNamespace to return true for existing disk asset")
	}

	// 3. First GetWithNamespace reads .ext and upgrades entry to hasMeta = true
	item, src := mgr.GetWithNamespace("gbf", testPath)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}

	entry2, ok2 := mgr.getDiskMeta(ramKey)
	if !ok2 || !entry2.hasMeta || entry2.etag != `"cold-presence-etag"` {
		t.Fatalf("expected upgraded entry with hasMeta=true, got: %+v, ok=%v", entry2, ok2)
	}
}

func TestDiskMetadataIndex_FallbackPath(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type":  "image/png",
		"etag":          `"fallback-etag"`,
		"last-modified": "Wed, 21 Oct 2026 07:28:00 GMT",
	}

	// Save with assets/ prefix on disk
	testPathOnDisk := "/assets/12345/sound/se.png"
	if !mgr.Save(testPathOnDisk, headers, payload) {
		t.Fatal("failed to save asset")
	}
	testFile, _ := mgr.resolvePathWithNamespace("gbf", "assets/12345/sound/se.png")
	waitForFile(t, testFile)

	mgr.ClearRAM()
	mgr.clearDiskMeta()

	// Query without assets/ prefix -> fallback should hit and index the queried key
	queriedPath := "/12345/sound/se.png"
	queriedKey := makeRAMKey("gbf", "12345/sound/se.png")

	if !mgr.HasCacheWithNamespace("gbf", queriedPath) {
		t.Fatal("expected fallback HasCacheWithNamespace to return true")
	}
	entry, ok := mgr.getDiskMeta(queriedKey)
	if !ok || !entry.exists {
		t.Fatalf("expected fallback presence to be recorded for queried key, got: %+v, ok=%v", entry, ok)
	}

	// GetWithNamespace should load via fallback and index metadata for queried key
	item, src := mgr.GetWithNamespace("gbf", queriedPath)
	if item == nil || src != "DISK" {
		t.Fatalf("expected fallback DISK hit, got src=%s", src)
	}
	if item.ETag != `"fallback-etag"` {
		t.Fatalf("unexpected item ETag: %s", item.ETag)
	}

	entryUpgraded, okUpgraded := mgr.getDiskMeta(queriedKey)
	if !okUpgraded || !entryUpgraded.hasMeta || entryUpgraded.etag != `"fallback-etag"` {
		t.Fatalf("expected upgraded metadata for fallback queried key, got: %+v", entryUpgraded)
	}
}

func TestDiskMetadataIndex_MissingExtFallback(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/img/sp/no_ext.png"
	ramKey := makeRAMKey("gbf", cleanKey)
	targetFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.MkdirAll(targetFile[:len(targetFile)-len("/no_ext.png")], 0755)

	payload := makeFakePNG(1024)
	if err := os.WriteFile(targetFile, payload, 0644); err != nil {
		t.Fatalf("failed to write raw file: %v", err)
	}

	// No .ext file was written. GetWithNamespace should succeed with mime fallback and index it.
	item, src := mgr.GetWithNamespace("gbf", "/assets/img/sp/no_ext.png")
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit without .ext, got src=%s", src)
	}
	if item.ContentType != "image/png" {
		t.Fatalf("expected mime fallback image/png, got %s", item.ContentType)
	}

	entry, ok := mgr.getDiskMeta(ramKey)
	if !ok || !entry.hasMeta || entry.contentType != "image/png" {
		t.Fatalf("expected mime fallback to be indexed with hasMeta=true, got: %+v", entry)
	}
	if entry.etag == "" || entry.etag != item.ETag {
		t.Fatalf("expected fallback etag %s to be indexed, got %s", item.ETag, entry.etag)
	}
}

func TestDiskMetadataIndex_ExternalDeletion(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type": "image/png",
		"etag":         `"del-etag"`,
	}
	testPath := "/assets/img/sp/del_test.png"
	cleanKey := "assets/img/sp/del_test.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	mgr.Save(testPath, headers, payload)
	waitForFile(t, diskFile)

	// Verify indexed
	if _, ok := mgr.getDiskMeta(ramKey); !ok {
		t.Fatal("expected asset to be indexed")
	}

	// Delete from disk externally
	mgr.ClearRAM()
	_ = os.Remove(diskFile)
	_ = os.Remove(diskFile + ".ext")

	// GetWithNamespace should notice deletion, evict from index, and return nil
	item, src := mgr.GetWithNamespace("gbf", testPath)
	if item != nil || src != "" {
		t.Fatalf("expected miss for deleted file, got src=%s", src)
	}

	if _, ok := mgr.getDiskMeta(ramKey); ok {
		t.Fatal("expected deleted file to be evicted from diskMetaIndex")
	}

	if mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to return false after deletion")
	}
}

func TestDiskMetadataIndex_ExternalDeletion_DirectHasCache(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type": "image/png",
		"etag":         `"direct-del-etag"`,
	}
	testPath := "/assets/img/sp/direct_del.png"
	cleanKey := "assets/img/sp/direct_del.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	mgr.Save(testPath, headers, payload)
	waitForFile(t, diskFile)

	// Clear RAM cache so lookup must check disk
	mgr.ClearRAM()

	// External deletion of the cache file on disk
	_ = os.Remove(diskFile)
	_ = os.Remove(diskFile + ".ext")

	// HasCacheWithNamespace called directly without prior GetWithNamespace
	// Must NOT return a false positive true; must detect deletion and return false
	if mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to return false after external deletion")
	}

	if _, ok := mgr.getDiskMeta(ramKey); ok {
		t.Fatal("expected deleted entry to be purged from diskMetaIndex by HasCacheWithNamespace")
	}
}

func TestDiskMetadataIndex_ExternalReplacement(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	initialPayload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type":  "image/png",
		"etag":          `"initial-etag"`,
		"last-modified": "Wed, 21 Oct 2026 07:00:00 GMT",
	}
	testPath := "/assets/img/sp/replace_test.png"
	cleanKey := "assets/img/sp/replace_test.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	mgr.Save(testPath, headers, initialPayload)
	waitForFile(t, diskFile)

	// Verify initial metadata is indexed
	entry, ok := mgr.getDiskMeta(ramKey)
	if !ok || entry.etag != `"initial-etag"` {
		t.Fatalf("expected initial etag indexed, got %+v", entry)
	}

	// Evict from RAM without wiping diskMeta (simulating LRU eviction)
	mgr.ramCache.Delete(ramKey)

	// External replacement: write different bytes (different size and mtime) and update .ext
	time.Sleep(10 * time.Millisecond) // ensure mtime differs
	replacedPayload := makeFakePNG(5 * 1024)
	if err := os.WriteFile(diskFile, replacedPayload, 0644); err != nil {
		t.Fatalf("failed to overwrite disk file: %v", err)
	}
	newExt := `{"ct":"image/png","etag":"\"updated-etag\"","LastModified":"Wed, 21 Oct 2026 08:00:00 GMT","v":1}`
	if err := os.WriteFile(diskFile+".ext", []byte(newExt), 0644); err != nil {
		t.Fatalf("failed to overwrite ext file: %v", err)
	}

	// HasCache called before Get
	if !mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to be true")
	}

	// GetWithNamespace must detect file identity change, reload .ext, and return updated metadata
	item, src := mgr.GetWithNamespace("gbf", testPath)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit for replaced item, got src=%s", src)
	}
	if item.ETag != `"updated-etag"` {
		t.Fatalf("expected updated ETag %q, got stale %q", `"updated-etag"`, item.ETag)
	}
	if len(item.Data) != len(replacedPayload) {
		t.Fatalf("expected replaced payload length %d, got %d", len(replacedPayload), len(item.Data))
	}

	// diskMetaIndex must be updated with the new identity and metadata
	updatedEntry, ok := mgr.getDiskMeta(ramKey)
	if !ok || updatedEntry.etag != `"updated-etag"` || updatedEntry.size != int64(len(replacedPayload)) {
		t.Fatalf("expected diskMetaIndex to be updated with new identity, got %+v", updatedEntry)
	}
}

func TestDiskMetadataIndex_SetCacheBase(t *testing.T) {
	tempDir1 := t.TempDir()
	tempDir2 := t.TempDir()

	mgr := NewManager(tempDir1, 16)
	defer mgr.Close()

	payload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type": "image/png",
		"etag":         `"base1-etag"`,
	}
	testPath := "/assets/img/sp/switch_base.png"
	cleanKey := "assets/img/sp/switch_base.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	mgr.Save(testPath, headers, payload)
	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	waitForFile(t, diskFile)

	if !mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected item in base1")
	}
	if _, ok := mgr.getDiskMeta(ramKey); !ok {
		t.Fatal("expected item indexed in base1")
	}

	// Switch cache base to empty dir2
	mgr.SetCacheBase(tempDir2)

	// diskMetaIndex must be cleared and HasCacheWithNamespace must report false
	if _, ok := mgr.getDiskMeta(ramKey); ok {
		t.Fatal("expected diskMetaIndex to be cleared after SetCacheBase")
	}
	if mgr.HasCacheWithNamespace("gbf", testPath) {
		t.Fatal("expected HasCacheWithNamespace to return false in new empty cache base")
	}
}

func TestDiskMetadataIndex_PruneStaleVersions(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(1024)
	headers := map[string]string{
		"content-type": "image/png",
	}

	// Save versions 1000 and 2000
	path1 := "/assets/1000/img/sp/item.png"
	path2 := "/assets/2000/img/sp/item.png"
	key1 := makeRAMKey("gbf", "assets/1000/img/sp/item.png")
	key2 := makeRAMKey("gbf", "assets/2000/img/sp/item.png")

	mgr.Save(path1, headers, payload)
	mgr.Save(path2, headers, payload)

	f1, _ := mgr.resolvePathWithNamespace("gbf", "assets/1000/img/sp/item.png")
	f2, _ := mgr.resolvePathWithNamespace("gbf", "assets/2000/img/sp/item.png")
	waitForFile(t, f1)
	waitForFile(t, f2)

	// Prune keeping only 1 version (2000 should be kept, 1000 pruned)
	deletedDirs, deletedFiles, _ := mgr.PruneStaleVersions(1)
	if deletedDirs == 0 || deletedFiles == 0 {
		t.Fatalf("expected pruning to delete files, got dirs=%d, files=%d", deletedDirs, deletedFiles)
	}

	// Pruned version (1000) must be evicted from diskMetaIndex
	if _, ok := mgr.getDiskMeta(key1); ok {
		t.Fatal("expected pruned version 1000 to be evicted from diskMetaIndex")
	}
	if mgr.HasCacheWithNamespace("gbf", path1) {
		t.Fatal("expected HasCacheWithNamespace to return false for pruned version 1000")
	}

	// Retained version (2000) should still be accessible
	if !mgr.HasCacheWithNamespace("gbf", path2) {
		t.Fatal("expected retained version 2000 to remain in cache")
	}
	if _, ok := mgr.getDiskMeta(key2); !ok {
		t.Fatal("expected retained version 2000 to remain in diskMetaIndex")
	}
}

func TestDiskMetadataIndex_ConcurrentOps(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(1024)
	headers := map[string]string{
		"content-type": "image/png",
		"etag":         `"concurrent-etag"`,
	}

	const workers = 8
	const iterations = 50
	done := make(chan struct{})

	for w := 0; w < workers; w++ {
		go func(workerID int) {
			path := fmt.Sprintf("/assets/img/sp/concurrent_%d.png", workerID%4)
			for i := 0; i < iterations; i++ {
				switch i % 5 {
				case 0:
					mgr.Save(path, headers, payload)
				case 1:
					mgr.HasCacheWithNamespace("gbf", path)
				case 2:
					mgr.GetWithNamespace("gbf", path)
				case 3:
					mgr.ClearRAM()
				case 4:
					mgr.TouchRAMWithNamespace("gbf", path)
				}
			}
			done <- struct{}{}
		}(w)
	}

	for w := 0; w < workers; w++ {
		<-done
	}
}

func TestDiskMetadataIndex_ClearRAM_PreservesMetadata(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(2 * 1024)
	headers := map[string]string{
		"content-type":  "image/png",
		"etag":          `"keep-on-clear-ram"`,
		"last-modified": "Wed, 21 Oct 2026 07:28:00 GMT",
	}

	testPath := "/assets/img/sp/clear_ram_test.png"
	cleanKey := "assets/img/sp/clear_ram_test.png"
	ramKey := makeRAMKey("gbf", cleanKey)

	if !mgr.Save(testPath, headers, payload) {
		t.Fatal("failed to save asset")
	}

	diskFile, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	waitForFile(t, diskFile)

	// Verify item is in RAM and in diskMetaIndex
	if items, _ := mgr.Stats(); items != 1 {
		t.Fatalf("expected 1 item in RAM, got %d", items)
	}
	entry, ok := mgr.getDiskMeta(ramKey)
	if !ok || !entry.hasMeta || entry.etag != `"keep-on-clear-ram"` {
		t.Fatalf("expected metadata indexed, got: %+v, ok=%v", entry, ok)
	}

	// Calling ClearRAM() must wipe RAM cache payloads but preserve disk metadata index
	mgr.ClearRAM()

	if items, _ := mgr.Stats(); items != 0 {
		t.Fatalf("expected 0 items in RAM after ClearRAM(), got %d", items)
	}

	entryAfter, okAfter := mgr.getDiskMeta(ramKey)
	if !okAfter || !entryAfter.hasMeta || entryAfter.etag != `"keep-on-clear-ram"` {
		t.Fatalf("expected diskMetaIndex to persist across ClearRAM(), got: %+v, ok=%v", entryAfter, okAfter)
	}

	// Subsequent GetWithNamespace should hit DISK using the indexed metadata
	item, src := mgr.GetWithNamespace("gbf", testPath)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}
	if item.ETag != `"keep-on-clear-ram"` {
		t.Fatalf("unexpected item ETag: %s", item.ETag)
	}

	// Calling ClearAll() must wipe both RAM and diskMetaIndex
	mgr.ClearAll()
	if _, okClearAll := mgr.getDiskMeta(ramKey); okClearAll {
		t.Fatal("expected diskMetaIndex to be cleared after ClearAll()")
	}
}
