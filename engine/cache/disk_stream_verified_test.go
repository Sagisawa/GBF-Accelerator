package cache

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestVerified_MetadataSerializationAndCompat tests .ext serialization, deserialization,
// backwards compatibility with legacy Version 1 schemas (including legacy "ContentType" key),
// and atomic promotion to verified.
func TestVerified_MetadataSerializationAndCompat(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/img/test_compat.png"
	filePath, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)

	payload := makeFakePNG(1024)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	// 1. Legacy .ext without verified field, using legacy "ContentType" key
	legacyExt := fmt.Sprintf(`{"ContentType":"image/png","ETag":"\"legacy-etag\"","LastModified":"Tue, 01 Jan 2025 00:00:00 GMT","v":1}`)
	extPath := filePath + ".ext"
	if err := os.WriteFile(extPath, []byte(legacyExt), 0644); err != nil {
		t.Fatalf("failed to write legacy .ext: %v", err)
	}

	// Verify loadDiskMetaFast returns false because item is not yet verified
	itemFast, ok := mgr.loadDiskMetaFast("gbf", cleanKey, filePath)
	if ok || itemFast != nil {
		t.Fatalf("expected unverified asset to be rejected by loadDiskMetaFast")
	}

	// First load via loadAndValidateDiskItem performs full validation and promotes to Verified
	item, err := mgr.loadAndValidateDiskItem("gbf", cleanKey, filePath)
	if err != nil {
		t.Fatalf("loadAndValidateDiskItem failed: %v", err)
	}
	if item.ContentType != "image/png" || item.ETag != `"legacy-etag"` {
		t.Fatalf("unexpected item metadata: ct=%s, etag=%s", item.ContentType, item.ETag)
	}

	// In-memory index must now be marked verified
	ramKey := makeRAMKey("gbf", cleanKey)
	metaEntry, ok := mgr.getDiskMeta(ramKey)
	if !ok || !metaEntry.verified {
		t.Fatalf("expected in-memory metadata to be marked verified: %+v", metaEntry)
	}

	// .ext on disk must now contain verified: true
	extBytes, err := os.ReadFile(extPath)
	if err != nil {
		t.Fatalf("failed to read updated .ext: %v", err)
	}
	parsedMeta, ok := parseExtFile(extBytes)
	if !ok || !parsedMeta.Verified || parsedMeta.Size != fi.Size() || parsedMeta.MTimeNano != fi.ModTime().UnixNano() {
		t.Fatalf("expected updated .ext to contain verified metadata: %+v", parsedMeta)
	}

	// Subsequent loadDiskMetaFast must succeed with Data == nil
	itemFastAfter, ok := mgr.loadDiskMetaFast("gbf", cleanKey, filePath)
	if !ok || itemFastAfter == nil {
		t.Fatalf("expected loadDiskMetaFast to succeed after promotion")
	}
	if itemFastAfter.Data != nil {
		t.Fatalf("expected loadDiskMetaFast to return Data == nil, got %d bytes", len(itemFastAfter.Data))
	}
	if itemFastAfter.Size != fi.Size() || itemFastAfter.ContentType != "image/png" {
		t.Fatalf("unexpected metadata from loadDiskMetaFast: size=%d, ct=%s", itemFastAfter.Size, itemFastAfter.ContentType)
	}
}

