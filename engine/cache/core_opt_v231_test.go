package cache

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 1. Tests for Disk Hit -> Probation -> Promotion
// ---------------------------------------------------------------------------

func TestSLRU_DiskHitAdmittedToProbationThenPromoted(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetPath := "/assets/js/combat.js"
	cleanPath := "assets/js/combat.js"
	payload := []byte("console.log('combat logic');")
	headers := map[string]string{"content-type": "application/javascript"}

	// 1. Seed to disk
	if !mgr.Save(assetPath, headers, payload) {
		t.Fatal("failed to save asset to disk")
	}

	// 2. Clear RAM to force Disk hit
	mgr.ClearRAM()
	if mgr.PeekRAMWithNamespace("gbf", assetPath) != nil {
		t.Fatal("RAM should be empty after ClearRAM()")
	}

	// 3. First Get: hits Disk
	item, src := mgr.GetWithNamespace("gbf", assetPath)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}

	// SLRU verification: Must be admitted into Probationary segment, NOT Protected
	if mgr.IsRAMProtected("gbf", cleanPath) {
		t.Fatal("first Disk hit was incorrectly admitted directly to Protected segment; must be Probationary")
	}
	if mgr.PeekRAMWithNamespace("gbf", cleanPath) == nil {
		t.Fatal("item was not placed in RAM after Disk hit")
	}

	// 4. Second Get: hits RAM and must be promoted to Protected
	item2, src2 := mgr.GetWithNamespace("gbf", assetPath)
	if item2 == nil || src2 != "RAM" {
		t.Fatalf("expected RAM hit on 2nd access, got src=%s", src2)
	}

	if !mgr.IsRAMProtected("gbf", cleanPath) {
		t.Fatal("second Get (RAM hit) failed to promote item from Probation to Protected segment")
	}
}

