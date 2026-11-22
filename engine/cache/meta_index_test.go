package cache

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func TestMetaIndex_SaveLoadAndTieredWarmup(t *testing.T) {
	base := t.TempDir()
	m1 := NewManager(base, 32)

	jsData := []byte("console.log('core-script');")
	cssData := []byte("body { color: #000; }")
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89sprite")

	if !m1.Save("/assets/1795000000/js/core.js", map[string]string{"content-type": "application/javascript", "etag": `"js1"`}, jsData) {
		t.Fatal("failed to save core.js")
	}
	if !m1.Save("/assets/1795000000/css/app.css", map[string]string{"content-type": "text/css", "etag": `"css1"`}, cssData) {
		t.Fatal("failed to save app.css")
	}
	// Save png images with gbf namespace AFTER js/css so their timestamps are >= js/css,
	// proving Tiered Warmup still prioritizes core .js/.css over newer .png images.
	if !m1.Save("/assets/1795000000/img/bg.png", map[string]string{"content-type": "image/png", "etag": `"png1"`}, pngData) {
		t.Fatal("failed to save bg.png")
	}
	if !m1.Save("/assets/1795000000/img/bg2.png", map[string]string{"content-type": "image/png", "etag": `"png2"`}, pngData) {
		t.Fatal("failed to save bg2.png")
	}

	m1.Close()

	indexPath := filepath.Join(base, MetaIndexFileName)
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected meta index file at %s: %v", indexPath, err)
	}

	// New manager should load meta index asynchronously and serve metadata without reading .ext
	m2 := NewManager(base, 32)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)

	if !m2.metaIndexLoaded.Load() {
		t.Fatal("expected metaIndexLoaded to be true after loading snapshot")
	}

	// Remove .ext files to prove disk metadata hits come from the binary index (while physical data remains intact)
	pJS, _ := m2.ResolvePathWithNamespace("gbf", "/assets/1795000000/js/core.js")
	_ = os.Remove(pJS + ".ext")

	metaItem, src := m2.GetMetadataWithNamespace("gbf", "/assets/1795000000/js/core.js")
	if metaItem == nil || src != "DISK" || metaItem.ETag != `"js1"` {
		t.Fatalf("expected disk metadata hit from meta index without .ext, got item=%v src=%q", metaItem, src)
	}

	// Tiered Warmup(2) should prioritize Tier-3 core .js/.css over Tier-1 bg.png
	m2.ClearRAM()
	loaded := m2.Warmup(2)
	if loaded != 2 {
		t.Fatalf("expected Warmup(2) to load 2 items, got %d", loaded)
	}
	if m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/js/core.js") == nil {
		t.Fatal("expected core.js to be prioritized by Tiered Warmup")
	}
	if m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/css/app.css") == nil {
		t.Fatal("expected app.css to be prioritized over .png items during Warmup(2)")
	}
	if m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/img/bg.png") != nil || m2.PeekRAMWithNamespace("gbf", "/assets/1795000000/img/bg2.png") != nil {
		t.Fatal("expected bg.png / bg2.png (Tier 1) NOT to be loaded before core .js/.css during Warmup(2)")
	}
}

func TestMetaIndex_CorruptFallbackAndClearAllRemoval(t *testing.T) {
	base := t.TempDir()
	indexPath := filepath.Join(base, MetaIndexFileName)
	if err := os.WriteFile(indexPath, []byte("CORRUPT_GARBAGE_DATA_12345"), 0644); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}

	m := NewManager(base, 16)
	defer m.Close()
	m.waitMetaIndexLoad(0)

	if m.metaIndexLoaded.Load() {
		t.Fatal("expected corrupt meta index to be rejected")
	}

	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89clean")
	if !m.Save("/assets/1795000000/img/a.png", map[string]string{"content-type": "image/png"}, pngData) {
		t.Fatal("save failed")
	}
	if err := m.SaveMetaIndex(); err != nil {
		t.Fatalf("SaveMetaIndex failed: %v", err)
	}
	if _, _, _, err := m.ClearAllWithError(); err != nil {
		t.Fatalf("ClearAllWithError failed: %v", err)
	}
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("expected ClearAllWithError to remove %s", MetaIndexFileName)
	}
}