// TestVerified_StatMismatchInvalidation tests that external modification of file size
// or mtime invalidates verified state and forces re-validation.
func TestVerified_StatMismatchInvalidation(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/img/mismatch_test.png"
	filePath, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)

	payload := makeFakePNG(2048)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Validate and promote to verified
	item, err := mgr.loadAndValidateDiskItem("gbf", cleanKey, filePath)
	if err != nil || item == nil {
		t.Fatalf("initial validation failed: %v", err)
	}

	// Verify loadDiskMetaFast succeeds
	if _, ok := mgr.loadDiskMetaFast("gbf", cleanKey, filePath); !ok {
		t.Fatal("expected loadDiskMetaFast to succeed for verified file")
	}

	// Simulate external tampering/truncation: change file content & size
	truncatedPayload := payload[:1024]
	if err := os.WriteFile(filePath, truncatedPayload, 0644); err != nil {
		t.Fatalf("failed to truncate file: %v", err)
	}

	// Stat mismatch must cause loadDiskMetaFast to reject
	if itemBad, ok := mgr.loadDiskMetaFast("gbf", cleanKey, filePath); ok || itemBad != nil {
		t.Fatal("expected loadDiskMetaFast to reject file after size change")
	}
}

// TestSLRU_StreamItemAdmissionRejection tests that items with Data == nil && Size > 0
// are strictly prohibited from entering the SLRU cache.
func TestSLRU_StreamItemAdmissionRejection(t *testing.T) {
	lru := NewLRUCache(16 * 1024 * 1024)

	// 1. Attempt to add Stream Item (Data == nil, Size > 0) via Set
	streamItem := &CacheItem{
		Key:         "large_stream_item",
		Data:        nil,
		Size:        5 * 1024 * 1024,
		ContentType: "video/mp4",
	}
	lru.Set("large_stream_item", streamItem)

	if _, ok := lru.Get("large_stream_item"); ok {
		t.Fatal("expected Stream Item to be rejected from LRUCache Set")
	}
	if lru.TotalBytes() != 0 {
		t.Fatalf("expected 0 TotalBytes in LRUCache, got %d", lru.TotalBytes())
	}

	// 2. Attempt to add Stream Item via SetProbation
	lru.SetProbation("large_stream_item", streamItem)
	if _, ok := lru.Get("large_stream_item"); ok {
		t.Fatal("expected Stream Item to be rejected from LRUCache SetProbation")
	}
	if lru.TotalBytes() != 0 {
		t.Fatalf("expected 0 TotalBytes in LRUCache, got %d", lru.TotalBytes())
	}

	// 3. Normal items with Data != nil must be admitted normally
	normalItem := &CacheItem{
		Key:         "normal_item",
		Data:        make([]byte, 1024),
		Size:        1024,
		ContentType: "image/png",
	}
	lru.SetProbation("normal_item", normalItem)
	got, ok := lru.Get("normal_item")
	if !ok || got == nil || len(got.Data) != 1024 {
		t.Fatalf("expected normal item to be admitted and retrieved from LRUCache")
	}
	if lru.TotalBytes() != 1024 {
		t.Fatalf("expected TotalBytes 1024, got %d", lru.TotalBytes())
	}
}