func TestSLRU_DiskBurstDoesNotEvictProtectedAssets(t *testing.T) {
	tempDir := t.TempDir()
	// Create manager with 4 MB RAM limit (16 shards = 256 KB per shard)
	mgr := NewManager(tempDir, 4)
	defer mgr.Close()

	headers := map[string]string{"content-type": "application/javascript"}

	// 1. Seed and promote 3 hot assets (each 30 KB, fits comfortably within shard capacity)
	numHot := 3
	hotPayload := makeFakeJS(30 * 1024)
	hotPaths := make([]string, numHot)
	for i := 0; i < numHot; i++ {
		p := fmt.Sprintf("/assets/js/hot_%d.js", i)
		hotPaths[i] = p
		mgr.Save(p, headers, hotPayload)
		// Access twice to ensure it is promoted to Protected
		mgr.Get(p)
		mgr.Get(p)
		clean := strings.TrimPrefix(p, "/")
		if !mgr.IsRAMProtected("gbf", clean) {
			t.Fatalf("hot asset %s should be in Protected segment", p)
		}
	}

	// 2. Seed 30 one-time disk assets directly to disk (NOT in RAM)
	numOneTime := 30
	oneTimePayload := makeFakePNG(40 * 1024)
	headersPNG := map[string]string{"content-type": "image/png"}
	oneTimePaths := make([]string, numOneTime)
	for i := 0; i < numOneTime; i++ {
		p := fmt.Sprintf("/assets/img/onetime_%d.png", i)
		oneTimePaths[i] = p
		clean := strings.TrimPrefix(p, "/")
		filePath, _ := mgr.resolvePathWithNamespace("gbf", clean)
		mgr.saveToDisk(filePath, headersPNG, oneTimePayload, mgr.generation)
	}

	// Do NOT clear RAM: keep hot assets in Protected segment.
	// Now read all 20 one-time assets from disk via Get (simulating cutscene / banner storm).
	for _, p := range oneTimePaths {
		item, src := mgr.Get(p)
		if item == nil || src != "DISK" {
			t.Fatalf("expected DISK hit for %s, got %s", p, src)
		}
	}

	// 3. Verify hot assets are STILL in RAM (Protected segment was not evicted by Disk burst)
	for _, p := range hotPaths {
		clean := strings.TrimPrefix(p, "/")
		peek := mgr.PeekRAMWithNamespace("gbf", clean)
		if peek == nil {
			t.Fatalf("hot asset %s was evicted from RAM by one-time disk burst!", p)
		}
		if !mgr.IsRAMProtected("gbf", clean) {
			t.Fatalf("hot asset %s lost its Protected status", p)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Tests for Disk Read Pre-allocation and Byte-for-Byte Exactness
// ---------------------------------------------------------------------------

func TestDiskRead_ByteExactnessAcrossSizes(t *testing.T) {
	tempDir := t.TempDir()
	sizes := []int{
		16 * 1024,       // 16 KB
		64 * 1024,       // 64 KB
		256 * 1024,      // 256 KB
		1024 * 1024,     // 1 MB
		4 * 1024 * 1024, // 4 MB
	}

	r := rand.New(rand.NewPCG(42, 1337))

	for _, sz := range sizes {
		sz := sz
		t.Run(fmt.Sprintf("Size_%dKB", sz/1024), func(t *testing.T) {
			filePath := filepath.Join(tempDir, fmt.Sprintf("test_%d.dat", sz))
			payload := make([]byte, sz)
			for i := range payload {
				payload[i] = byte(r.IntN(256))
			}
			if err := os.WriteFile(filePath, payload, 0644); err != nil {
				t.Fatal(err)
			}

			f, err := os.Open(filePath)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			fi, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}

			data, err := readDiskFile(f, fi.Size())
			if err != nil {
				t.Fatalf("readDiskFile failed: %v", err)
			}

			if len(data) != sz {
				t.Fatalf("length mismatch: expected %d, got %d", sz, len(data))
			}
			if !bytes.Equal(data, payload) {
				t.Fatal("byte content mismatch: readDiskFile altered data bytes")
			}
		})
	}
}

func TestDiskRead_FileShrinksAfterStat(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "shrink.dat")
	originalPayload := []byte("1234567890abcdefghijklmnopqrstuvwxyz") // 36 bytes
	if err := os.WriteFile(filePath, originalPayload, 0644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	reportedSize := fi.Size() // 36 bytes

	// Another process truncates the file to 10 bytes
	truncatedPayload := originalPayload[:10]
	if err := os.WriteFile(filePath, truncatedPayload, 0644); err != nil {
		t.Fatal(err)
	}

	// readDiskFile with reportedSize=36 on truncated file
	data, err := readDiskFile(f, reportedSize)
	if err != nil {
		t.Fatalf("readDiskFile should handle shrunk file gracefully, got err: %v", err)
	}

	if len(data) != 10 {
		t.Fatalf("expected 10 bytes for shrunk file, got %d", len(data))
	}
	if !bytes.Equal(data, truncatedPayload) {
		t.Fatal("data read does not match actual truncated content on disk")
	}
}

func TestDiskRead_FileGrowsAfterStat(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "grow.dat")
	initialPayload := []byte("Initial-50-bytes-header-content-for-testing-growth.")
	if err := os.WriteFile(filePath, initialPayload, 0644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	initialSize := fi.Size()

	// Append 100 more bytes while f is open
	appendedPayload := append(initialPayload, []byte("---Appended-data-that-makes-the-file-larger-than-initial-stat-reported---")...)
	if err := os.WriteFile(filePath, appendedPayload, 0644); err != nil {
		t.Fatal(err)
	}

	// readDiskFile with initialSize
	data, err := readDiskFile(f, initialSize)
	if err != nil {
		t.Fatalf("readDiskFile failed: %v", err)
	}

	if len(data) != len(appendedPayload) {
		t.Fatalf("byte loss: expected %d bytes, got %d (file growth was truncated!)", len(appendedPayload), len(data))
	}
	if !bytes.Equal(data, appendedPayload) {
		t.Fatal("read content does not match full grown file content")
	}
}

func TestDiskRead_ZeroByteFileRejected(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	emptyFile := filepath.Join(tempDir, "empty.png")
	if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.loadAndValidateDiskItem("gbf", "empty.png", emptyFile)
	if err == nil {
		t.Fatal("expected error for 0-byte file, got nil")
	}
	if !strings.Contains(err.Error(), "empty cache file") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestDiskRead_GzipCompressedContent(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	rawJS := []byte("function gameInit() { console.log('ready'); }")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(rawJS)
	_ = gw.Close()
	gzippedBytes := buf.Bytes()

	path := "/assets/js/bundle.js"
	headers := map[string]string{
		"content-type":     "application/javascript",
		"content-encoding": "gzip",
	}

	if !mgr.Save(path, headers, gzippedBytes) {
		t.Fatal("failed to save gzipped asset")
	}

	mgr.ClearRAM()

	item, src := mgr.Get(path)
	if item == nil || src != "DISK" {
		t.Fatalf("expected DISK hit, got src=%s", src)
	}
	if !bytes.Equal(item.Data, gzippedBytes) {
		t.Fatal("loaded gzip bytes do not match original gzipped bytes")
	}
	if item.ContentEncoding != "gzip" {
		t.Fatalf("expected content-encoding=gzip, got %s", item.ContentEncoding)
	}
}

// ---------------------------------------------------------------------------
// 3. Tests for Validated Save Path (Deduplication)
// ---------------------------------------------------------------------------

func TestSaveRAMValidated_BypassesDuplicateValidation(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	urlPath := "/assets/js/manifest.js"
	cleanPath := "assets/js/manifest.js"
	validJS := []byte("var manifest = { version: '1.0' };")
	headers := map[string]string{"content-type": "application/javascript"}

	// SaveRAMValidated: foreground verified -> enters Protected
	item, ok := mgr.SaveRAMValidatedWithNamespace("gbf", urlPath, headers, validJS)
	if !ok || item == nil {
		t.Fatal("SaveRAMValidatedWithNamespace failed")
	}

	if !mgr.IsRAMProtected("gbf", cleanPath) {
		t.Fatal("SaveRAMValidated should admit to Protected segment directly")
	}

	// SavePrefetchValidated: background verified -> enters Probationary
	prefetchPath := "/assets/js/prefetch_chunk.js"
	prefetchClean := "assets/js/prefetch_chunk.js"
	itemP, okP := mgr.SavePrefetchValidatedWithNamespace("gbf", prefetchPath, headers, validJS)
	if !okP || itemP == nil {
		t.Fatal("SavePrefetchValidatedWithNamespace failed")
	}

	if mgr.IsRAMProtected("gbf", prefetchClean) {
		t.Fatal("SavePrefetchValidatedWithNamespace should admit to Probationary segment, not Protected")
	}

	// Normal public SaveRAMWithNamespace MUST STILL VALIDATE and reject invalid content
	htmlErr := []byte("<html><body>502 Bad Gateway</body></html>")
	_, okBad := mgr.SaveRAMWithNamespace("gbf", "/assets/img/err.png", map[string]string{"content-type": "text/html"}, htmlErr)
	if okBad {
		t.Fatal("normal SaveRAMWithNamespace should have rejected invalid HTML content")
	}
}

func TestManager_ClearRAMAndSetRAMLimitWithProbation(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	p := "/assets/sound/bgm.mp3"
	clean := "assets/sound/bgm.mp3"
	payload := makeFakePNG(20 * 1024)
	headers := map[string]string{"content-type": "audio/mp3"}

	mgr.Save(p, headers, payload)
	mgr.ClearRAM()

	// 1st get: enters probation
	mgr.Get(p)
	if mgr.IsRAMProtected("gbf", clean) {
		t.Fatal("item should be in probation after 1st get")
	}

	// ClearRAM should evict it from RAM
	mgr.ClearRAM()
	if mgr.PeekRAMWithNamespace("gbf", clean) != nil {
		t.Fatal("PeekRAMWithNamespace should return nil after ClearRAM")
	}

	// Re-load and test SetRAMLimit
	mgr.Get(p)
	mgr.SetRAMLimit(1) // 1MB
	// Still accessible
	if item, _ := mgr.Get(p); item == nil {
		t.Fatal("expected item to still be accessible")
	}
}

func TestDiskRead_FileReplacedAfterStat(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "replaced.dat")

	contentA := []byte("Original-content-AAA-1234567890")
	contentB := []byte("Replaced-content-BBB-0987654321-new-and-longer-data-stream")

	if err := os.WriteFile(filePath, contentA, 0644); err != nil {
		t.Fatal(err)
	}

	f, err := os.OpenFile(filePath, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	statSize := fi.Size()

	// Concurrently rewrite content to the file while f is open
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(contentB); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}

	// readDiskFile with original statSize
	data, err := readDiskFile(f, statSize)
	if err != nil {
		t.Fatalf("readDiskFile should handle file replacement gracefully, got: %v", err)
	}

	if len(data) != len(contentB) {
		t.Fatalf("expected length %d, got %d", len(contentB), len(data))
	}
	if !bytes.Equal(data, contentB) {
		t.Fatal("data does not match replacement content")
	}
}

func TestDiskRead_CommonAssetTypes_JS_CSS_JSON_PNG(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cases := []struct {
		urlPath     string
		cleanKey    string
		contentType string
		payload     []byte
	}{
		{
			urlPath:     "/assets/css/style.css",
			cleanKey:    "assets/css/style.css",
			contentType: "text/css",
			payload:     []byte("body { background: #000; color: #fff; margin: 0; }"),
		},
		{
			urlPath:     "/assets/data/quest.json",
			cleanKey:    "assets/data/quest.json",
			contentType: "application/json",
			payload:     []byte(`{"quest_id":1001,"title":"The Final Battle","boss":"Proto Bahamut"}`),
		},
		{
			urlPath:     "/assets/js/engine.js",
			cleanKey:    "assets/js/engine.js",
			contentType: "application/javascript",
			payload:     makeFakeJS(10 * 1024),
		},
		{
			urlPath:     "/assets/img/avatar.png",
			cleanKey:    "assets/img/avatar.png",
			contentType: "image/png",
			payload:     makeFakePNG(15 * 1024),
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.cleanKey, func(t *testing.T) {
			headers := map[string]string{"content-type": tc.contentType}
			if !mgr.Save(tc.urlPath, headers, tc.payload) {
				t.Fatalf("failed to save %s", tc.urlPath)
			}

			// Clear RAM to force disk load
			mgr.ClearRAM()

			// 1st access: hits disk -> enters probation
			item, src := mgr.Get(tc.urlPath)
			if item == nil || src != "DISK" {
				t.Fatalf("expected DISK hit for %s, got %s", tc.urlPath, src)
			}
			if !bytes.Equal(item.Data, tc.payload) {
				t.Fatalf("payload mismatch for %s", tc.urlPath)
			}
			if mgr.IsRAMProtected("gbf", tc.cleanKey) {
				t.Fatalf("%s should be in probation, not protected", tc.cleanKey)
			}

			// 2nd access: hits RAM -> promoted to protected
			item2, src2 := mgr.Get(tc.urlPath)
			if item2 == nil || src2 != "RAM" {
				t.Fatalf("expected RAM hit for %s, got %s", tc.urlPath, src2)
			}
			if !mgr.IsRAMProtected("gbf", tc.cleanKey) {
				t.Fatalf("%s should be promoted to protected on second access", tc.cleanKey)
			}
		})
	}
}

func TestDiskRead_LargeImageAndAudio(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 32)
	defer mgr.Close()

	// 4MB Image
	largeImgPayload := makeFakePNG(4 * 1024 * 1024)
	imgPath := "/assets/img/sp/quest/scene/bg_4mb.png"
	imgClean := "assets/img/sp/quest/scene/bg_4mb.png"
	if !mgr.Save(imgPath, map[string]string{"content-type": "image/png"}, largeImgPayload) {
		t.Fatal("failed to save 4MB image")
	}

	// 4MB Audio
	largeAudioPayload := makeFakePNG(4 * 1024 * 1024) // fake binary data
	audioPath := "/assets/sound/bgm/battle_theme_4mb.mp3"
	audioClean := "assets/sound/bgm/battle_theme_4mb.mp3"
	if !mgr.Save(audioPath, map[string]string{"content-type": "audio/mp3"}, largeAudioPayload) {
		t.Fatal("failed to save 4MB audio")
	}

	mgr.ClearRAM()

	// Verify 4MB Image read from disk
	imgItem, src := mgr.Get(imgPath)
	if imgItem == nil || src != "DISK" {
		t.Fatalf("expected DISK hit for large image, got %s", src)
	}
	if len(imgItem.Data) != len(largeImgPayload) || !bytes.Equal(imgItem.Data, largeImgPayload) {
		t.Fatal("4MB image byte content mismatch")
	}
	if mgr.IsRAMProtected("gbf", imgClean) {
		t.Fatal("large image should be in probation after 1st read")
	}

	// Verify 4MB Audio read from disk
	audioItem, aSrc := mgr.Get(audioPath)
	if audioItem == nil || aSrc != "DISK" {
		t.Fatalf("expected DISK hit for large audio, got %s", aSrc)
	}
	if len(audioItem.Data) != len(largeAudioPayload) || !bytes.Equal(audioItem.Data, largeAudioPayload) {
		t.Fatal("4MB audio byte content mismatch")
	}
	if mgr.IsRAMProtected("gbf", audioClean) {
		t.Fatal("large audio should be in probation after 1st read")
	}
}

func TestDiskRead_WindowsFileSharingBehavior(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "lock_test.png")
	payload := makeFakePNG(1024)
	if err := os.WriteFile(filePath, payload, 0644); err != nil {
		t.Fatal(err)
	}

	// Open file via os.Open
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}

	// On Windows, while f is open without FILE_SHARE_DELETE, os.Remove returns an error.
	// On Linux/POSIX, os.Remove succeeds (unlink while open).
	// This test documents the exact reason why loadAndValidateDiskItem must call f.Close()
	// prior to calling CheckAndQuarantineTamperedJS or os.Remove.
	removeErr := os.Remove(filePath)
	if removeErr == nil {
		// POSIX behavior: unlinked while open, f can still read
		data, err := readDiskFile(f, int64(len(payload)))
		_ = f.Close()
		if err != nil || len(data) != len(payload) {
			t.Fatal("failed to read unlinked file on POSIX")
		}
	} else {
		// Windows behavior: sharing violation as expected
		_ = f.Close()
		if err := os.Remove(filePath); err != nil {
			t.Fatalf("os.Remove after f.Close() failed on Windows: %v", err)
		}
	}
}