func TestMetaIndex_OrphanTempCleanedAndConcurrentFlushClose(t *testing.T) {
	base := t.TempDir()
	m := NewManager(base, 16)

	orphanTmp := filepath.Join(base, MetaIndexFileName+".tmp.99999.123456.1")
	if err := os.WriteFile(orphanTmp, []byte("stale-tmp"), 0644); err != nil {
		t.Fatalf("write orphanTmp: %v", err)
	}
	activeTmp := filepath.Join(base, MetaIndexFileName+".tmp.99999.123456.2")
	if err := os.WriteFile(activeTmp, []byte("active-tmp"), 0644); err != nil {
		t.Fatalf("write activeTmp: %v", err)
	}
	m.persistMu.RLock()
	tokOK := m.registerTempFileTokenLocked(activeTmp, opToken{gen: m.generation})
	m.persistMu.RUnlock()
	if !tokOK {
		t.Fatal("failed to register active meta index temp token")
	}

	res := m.AuditAndRepairWithProgress(nil, nil)
	if removed, _ := res["temp_files_removed"].(int); removed != 1 {
		t.Fatalf("expected 1 orphaned meta index temp file removed, got %v (full=%+v)", res["temp_files_removed"], res)
	}
	if _, err := os.Stat(orphanTmp); !os.IsNotExist(err) {
		t.Fatalf("expected orphanTmp %s to be deleted by audit", orphanTmp)
	}
	if _, err := os.Stat(activeTmp); err != nil {
		t.Fatalf("expected activeTmp %s to remain protected while registered: %v", activeTmp, err)
	}
	m.DiscardDiskTempFile(activeTmp)

	// Simulate in-flight periodic flush holding metaIndexSaveMu while a subsequent dirty write
	// arrives, verifying Close() waits for the flush and commits the latest dirty write.
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89close")
	if !m.Save("/assets/1795000000/img/final_item.png", map[string]string{"content-type": "image/png", "etag": `"final1"`}, pngData) {
		t.Fatal("save final_item.png failed")
	}

	m.metaIndexSaveMu.Lock()
	m.metaIndexDirty.Store(true)
	closeDone := make(chan struct{})
	go func() {
		m.Close()
		close(closeDone)
	}()

	// Close() must block until the in-flight periodic save releases metaIndexSaveMu.
	select {
	case <-closeDone:
		m.metaIndexSaveMu.Unlock()
		t.Fatal("Close() returned before in-flight metaIndexSaveMu lock was released")
	default:
	}
	m.metaIndexSaveMu.Unlock()
	<-closeDone

	m2 := NewManager(base, 16)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)
	metaItem, src := m2.GetMetadataWithNamespace("gbf", "/assets/1795000000/img/final_item.png")
	if metaItem == nil || src != "DISK" || metaItem.ETag != `"final1"` {
		t.Fatalf("expected meta index flushed cleanly on Close(), got item=%v src=%q", metaItem, src)
	}
}