// TestBoost_LargeFileAdmissionAndBudget tests that files larger than MaxDiskDirectReadSize (2MB)
// are admitted into Boost ResidentPool when budget allows, served from RAM, and fallback to Disk Stream when Boost is disabled.
func TestBoost_LargeFileAdmissionAndBudget(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets")
	_ = os.MkdirAll(assetsDir, 0755)

	// Small file: 100 KB
	smallPath := filepath.Join(assetsDir, "small.png")
	_ = os.WriteFile(smallPath, makeFakePNG(100*1024), 0644)
	_ = os.WriteFile(smallPath+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	// Large file: 3 MB (> MaxDiskDirectReadSize 2MB)
	largePayload := makeFakePNG(3 * 1024 * 1024)
	largePath := filepath.Join(assetsDir, "large.png")
	_ = os.WriteFile(largePath, largePayload, 0644)
	_ = os.WriteFile(largePath+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	// 1. With sufficient budget (64MB), both small and large files enter ResidentPool
	progress := mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if progress.State != BoostStateCompleted {
		t.Fatalf("expected boost completed, got %s", progress.State)
	}
	if progress.LoadedFiles != 2 {
		t.Fatalf("expected exactly 2 files loaded into boost resident pool, got %d", progress.LoadedFiles)
	}

	smallKey := makeRAMKey("gbf", "assets/small.png")
	largeKey := makeRAMKey("gbf", "assets/large.png")

	smallItem, okSmall := mgr.residentPool.Get(smallKey)
	if !okSmall || len(smallItem.Data) != 100*1024 {
		t.Fatalf("expected small asset in ResidentPool with full data, ok=%v", okSmall)
	}

	largeItem, okLarge := mgr.residentPool.Get(largeKey)
	if !okLarge || len(largeItem.Data) != 3*1024*1024 {
		t.Fatalf("expected large asset (>2MB) in ResidentPool with full data, ok=%v, len=%d", okLarge, len(largeItem.Data))
	}

	// In Boost mode, GetWithNamespace returns RAM-BOOST with full Data
	itemBoost, srcBoost := mgr.GetWithNamespace("gbf", "assets/large.png")
	if srcBoost != "RAM-BOOST" || itemBoost == nil || len(itemBoost.Data) != 3*1024*1024 {
		t.Fatalf("expected RAM-BOOST hit with full data, got src=%s, dataLen=%d", srcBoost, len(itemBoost.Data))
	}

	// 2. When Boost is disabled, large file is NOT in RAM, and falls back to normal full disk read
	mgr.ResidentPool().Disable(0)
	mgr.ClearRAM()

	items, totalBytes := mgr.ResidentPool().Stats()
	if items != 0 || totalBytes != 0 {
		t.Fatalf("expected 0 resident items/bytes after disable, got items=%d, bytes=%d", items, totalBytes)
	}

	itemNormal, srcNormal := mgr.GetWithNamespace("gbf", "assets/large.png")
	if srcNormal != "DISK" || itemNormal == nil {
		t.Fatalf("expected DISK hit in normal mode, got src=%s", srcNormal)
	}
	if itemNormal.Size != 3*1024*1024 || len(itemNormal.Data) != 3*1024*1024 || !bytes.Equal(itemNormal.Data, largePayload) {
		t.Fatalf("expected normal mode large file to have full Data and Size=3MB matching payload, got dataLen=%d, size=%d", len(itemNormal.Data), itemNormal.Size)
	}

	// 3. When Budget is insufficient, large file stops with BoostStatePartial
	mgrSmallBudget := NewManager(filepath.Join(tempDir, "small_budget"), 2) // 2MB budget
	defer mgrSmallBudget.Close()

	sbAssetsDir := filepath.Join(tempDir, "small_budget", "assets")
	_ = os.MkdirAll(sbAssetsDir, 0755)
	_ = os.WriteFile(filepath.Join(sbAssetsDir, "a_small.png"), makeFakePNG(100*1024), 0644)
	_ = os.WriteFile(filepath.Join(sbAssetsDir, "a_small.png.ext"), []byte(`{"ContentType":"image/png","v":1}`), 0644)
	_ = os.WriteFile(filepath.Join(sbAssetsDir, "z_large.png"), makeFakePNG(3*1024*1024), 0644)
	_ = os.WriteFile(filepath.Join(sbAssetsDir, "z_large.png.ext"), []byte(`{"ContentType":"image/png","v":1}`), 0644)

	progSmall := mgrSmallBudget.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if progSmall.State != BoostStatePartial {
		t.Fatalf("expected BoostStatePartial when budget is exceeded by 3MB file, got %s", progSmall.State)
	}
	if progSmall.LoadedFiles != 1 {
		t.Fatalf("expected only 1 file (small) loaded within 2MB budget, got %d", progSmall.LoadedFiles)
	}
}

// TestOpenDiskStream_StreamingAndFallback tests OpenDiskStream path resolution,
// namespace fallback ("assets/" prefix), and streaming integrity.
func TestOpenDiskStream_StreamingAndFallback(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(64 * 1024)

	// 1. Direct path
	p1, _ := mgr.resolvePathWithNamespace("gbf", "assets/sound/bgm.mp3")
	_ = os.MkdirAll(filepath.Dir(p1), 0755)
	_ = os.WriteFile(p1, payload, 0644)
	fi1, _ := os.Stat(p1)
	extMeta1 := diskMeta{
		ContentType: "audio/mpeg",
		Version:     1,
		Verified:    true,
		Size:        fi1.Size(),
		MTimeNano:   fi1.ModTime().UnixNano(),
	}
	_ = writeDiskExtFile(p1, extMeta1)

	rc, size, err := mgr.OpenDiskStream("gbf", "/assets/sound/bgm.mp3")
	if err != nil {
		t.Fatalf("OpenDiskStream failed: %v", err)
	}
	defer rc.Close()

	if size != int64(len(payload)) {
		t.Fatalf("expected size %d, got %d", len(payload), size)
	}
	streamedData, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(streamedData, payload) {
		t.Fatal("streamed data mismatch for direct path")
	}

	// 2. Alt path fallback: request "/sound/bgm.mp3" should resolve to "assets/sound/bgm.mp3"
	rcAlt, sizeAlt, errAlt := mgr.OpenDiskStream("gbf", "/sound/bgm.mp3")
	if errAlt != nil {
		t.Fatalf("OpenDiskStream fallback failed: %v", errAlt)
	}
	defer rcAlt.Close()
	if sizeAlt != int64(len(payload)) {
		t.Fatalf("expected fallback size %d, got %d", len(payload), sizeAlt)
	}
	altData, err := io.ReadAll(rcAlt)
	if err != nil || !bytes.Equal(altData, payload) {
		t.Fatal("streamed data mismatch for fallback path")
	}
}

// TestGetMetadataWithNamespace_ZeroByteDiskRead tests that GetMetadataWithNamespace
// returns metadata without loading the underlying file body into memory.
func TestGetMetadataWithNamespace_ZeroByteDiskRead(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/movie/scene.mp4"
	filePath, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)

	// Create 3MB file
	fileSize := int64(3 * 1024 * 1024)
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(fileSize)
	_ = f.Close()

	fi, _ := os.Stat(filePath)
	extMeta := diskMeta{
		LastModified:    "Mon, 01 Jan 2024 00:00:00 GMT",
		ETag:            `"scene-etag"`,
		ContentType:     "video/mp4",
		Version:         1,
		Verified:        true,
		Size:            fileSize,
		MTimeNano:       fi.ModTime().UnixNano(),
	}
	if err := writeDiskExtFile(filePath, extMeta); err != nil {
		t.Fatalf("failed to write .ext: %v", err)
	}

	// Call GetMetadataWithNamespace
	item, src := mgr.GetMetadataWithNamespace("gbf", "/"+cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK metadata hit, got item=%v, src=%s", item, src)
	}

	// Verify Data is nil (zero bytes of file body read)
	if item.Data != nil {
		t.Fatalf("expected Data == nil in GetMetadataWithNamespace, got %d bytes", len(item.Data))
	}
	if item.Size != fileSize {
		t.Fatalf("expected Size %d, got %d", fileSize, item.Size)
	}
	if item.ETag != `"scene-etag"` || item.ContentType != "video/mp4" {
		t.Fatalf("unexpected metadata: etag=%s, ct=%s", item.ETag, item.ContentType)
	}
}

// TestMetadataOnly_ContentTypeFallback tests that GetMetadataWithNamespace falls back to MIME
// based on file extension when .ext file has an empty ContentType.
func TestMetadataOnly_ContentTypeFallback(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/img/sp/banner.png"
	filePath, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)

	payload := makeFakePNG(64 * 1024)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatal(err)
	}

	fi, _ := os.Stat(filePath)
	extMeta := diskMeta{
		LastModified: "Mon, 01 Jan 2024 00:00:00 GMT",
		ETag:         `"banner-etag"`,
		ContentType:  "", // empty ContentType to test fallback
		Version:      1,
		Verified:     true,
		Size:         fi.Size(),
		MTimeNano:    fi.ModTime().UnixNano(),
	}
	if err := writeDiskExtFile(filePath, extMeta); err != nil {
		t.Fatal(err)
	}

	item, src := mgr.GetMetadataWithNamespace("gbf", "/"+cleanKey)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got %v, src=%s", item, src)
	}
	if item.ContentType != "image/png" {
		t.Fatalf("expected ContentType image/png from fallback, got %q", item.ContentType)
	}
}