func TestSaveRAMValidated_ZeroByteDataRejected(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	headers := map[string]string{"content-type": "image/png"}

	// 1. SaveRAMValidated with 0-byte slice
	item1, ok1 := mgr.SaveRAMValidated("/assets/img/zero.png", headers, []byte{})
	if ok1 || item1 != nil {
		t.Fatal("SaveRAMValidated should have rejected 0-byte slice")
	}

	// 2. SaveRAMValidatedWithNamespace with nil slice
	item2, ok2 := mgr.SaveRAMValidatedWithNamespace("gbf", "/assets/img/zero2.png", headers, nil)
	if ok2 || item2 != nil {
		t.Fatal("SaveRAMValidatedWithNamespace should have rejected nil data")
	}

	// 3. SavePrefetchValidatedWithNamespace with 0-byte slice
	item3, ok3 := mgr.SavePrefetchValidatedWithNamespace("gbf", "/assets/img/zero3.png", headers, []byte{})
	if ok3 || item3 != nil {
		t.Fatal("SavePrefetchValidatedWithNamespace should have rejected 0-byte slice")
	}

	// 4. SavePrefetchValidated with 0-byte slice
	item4, ok4 := mgr.SavePrefetchValidated("/assets/img/zero4.png", headers, []byte{})
	if ok4 || item4 != nil {
		t.Fatal("SavePrefetchValidated should have rejected 0-byte slice")
	}

	// Verify nothing was admitted into RAM
	if mgr.PeekRAMWithNamespace("gbf", "assets/img/zero.png") != nil {
		t.Fatal("0-byte item should not be present in RAM")
	}
}

