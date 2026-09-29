package cache

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestBoostLifecycle_ClearAll(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_clearall_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	// 1. Prewarm with an asset
	assetsDir := filepath.Join(tempDir, "assets", "clearall")
	_ = os.MkdirAll(assetsDir, 0755)
	p := filepath.Join(assetsDir, "test.png")
	data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtest")
	_ = os.WriteFile(p, data, 0644)
	_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if item, src := mgr.GetWithNamespace("gbf", "assets/clearall/test.png"); item == nil || src != "RAM-BOOST" {
		t.Fatalf("expected RAM-BOOST hit before ClearAll, got src=%s", src)
	}

	// 2. Execute ClearAll
	mgr.ClearAll()

	// 3. Verify Resident Pool is cleared
	rItems, rBytes := mgr.ResidentPool().Stats()
	if rItems != 0 || rBytes != 0 {
		t.Fatalf("expected 0 resident items/bytes after ClearAll, got (%d, %d)", rItems, rBytes)
	}

	// 4. Verify request cannot hit Resident Pool
	if item, _ := mgr.GetWithNamespace("gbf", "assets/clearall/test.png"); item != nil {
		t.Fatalf("expected nil item after ClearAll, got %v", item)
	}
}

func TestBoostLifecycle_SetCacheBase(t *testing.T) {
	dir1, _ := os.MkdirTemp("", "gbf_boost_base1_*")
	dir2, _ := os.MkdirTemp("", "gbf_boost_base2_*")
	defer os.RemoveAll(dir1)
	defer os.RemoveAll(dir2)

	mgr := NewManager(dir1, 16)
	defer mgr.Close()

	p1 := filepath.Join(dir1, "assets", "base1.png")
	_ = os.MkdirAll(filepath.Dir(p1), 0755)
	_ = os.WriteFile(p1, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRbase1"), 0644)
	_ = os.WriteFile(p1+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if item, src := mgr.GetWithNamespace("gbf", "assets/base1.png"); item == nil || src != "RAM-BOOST" {
		t.Fatalf("expected RAM-BOOST hit before SetCacheBase, got src=%s", src)
	}

	// Switch cache base to dir2
	mgr.SetCacheBase(dir2)

	// Resident Pool must be cleared immediately
	rItems, _ := mgr.ResidentPool().Stats()
	if rItems != 0 {
		t.Fatalf("expected 0 resident items after SetCacheBase, got %d", rItems)
	}
	if item, _ := mgr.GetWithNamespace("gbf", "assets/base1.png"); item != nil {
		t.Fatalf("expected nil item after SetCacheBase, got %v", item)
	}
}

func TestBoostLifecycle_SaveNewAssetInvalidatesOldResident(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_savenew_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "version")
	_ = os.MkdirAll(assetsDir, 0755)
	p := filepath.Join(assetsDir, "item.png")
	v1Data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRv1data")
	_ = os.WriteFile(p, v1Data, 0644)
	_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	item, src := mgr.GetWithNamespace("gbf", "assets/version/item.png")
	if item == nil || src != "RAM-BOOST" || string(item.Data) != string(v1Data) {
		t.Fatalf("expected v1 from RAM-BOOST, got item=%v, src=%s", item, src)
	}

	// Save new v2 content to the same key via SaveRAMWithNamespace
	v2Data := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRv2data_NEW")
	mgr.SaveRAMWithNamespace("gbf", "assets/version/item.png", map[string]string{
		"content-type": "image/png",
	}, v2Data)

	// Old entry in Resident Pool must have been deleted; new item served from RAM (SLRU)
	itemAfter, srcAfter := mgr.GetWithNamespace("gbf", "assets/version/item.png")
	if itemAfter == nil {
		t.Fatalf("expected non-nil item after save")
	}
	if string(itemAfter.Data) != string(v2Data) {
		t.Fatalf("expected v2 data %q, got %q", string(v2Data), string(itemAfter.Data))
	}
	if srcAfter == "RAM-BOOST" {
		t.Fatalf("expected updated item to not come from stale RAM-BOOST snapshot, got src=%s", srcAfter)
	}
}

func TestBoostLifecycle_AuditInvalidatesCorruptedResident(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_audit_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "audit")
	_ = os.MkdirAll(assetsDir, 0755)
	p := filepath.Join(assetsDir, "clean.png")
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRgood")
	_ = os.WriteFile(p, validPng, 0644)
	_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if item, _ := mgr.GetWithNamespace("gbf", "assets/audit/clean.png"); item == nil {
		t.Fatalf("expected clean item in boost")
	}

	// Corrupt file on disk
	_ = os.WriteFile(p, []byte("corrupted HTML 500 error"), 0644)

	// Run audit
	mgr.AuditAndRepair()

	// Resident entry must be deleted
	if _, ok := mgr.ResidentPool().Get("assets/audit/clean.png"); ok {
		t.Fatalf("expected corrupted file to be removed from resident pool after audit")
	}
}