// TestOpenDiskStream_MismatchedMetadata_Invalidation tests that OpenDiskStream detects when
// a file on disk has been modified after verification (size or mtime changed), treats it
// as cache invalidation, and returns an error.
func TestOpenDiskStream_MismatchedMetadata_Invalidation(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/sound/bgm_tampered.mp3"
	filePath, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	_ = os.MkdirAll(filepath.Dir(filePath), 0755)

	origSize := int64(3 * 1024 * 1024)
	f, _ := os.Create(filePath)
	_ = f.Truncate(origSize)
	_ = f.Close()

	fi, _ := os.Stat(filePath)
	extMeta := diskMeta{
		LastModified: "Mon, 01 Jan 2024 00:00:00 GMT",
		ETag:         `"tampered-etag"`,
		ContentType:  "audio/mpeg",
		Version:      1,
		Verified:     true,
		Size:         origSize,
		MTimeNano:    fi.ModTime().UnixNano(),
	}
	_ = writeDiskExtFile(filePath, extMeta)

	// Prime the verified metadata into manager's in-memory index
	item, ok := mgr.loadDiskMetaFast("gbf", cleanKey, filePath)
	if !ok || item == nil {
		t.Fatal("expected loadDiskMetaFast to succeed")
	}

	// 1. First verify OpenDiskStream succeeds on untouched file
	rc, sz, err := mgr.OpenDiskStream("gbf", "/"+cleanKey)
	if err != nil {
		t.Fatalf("expected OpenDiskStream to succeed initially, got: %v", err)
	}
	_ = rc.Close()
	if sz != origSize {
		t.Fatalf("expected size %d, got %d", origSize, sz)
	}

	// 2. Tamper with the file: truncate to 1024 bytes (size mismatch)
	f, err = os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(1024)
	_ = f.Close()

	// 3. OpenDiskStream must detect size mismatch, delete metadata, and return error
	rcTampered, _, errTampered := mgr.OpenDiskStream("gbf", "/"+cleanKey)
	if errTampered == nil {
		_ = rcTampered.Close()
		t.Fatal("expected OpenDiskStream to fail due to size mismatch")
	}

	// In-memory metadata must have been invalidated (deleted)
	ramKey := makeRAMKey("gbf", cleanKey)
	if meta, ok := mgr.getDiskMeta(ramKey); ok && meta.verified {
		t.Fatal("expected verified metadata in memory to be deleted upon mismatch")
	}
}