func TestMetaIndex_V1LegacyIncompleteAndV2CompleteMigration(t *testing.T) {
	base := t.TempDir()

	// 1. Create 3 physical asset files on disk with valid .ext metadata
	m0 := NewManager(base, 16)
	pngData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89item")
	if !m0.Save("/assets/1795000000/img/item1.png", map[string]string{"content-type": "image/png", "etag": `"it1"`}, pngData) {
		t.Fatal("save item1 failed")
	}
	if !m0.Save("/assets/1795000000/img/item2.png", map[string]string{"content-type": "image/png", "etag": `"it2"`}, pngData) {
		t.Fatal("save item2 failed")
	}
	if !m0.Save("/assets/1795000000/img/item3.png", map[string]string{"content-type": "image/png", "etag": `"it3"`}, pngData) {
		t.Fatal("save item3 failed")
	}
	m0.Close()

	// 2. Synthesize an incomplete V1 legacy snapshot containing ONLY item1
	indexPath := filepath.Join(base, MetaIndexFileName)
	_ = os.Remove(indexPath)

	p1, _ := m0.ResolvePathWithNamespace("gbf", "/assets/1795000000/img/item1.png")
	fi1, err := os.Stat(p1)
	if err != nil {
		t.Fatalf("stat item1: %v", err)
	}

	rawV1 := writeTestMetaIndexV1([]testIndexKV{
		{
			key:       "assets/1795000000/img/item1.png",
			namespace: "gbf",
			entry: diskMetaEntry{
				mtimeNano:       fi1.ModTime().UnixNano(),
				size:            fi1.Size(),
				accessTime:      fi1.ModTime().Unix(),
				contentType:     "image/png",
				contentEncoding: "",
				etag:            `"it1"`,
				exists:          true,
				hasMeta:         true,
				verified:        true,
			},
		},
	})
	if err := os.WriteFile(indexPath, rawV1, 0644); err != nil {
		t.Fatalf("write rawV1 index: %v", err)
	}

	// 3. Load with Manager: verify V1 loads item1, but metaIndexComplete is FALSE
	m1 := NewManager(base, 16)
	m1.waitMetaIndexLoad(0)
	if !m1.metaIndexLoaded.Load() {
		t.Fatal("expected metaIndexLoaded to be true from V1 index")
	}
	if m1.isMetaIndexComplete() {
		t.Fatal("expected legacy V1 index to NOT be certified complete (metaIndexComplete=false)")
	}
	if m1.diskMetaCount() != 1 {
		t.Fatalf("expected 1 item in diskMetaShards from V1 index, got %d", m1.diskMetaCount())
	}

	// 4. Run BackfillMetaIndex: verify it fills remaining items and marks complete
	added := m1.BackfillMetaIndex(nil, nil)
	if added != 2 {
		t.Fatalf("expected 2 newly discovered items from backfill, got %d", added)
	}
	if !m1.isMetaIndexComplete() {
		t.Fatal("expected metaIndexComplete=true after full backfill")
	}
	if m1.diskMetaCount() != 3 {
		t.Fatalf("expected all 3 items indexed in memory, got %d", m1.diskMetaCount())
	}
	m1.Close()

	// 5. Verify the persisted index on disk was upgraded to V2 with FlagMetaIndexComplete
	rawV2, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read upgraded index: %v", err)
	}
	if len(rawV2) < 20 {
		t.Fatalf("upgraded index too short: %d bytes", len(rawV2))
	}
	ver := binary.LittleEndian.Uint32(rawV2[8:12])
	if ver != MetaIndexVersion2 {
		t.Fatalf("expected index version 2, got %d", ver)
	}
	flags := binary.LittleEndian.Uint32(rawV2[12:16])
	if flags&FlagMetaIndexComplete == 0 {
		t.Fatalf("expected FlagMetaIndexComplete in flags, got %d", flags)
	}
	count := binary.LittleEndian.Uint32(rawV2[16:20])
	if count != 3 {
		t.Fatalf("expected 3 items in V2 index, got %d", count)
	}

	// 6. Cold-start a new manager: verify it reads V2 index and immediately has metaIndexComplete=true
	m2 := NewManager(base, 16)
	defer m2.Close()
	m2.waitMetaIndexLoad(0)
	if !m2.isMetaIndexComplete() {
		t.Fatal("expected m2 to load V2 index as certified complete")
	}
	if m2.diskMetaCount() != 3 {
		t.Fatalf("expected 3 items in m2, got %d", m2.diskMetaCount())
	}
}