func TestSaveRAMValidated_GzipValidationIntegrity(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	rawJS := []byte("function battleStart() { console.log('ready'); }")
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, _ = gw.Write(rawJS)
	_ = gw.Close()
	validGzipJS := buf.Bytes()

	headers := map[string]string{
		"content-type":     "application/javascript",
		"content-encoding": "gzip",
	}

	// SaveRAMValidated should save valid gzipped asset
	item, ok := mgr.SaveRAMValidatedWithNamespace("gbf", "/assets/js/battle.js", headers, validGzipJS)
	if !ok || item == nil {
		t.Fatal("SaveRAMValidatedWithNamespace failed on valid gzipped asset")
	}
	if item.ContentEncoding != "gzip" {
		t.Fatalf("expected content-encoding gzip, got %s", item.ContentEncoding)
	}

	// Public SaveRAMWithNamespace should reject gzipped HTML error page
	html502 := []byte("<html><body>502 Bad Gateway</body></html>")
	var bufErr bytes.Buffer
	gwErr := gzip.NewWriter(&bufErr)
	_, _ = gwErr.Write(html502)
	_ = gwErr.Close()
	gzipped502 := bufErr.Bytes()

	headersErr := map[string]string{
		"content-type":     "text/html",
		"content-encoding": "gzip",
	}
	_, okErr := mgr.SaveRAMWithNamespace("gbf", "/assets/js/err.js", headersErr, gzipped502)
	if okErr {
		t.Fatal("SaveRAMWithNamespace should reject gzipped HTML error")
	}
}