// TestManager_Invalidate tests that Invalidate purges memory metadata, RAM cache, and disk files.
func TestManager_Invalidate(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	urlPath := "/assets/img/purge_test.png"
	cleanKey := "assets/img/purge_test.png"
	payload := makeFakePNG(4 * 1024)
	headers := map[string]string{"content-type": "image/png"}

	if !mgr.Save(urlPath, headers, payload) {
		t.Fatal("failed to save asset")
	}

	// Verify file and metadata exist
	filePath, ok := mgr.resolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.WriteFile(filePath, payload, 0644)
	_ = os.WriteFile(filePath+".ext", []byte(`{"verified":true}`), 0644)

	// Call Invalidate
	mgr.Invalidate("gbf", urlPath)

	// Disk file and .ext must be removed
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatal("expected disk file to be removed after Invalidate")
	}
	if _, err := os.Stat(filePath + ".ext"); !os.IsNotExist(err) {
		t.Fatal("expected .ext file to be removed after Invalidate")
	}

	// Memory metadata must be deleted
	ramKey := makeRAMKey("gbf", cleanKey)
	if meta, ok := mgr.getDiskMeta(ramKey); ok && meta.exists {
		t.Fatal("expected diskMeta to be deleted after Invalidate")
	}
}

// TestOpenDiskStream_Unverified_Rejected verifies that OpenDiskStream refuses to open
// and stream a file on disk if it lacks verified metadata.
func TestOpenDiskStream_Unverified_Rejected(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(64 * 1024)
	p, _ := mgr.resolvePathWithNamespace("gbf", "assets/sound/unverified.mp3")
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	_ = os.WriteFile(p, payload, 0644)

	// Without verified metadata, OpenDiskStream must reject and return an error
	rc, _, err := mgr.OpenDiskStream("gbf", "/assets/sound/unverified.mp3")
	if err == nil {
		_ = rc.Close()
		t.Fatal("expected OpenDiskStream to fail for unverified file without metadata")
	}
}