func TestBoost_WalkDirFallbackWhenIndexIncomplete(t *testing.T) {
	base := t.TempDir()

	// 1. Create 4 physical asset files on disk
	m0 := NewManager(base, 16)
	jsData := []byte("console.log('boost-test');")
	for i := 1; i <= 4; i++ {
		key := fmt.Sprintf("/assets/1795000000/js/file%d.js", i)
		if !m0.Save(key, map[string]string{"content-type": "application/javascript", "etag": fmt.Sprintf(`"f%d"`, i)}, jsData) {
			t.Fatalf("save file %d failed", i)
		}
	}
	m0.Close()

	// 2. Synthesize an incomplete V1 index containing only file1 (1 item)
	indexPath := filepath.Join(base, MetaIndexFileName)
	_ = os.Remove(indexPath)

	p1, _ := m0.ResolvePathWithNamespace("gbf", "/assets/1795000000/js/file1.js")
	fi1, _ := os.Stat(p1)
	rawV1 := writeTestMetaIndexV1([]testIndexKV{
		{
			key:       "assets/1795000000/js/file1.js",
			namespace: "gbf",
			entry: diskMetaEntry{
				mtimeNano:   fi1.ModTime().UnixNano(),
				size:        fi1.Size(),
				accessTime:  fi1.ModTime().Unix(),
				contentType: "application/javascript",
				etag:        `"f1"`,
				exists:      true,
				hasMeta:     true,
				verified:    true,
			},
		},
	})
	if err := os.WriteFile(indexPath, rawV1, 0644); err != nil {
		t.Fatalf("write rawV1 index: %v", err)
	}

	// 3. Start manager with ample budget (e.g. 50MB for ~100 bytes of files)
	m := NewManager(base, 50)
	defer m.Close()
	m.waitMetaIndexLoad(0)
	if m.isMetaIndexComplete() {
		t.Fatal("expected index to be incomplete")
	}
	if m.diskMetaCount() != 1 {
		t.Fatalf("expected only 1 item in memory index, got %d", m.diskMetaCount())
	}

	// 4. Run RAM Boost: verify it does NOT blindly stop at 1 file, but walks the disk
	// and loads all 4 files into the Resident Pool.
	prog := m.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if prog.State != BoostStateCompleted {
		t.Fatalf("expected BoostStateCompleted, got %s (message=%s)", prog.State, prog.Message)
	}
	if prog.LoadedFiles != 4 {
		t.Fatalf("expected 4 files loaded by Boost through WalkDir fallback, got %d (total=%d)", prog.LoadedFiles, prog.TotalFiles)
	}

	// 5. Verify all 4 files are resident in RAM
	for i := 1; i <= 4; i++ {
		key := fmt.Sprintf("/assets/1795000000/js/file%d.js", i)
		if item, src := m.GetWithNamespace("gbf", key); item == nil || src != "RAM-BOOST" {
			t.Fatalf("expected file %d to be resident in RAM, got item=%v src=%q", i, item, src)
		}
	}

	// 6. Verify metaIndexComplete was set to true upon full Boost completion
	if !m.isMetaIndexComplete() {
		t.Fatal("expected metaIndexComplete=true after full Boost completion")
	}
}

type testIndexKV struct {
	key       string
	namespace string
	entry     diskMetaEntry
}

func writeTestMetaIndexV1(items []testIndexKV) []byte {
	var buf bytes.Buffer
	buf.Write(metaIndexMagic[:])
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], 1) // Version 1
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(items)))
	buf.Write(hdr[:])

	for _, it := range items {
		writeStringLP(&buf, it.key)
		writeStringLP(&buf, it.namespace)
		var nums [24]byte
		binary.LittleEndian.PutUint64(nums[0:8], uint64(it.entry.mtimeNano))
		binary.LittleEndian.PutUint64(nums[8:16], uint64(it.entry.size))
		binary.LittleEndian.PutUint64(nums[16:24], uint64(it.entry.accessTime))
		buf.Write(nums[:])
		writeStringLP(&buf, it.entry.contentType)
		writeStringLP(&buf, it.entry.contentEncoding)
		writeStringLP(&buf, it.entry.etag)
		writeStringLP(&buf, it.entry.lastModified)
		var flags byte
		if it.entry.exists {
			flags |= 1
		}
		if it.entry.hasMeta {
			flags |= 2
		}
		if it.entry.verified {
			flags |= 4
		}
		_ = buf.WriteByte(flags)
	}

	crc := crc32.ChecksumIEEE(buf.Bytes())
	var crcBuf [4]byte
	binary.LittleEndian.PutUint32(crcBuf[:], crc)
	buf.Write(crcBuf[:])
	return buf.Bytes()
}