func TestBoostLifecycle_RepeatedEnableDisable(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_cycle_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	assetsDir := filepath.Join(tempDir, "assets", "cycle")
	_ = os.MkdirAll(assetsDir, 0755)
	p := filepath.Join(assetsDir, "test.png")
	validPng := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRcycle")
	_ = os.WriteFile(p, validPng, 0644)
	_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)

	for i := 0; i < 3; i++ {
		// Enable / Prewarm
		res := mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
		if res.State != BoostStateCompleted {
			t.Fatalf("cycle %d: expected completed boost", i)
		}
		if item, src := mgr.GetWithNamespace("gbf", "assets/cycle/test.png"); item == nil || src != "RAM-BOOST" {
			t.Fatalf("cycle %d: expected RAM-BOOST hit, got src=%s", i, src)
		}

		// Disable
		mgr.DisableBoost()
		if mgr.ResidentPool().Enabled() {
			t.Fatalf("cycle %d: expected pool disabled", i)
		}
		if item, src := mgr.GetWithNamespace("gbf", "assets/cycle/test.png"); item == nil || src == "RAM-BOOST" {
			t.Fatalf("cycle %d: expected non-RAM-BOOST hit after disable, got src=%s", i, src)
		}
	}
}

func TestBoostLifecycle_ConcurrentPrewarmAndDisable(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_concurrent_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 64)
	defer mgr.Close()

	// Seed 50 assets
	assetsDir := filepath.Join(tempDir, "assets", "concurrent")
	_ = os.MkdirAll(assetsDir, 0755)
	for i := 0; i < 50; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("asset_%d.png", i))
		data := []byte(fmt.Sprintf("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRconcurrent_%d", i))
		_ = os.WriteFile(p, data, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	for iter := 0; iter < 30; iter++ {
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
		}()

		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(rand.Intn(2)+1) * time.Millisecond)
			mgr.DisableBoost()
		}()

		wg.Wait()

		// Verify invariant directly without any trailing Disable clean-up:
		if !mgr.ResidentPool().Enabled() {
			items, bytes := mgr.ResidentPool().Stats()
			if items != 0 || bytes != 0 {
				t.Fatalf("iteration %d: expected 0 items/bytes when disabled, got %d items, %d bytes", iter, items, bytes)
			}
			if mgr.RAMBudget().ResidentBytes() != 0 {
				t.Fatalf("iteration %d: expected 0 resident bytes in budget, got %d", iter, mgr.RAMBudget().ResidentBytes())
			}
			if mgr.ramCache.maxBytes != mgr.RAMBudget().TotalBudget() {
				t.Fatalf("iteration %d: expected SLRU maxBytes restored to %d, got %d", iter, mgr.RAMBudget().TotalBudget(), mgr.ramCache.maxBytes)
			}
		} else {
			items, bytes := mgr.ResidentPool().Stats()
			if mgr.RAMBudget().ResidentBytes() != bytes {
				t.Fatalf("iteration %d: budget mismatch when enabled: poolBytes=%d, budgetBytes=%d", iter, bytes, mgr.RAMBudget().ResidentBytes())
			}
			if items < 0 || bytes < 0 {
				t.Fatalf("iteration %d: invalid negative stats: items=%d, bytes=%d", iter, items, bytes)
			}
		}

		ok, resBytes, slruBytes, total := mgr.RAMBudget().ValidateUnifiedBudget()
		if !ok {
			t.Fatalf("iteration %d: unified budget invariant violated: res=%d slru=%d total=%d", iter, resBytes, slruBytes, total)
		}
	}
}

func TestBoostLifecycle_SetRAMLimitBelowResident(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gbf_boost_limit_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr := NewManager(tempDir, 100) // 100 MB limit
	defer mgr.Close()

	// Seed 20 assets of 500 KB each = 10 MB total
	assetsDir := filepath.Join(tempDir, "assets", "limit")
	_ = os.MkdirAll(assetsDir, 0755)
	for i := 0; i < 20; i++ {
		p := filepath.Join(assetsDir, fmt.Sprintf("item_%d.png", i))
		data := make([]byte, 500*1024)
		copy(data, "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRlimit")
		_ = os.WriteFile(p, data, 0644)
		_ = os.WriteFile(p+".ext", []byte(`{"ContentType":"image/png","v":1}`), 0644)
	}

	// Prewarm into resident memory (approx 10 MB)
	res := mgr.PrewarmBoostPoolWithProgress(nil, nil, nil)
	if res.State != BoostStateCompleted {
		t.Fatalf("expected completed boost, got %s", res.State)
	}
	if !mgr.ResidentPool().Enabled() {
		t.Fatalf("expected resident pool enabled")
	}

	resItems, resBytes := mgr.ResidentPool().Stats()
	if resItems != 20 || resBytes < int64(9*1024*1024) {
		t.Fatalf("expected 20 items, >=9MB, got %d items, %d bytes", resItems, resBytes)
	}

	// Reduce RAM limit below current resident occupancy (e.g., to 5 MB)
	mgr.SetRAMLimit(5)

	// Boost must be automatically disabled to prevent totalBudget < residentBytes
	if mgr.ResidentPool().Enabled() {
		t.Fatalf("expected resident pool to be disabled after reducing limit below resident payload")
	}

	items, bytes := mgr.ResidentPool().Stats()
	if items != 0 || bytes != 0 {
		t.Fatalf("expected 0 resident items/bytes after limit reduction, got (%d, %d)", items, bytes)
	}
	if mgr.RAMBudget().ResidentBytes() != 0 {
		t.Fatalf("expected 0 resident bytes in budget, got %d", mgr.RAMBudget().ResidentBytes())
	}
	if mgr.RAMBudget().TotalBudget() != int64(5*1024*1024) {
		t.Fatalf("expected budget total 5MB, got %d", mgr.RAMBudget().TotalBudget())
	}
	ok, rb, sb, tb := mgr.RAMBudget().ValidateUnifiedBudget()
	if !ok {
		t.Fatalf("invariant violated after limit reduction: res=%d, slru=%d, tot=%d", rb, sb, tb)
	}
}