// TestDiskRead_LegacyLargeAsset_FirstFullReadThenStream verifies that when a >2MB legacy
// cache item is accessed:
// 1. Created >2MB old cache with .ext lacking Verified (or Verified=false)
// 2. First Get must take legacy full-read path (Data != nil, len(Data) == size, full content)
// 3. After successful validation, in-memory metadata and .ext are promoted to Verified=true
// 4. Second Get returns full Data, Size > 2MB, byte-level matching payload, no RAM admission
func TestDiskRead_LegacyLargeAsset_FirstFullReadThenStream(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/img/sp/legacy_large.png"
	p, ok := mgr.resolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(p), 0755)

	fileSize := 3 * 1024 * 1024
	payload := makeFakePNG(fileSize)
	if err := os.WriteFile(p, payload, 0644); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	// 1 & 2: Legacy .ext without verified (or verified=false)
	legacyExt := `{"ContentType":"image/png","ETag":"\"legacy-etag\"","LastModified":"Tue, 01 Jan 2025 00:00:00 GMT","v":1,"verified":false}`
	if err := os.WriteFile(p+".ext", []byte(legacyExt), 0644); err != nil {
		t.Fatalf("failed to write .ext: %v", err)
	}

	// 3. First Get must walk legacy full-read path: Data != nil, full content verified
	item1, src1 := mgr.GetWithNamespace("gbf", "/"+cleanKey)
	if item1 == nil || src1 != "DISK" {
		t.Fatalf("expected DISK hit on first Get, got item=%v, src=%s", item1, src1)
	}
	if item1.Data == nil {
		t.Fatal("expected first Get on unverified legacy cache to perform full read and return Data != nil")
	}
	if len(item1.Data) != fileSize || !bytes.Equal(item1.Data, payload) {
		t.Fatalf("first Get returned corrupted Data: got %d bytes, want %d bytes", len(item1.Data), fileSize)
	}
	if item1.Size != int64(fileSize) {
		t.Fatalf("first Get size mismatch: got %d, want %d", item1.Size, fileSize)
	}

	// 4. After first validation passes, in-memory metadata and .ext must be upgraded to Verified=true
	ramKey := makeRAMKey("gbf", cleanKey)
	meta, ok := mgr.getDiskMeta(ramKey)
	if !ok || !meta.verified {
		t.Fatal("expected in-memory metadata to be upgraded to verified: true")
	}

	extBytes, err := os.ReadFile(p + ".ext")
	if err != nil {
		t.Fatalf("failed to read updated .ext: %v", err)
	}
	parsedMeta, ok := parseExtFile(extBytes)
	if !ok || !parsedMeta.Verified || parsedMeta.Size != int64(fileSize) || parsedMeta.MTimeNano != fi.ModTime().UnixNano() {
		t.Fatalf("expected .ext to be upgraded to verified: true with matching size and mtime, got: %+v", parsedMeta)
	}

	// 5. Second Get must return full Data, Size > 2MB and byte-level identical payload
	item2, src2 := mgr.GetWithNamespace("gbf", "/"+cleanKey)
	if item2 == nil || src2 != "DISK" {
		t.Fatalf("expected DISK hit on second Get, got item=%v, src=%s", item2, src2)
	}
	if item2.Data == nil || len(item2.Data) != fileSize || !bytes.Equal(item2.Data, payload) {
		t.Fatalf("expected second Get to return full Data (%d bytes), got %d bytes", fileSize, len(item2.Data))
	}
	if item2.Size != int64(fileSize) {
		t.Fatalf("second Get size mismatch: got %d, want %d", item2.Size, fileSize)
	}
	if mgr.IsRAMProtected("gbf", cleanKey) {
		t.Fatal("large asset must not enter RAM Protected segment")
	}

	// Verify OpenDiskStream succeeds on the promoted file
	rc, streamSize, err := mgr.OpenDiskStream("gbf", "/"+cleanKey)
	if err != nil {
		t.Fatalf("OpenDiskStream failed after promotion: %v", err)
	}
	defer rc.Close()
	if streamSize != int64(fileSize) {
		t.Fatalf("streamSize mismatch: got %d, want %d", streamSize, fileSize)
	}
	streamedBytes, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(streamedBytes, payload) {
		t.Fatal("streamed data mismatch with payload")
	}

	// 6. Test with legacy .ext NOT containing Verified field
	cleanKeyOmitted := "assets/img/sp/legacy_omitted_verified.png"
	pOmitted, _ := mgr.resolvePathWithNamespace("gbf", cleanKeyOmitted)
	_ = os.WriteFile(pOmitted, payload, 0644)
	fiOmitted, err := os.Stat(pOmitted)
	if err != nil {
		t.Fatalf("stat pOmitted failed: %v", err)
	}
	legacyExtOmitted := `{"ContentType":"image/png","ETag":"\"legacy-omitted-etag\"","LastModified":"Tue, 01 Jan 2025 00:00:00 GMT","v":1}`
	if err := os.WriteFile(pOmitted+".ext", []byte(legacyExtOmitted), 0644); err != nil {
		t.Fatalf("failed to write omitted-verified .ext: %v", err)
	}

	// First Get on file with .ext lacking Verified: full-read path
	itemOmitted1, srcOmitted1 := mgr.GetWithNamespace("gbf", "/"+cleanKeyOmitted)
	if itemOmitted1 == nil || srcOmitted1 != "DISK" {
		t.Fatalf("expected DISK hit on first Get for omitted-verified, got item=%v, src=%s", itemOmitted1, srcOmitted1)
	}
	if itemOmitted1.Data == nil || len(itemOmitted1.Data) != fileSize || !bytes.Equal(itemOmitted1.Data, payload) {
		t.Fatal("expected full read on first Get for file with omitted-verified .ext")
	}

	// Verify .ext file was promoted to Verified=true
	extOmittedBytes, err := os.ReadFile(pOmitted + ".ext")
	if err != nil {
		t.Fatalf("failed to read updated .ext: %v", err)
	}
	parsedOmittedMeta, ok := parseExtFile(extOmittedBytes)
	if !ok || !parsedOmittedMeta.Verified || parsedOmittedMeta.Size != int64(fileSize) || parsedOmittedMeta.MTimeNano != fiOmitted.ModTime().UnixNano() {
		t.Fatalf("expected .ext to be promoted to verified: true with matching size and mtime: %+v", parsedOmittedMeta)
	}

	// Second Get on file with omitted-verified .ext: returns full Data
	itemOmitted2, _ := mgr.GetWithNamespace("gbf", "/"+cleanKeyOmitted)
	if itemOmitted2 == nil || itemOmitted2.Data == nil || len(itemOmitted2.Data) != fileSize || !bytes.Equal(itemOmitted2.Data, payload) || itemOmitted2.Size != int64(fileSize) {
		t.Fatal("expected full Data on second Get after promotion for omitted-verified .ext")
	}
	if mgr.IsRAMProtected("gbf", cleanKeyOmitted) {
		t.Fatal("large asset must not enter RAM Protected segment for omitted-verified .ext")
	}

	// 7. Test with no .ext file (simulating externally populated or missing .ext file)
	cleanKeyNoExt := "assets/img/sp/legacy_no_ext.png"
	pNoExt, _ := mgr.resolvePathWithNamespace("gbf", cleanKeyNoExt)
	_ = os.WriteFile(pNoExt, payload, 0644)

	// First Get on file with no .ext: full-read path
	itemNoExt1, _ := mgr.GetWithNamespace("gbf", "/"+cleanKeyNoExt)
	if itemNoExt1 == nil || itemNoExt1.Data == nil || len(itemNoExt1.Data) != fileSize {
		t.Fatal("expected full read on first Get for file with missing .ext")
	}

	// Second Get on file with no .ext: returns full Data
	itemNoExt2, _ := mgr.GetWithNamespace("gbf", "/"+cleanKeyNoExt)
	if itemNoExt2 == nil || itemNoExt2.Data == nil || len(itemNoExt2.Data) != fileSize || !bytes.Equal(itemNoExt2.Data, payload) || itemNoExt2.Size != int64(fileSize) {
		t.Fatal("expected full Data on second Get after promotion for file with no .ext")
	}
	if mgr.IsRAMProtected("gbf", cleanKeyNoExt) {
		t.Fatal("large asset must not enter RAM Protected segment for file with no .ext")
	}
}