func TestDiskRead_WindowsRenameWhileOpen(t *testing.T) {
	tempDir := t.TempDir()
	dstPath := filepath.Join(tempDir, "target.dat")
	tmpPath := filepath.Join(tempDir, "source.tmp")

	data1 := []byte("Initial-target-data-12345")
	data2 := []byte("Replacement-new-data-67890-ABCDE")

	if err := os.WriteFile(dstPath, data1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpPath, data2, 0644); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(dstPath)
	if err != nil {
		t.Fatal(err)
	}

	renameErr := os.Rename(tmpPath, dstPath)
	t.Logf("os.Rename result while dst is open: %v", renameErr)

	// Now read from f: snapshot isolation guarantees data1 is read completely and uncorrupted
	readData, readErr := readDiskFile(f, int64(len(data1)))
	if readErr != nil {
		t.Fatalf("readDiskFile while rename pending failed: %v", readErr)
	}
	if !bytes.Equal(readData, data1) {
		t.Fatal("readDiskFile data corrupted or truncated by pending rename")
	}

	_ = f.Close()

	// After f.Close(), renameWithRetry must succeed
	if err := renameWithRetry(tmpPath, dstPath, 3); err != nil {
		t.Fatalf("renameWithRetry failed after f.Close(): %v", err)
	}

	// Verify dstPath now contains data2
	newData, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("os.ReadFile failed on replaced file: %v", err)
	}
	if !bytes.Equal(newData, data2) {
		t.Fatal("replaced file does not match data2")
	}
}