// TestDiskRead_LegacyLargeAsset_CorruptContentRejected verifies that when a >2MB legacy
// cache item contains corrupt content (e.g. invalid magic bytes for PNG), full-read
// validation rejects it, does NOT promote it to Verified, and removes the corrupt cache.
func TestDiskRead_LegacyLargeAsset_CorruptContentRejected(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanKey := "assets/img/sp/corrupt_large.png"
	p, ok := mgr.resolvePathWithNamespace("gbf", cleanKey)
	if !ok {
		t.Fatal("failed to resolve path")
	}
	_ = os.MkdirAll(filepath.Dir(p), 0755)

	// 3MB of all-zero junk bytes (invalid PNG magic bytes)
	fileSize := 3 * 1024 * 1024
	junkPayload := make([]byte, fileSize)
	if err := os.WriteFile(p, junkPayload, 0644); err != nil {
		t.Fatal(err)
	}

	legacyExt := `{"ContentType":"image/png","ETag":"\"corrupt-etag\"","LastModified":"Tue, 01 Jan 2025 00:00:00 GMT","v":1,"verified":false}`
	if err := os.WriteFile(p+".ext", []byte(legacyExt), 0644); err != nil {
		t.Fatal(err)
	}

	// First Get: validation must fail
	item1, src1 := mgr.GetWithNamespace("gbf", "/"+cleanKey)
	if item1 != nil || src1 != "" {
		t.Fatalf("expected corrupt asset to be rejected, got item=%v, src=%s", item1, src1)
	}

	// Must NOT be marked verified in memory
	ramKey := makeRAMKey("gbf", cleanKey)
	if meta, ok := mgr.getDiskMeta(ramKey); ok && meta.verified {
		t.Fatal("corrupt asset must not be marked verified in memory")
	}

	// OpenDiskStream must reject
	rc, _, err := mgr.OpenDiskStream("gbf", "/"+cleanKey)
	if err == nil {
		_ = rc.Close()
		t.Fatal("expected OpenDiskStream to reject unverified corrupt asset")
	}
}

