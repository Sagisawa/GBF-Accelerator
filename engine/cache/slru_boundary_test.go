package cache

import (
	"fmt"
	"testing"
)

// Point 1: 前台真实请求首次进入是否正确进入受保护段
func TestSLRU_Boundary_ForegroundEntersProtectedDirectly(t *testing.T) {
	cache := NewLRUCache(1000)

	// Simulate foreground request via Set (which calls set(..., true))
	item := &CacheItem{Key: "combat_effect.js", Data: make([]byte, 200)}
	cache.Set("combat_effect.js", item)

	if !cache.Contains("combat_effect.js") {
		t.Fatal("item not found in cache after Set()")
	}
	if !cache.IsProtected("combat_effect.js") {
		t.Fatal("foreground asset did not enter Protected segment directly")
	}

	shard := cache.getShard("combat_effect.js")
	shard.mu.RLock()
	protB := shard.protBytes
	probB := shard.probBytes
	shard.mu.RUnlock()

	if protB != 200 {
		t.Fatalf("expected protBytes = 200, got %d", protB)
	}
	if probB != 0 {
		t.Fatalf("expected probBytes = 0, got %d", probB)
	}
}

// Point 2: 大量 Prefetch 进入时，是否仅在观察段发生淘汰，受保护段未受污染
func TestSLRU_Boundary_PrefetchEntersProbationAndScanResistance(t *testing.T) {
	// Total cache: 1000 bytes -> 250 bytes probation, 750 bytes protected
	cache := NewLRUCache(1000)

	// 1. Establish 5 protected foreground assets (5 x 100B = 500B <= 750B protected quota)
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("fg_asset_%d.js", i)
		cache.Set(key, &CacheItem{Key: key, Data: make([]byte, 100)})
	}

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("fg_asset_%d.js", i)
		if !cache.IsProtected(key) {
			t.Fatalf("asset %s should be in protected segment", key)
		}
	}

	// 2. Flood with 50 prefetch assets (50 x 80B = 4000B >> 250B probation quota)
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("prefetch_burst_%d.png", i)
		cache.SetProbation(key, &CacheItem{Key: key, Data: make([]byte, 80)})
	}

	// 3. Verify: ALL 5 protected assets MUST STILL EXIST and remain protected
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("fg_asset_%d.js", i)
		if !cache.Contains(key) {
			t.Fatalf("CRITICAL: Protected foreground asset %s was evicted by prefetch flood!", key)
		}
		if !cache.IsProtected(key) {
			t.Fatalf("Protected foreground asset %s lost protected status!", key)
		}
	}

	// Verify that total cache size is still bounded
	items, bytes := cache.Stats()
	if bytes > 1000 {
		t.Fatalf("cache size exceeded maxBytes: %d > 1000", bytes)
	}
	t.Logf("Post-flood: retained %d items, total %d bytes (protected unharmed)", items, bytes)
}

// Point 3: 受保护段满时，淘汰或降级到观察段的行为是否符合预期
func TestSLRU_Boundary_ProtectedOverflow_DemotesToProbation(t *testing.T) {
	// Total cache: 1000 bytes -> 250 bytes probation, 750 bytes protected
	cache := NewLRUCache(1000)

	// Insert 7 items of 100B each into protected (700B <= 750B quota)
	for i := 0; i < 7; i++ {
		key := fmt.Sprintf("item_%d.js", i)
		cache.Set(key, &CacheItem{Key: key, Data: make([]byte, 100)})
	}

	// Now insert the 8th item (800B total in protected > 750B)
	// The oldest item (item_0.js) should be DEMOTED to Probationary
	cache.Set("item_7.js", &CacheItem{Key: "item_7.js", Data: make([]byte, 100)})

	shard := cache.getShard("item_0.js")
	shard.mu.RLock()
	protB := shard.protBytes
	probB := shard.probBytes
	shard.mu.RUnlock()

	if protB > 750 {
		t.Fatalf("protected segment did not respect quota: %d > 750", protB)
	}
	if probB != 100 {
		t.Fatalf("demoted item was not accounted in probBytes: got %d, expected 100", probB)
	}

	// Oldest item should now be in Probationary
	if cache.IsProtected("item_0.js") {
		t.Fatalf("item_0.js should have been demoted to probationary segment")
	}
	if !cache.Contains("item_0.js") {
		t.Fatalf("item_0.js was completely evicted instead of demoted to probationary")
	}

	// When accessed again via Get(), it should be PROMOTED back to Protected
	cache.Get("item_0.js")
	if !cache.IsProtected("item_0.js") {
		t.Fatalf("item_0.js should be promoted back to Protected after Get()")
	}
}

// Point 4: 缓存项更新、删除、Clear() 时，受保护段与观察段的计数、字节数与指针关系是否完全正确
func TestSLRU_Boundary_UpdateDeleteClear_StateAndPointers(t *testing.T) {
	cache := NewLRUCache(2000)
	shard := cache.shards[0] // single shard

	// 1. Insert into probation
	cache.SetProbation("prob_1", &CacheItem{Key: "prob_1", Data: make([]byte, 100)})
	if shard.probBytes != 100 || shard.protBytes != 0 {
		t.Fatalf("probation byte mismatch: prob=%d, prot=%d", shard.probBytes, shard.protBytes)
	}

	// Update in probation with larger item (150B)
	cache.SetProbation("prob_1", &CacheItem{Key: "prob_1", Data: make([]byte, 150)})
	if shard.probBytes != 150 {
		t.Fatalf("update in probation byte mismatch: %d != 150", shard.probBytes)
	}

	// 2. Insert into protected
	cache.Set("prot_1", &CacheItem{Key: "prot_1", Data: make([]byte, 200)})
	if shard.protBytes != 200 {
		t.Fatalf("protected byte mismatch: %d != 200", shard.protBytes)
	}

	// Update in protected with smaller item (120B)
	cache.Set("prot_1", &CacheItem{Key: "prot_1", Data: make([]byte, 120)})
	if shard.protBytes != 120 {
		t.Fatalf("update in protected byte mismatch: %d != 120", shard.protBytes)
	}

	// 3. Delete from probation
	if !cache.Delete("prob_1") {
		t.Fatal("failed to delete prob_1")
	}
	if shard.probBytes != 0 {
		t.Fatalf("probBytes not zero after deletion: %d", shard.probBytes)
	}

	// 4. Delete from protected
	if !cache.Delete("prot_1") {
		t.Fatal("failed to delete prot_1")
	}
	if shard.protBytes != 0 {
		t.Fatalf("protBytes not zero after deletion: %d", shard.protBytes)
	}

	// 5. Populate and Clear()
	for i := 0; i < 5; i++ {
		cache.Set(fmt.Sprintf("k_%d", i), &CacheItem{Key: fmt.Sprintf("k_%d", i), Data: make([]byte, 50)})
		cache.SetProbation(fmt.Sprintf("p_%d", i), &CacheItem{Key: fmt.Sprintf("p_%d", i), Data: make([]byte, 50)})
	}
	cache.Clear()

	items, totalB := cache.Stats()
	if items != 0 || totalB != 0 {
		t.Fatalf("Clear() did not reset stats: items=%d, bytes=%d", items, totalB)
	}

	// Check sentinel pointer integrity
	shard.mu.RLock()
	if shard.probHead.next != shard.probTail || shard.probTail.prev != shard.probHead {
		t.Fatal("probationary sentinels corrupted after Clear()")
	}
	if shard.protHead.next != shard.protTail || shard.protTail.prev != shard.protHead {
		t.Fatal("protected sentinels corrupted after Clear()")
	}
	shard.mu.RUnlock()

	// Verify insertion works normally post-Clear()
	cache.Set("new_item", &CacheItem{Key: "new_item", Data: make([]byte, 80)})
	if !cache.Contains("new_item") || !cache.IsProtected("new_item") {
		t.Fatal("failed to insert after Clear()")
	}
}

// Point 5: 动态修改缓存大小上限时，两段的容量调整与必要淘汰是否正确
func TestSLRU_Boundary_SetMaxBytes_ResizingAndEviction(t *testing.T) {
	// Start with 1000 bytes: 750 protected, 250 probation
	cache := NewLRUCache(1000)

	// Populate 500B protected (5 x 100B)
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("prot_%d", i)
		cache.Set(key, &CacheItem{Key: key, Data: make([]byte, 100)})
	}
	// Populate 200B probation (2 x 100B)
	for i := 0; i < 2; i++ {
		key := fmt.Sprintf("prob_%d", i)
		cache.SetProbation(key, &CacheItem{Key: key, Data: make([]byte, 100)})
	}

	items, bytes := cache.Stats()
	t.Logf("Initial: %d items, %d bytes", items, bytes)
	if bytes != 700 {
		t.Fatalf("expected 700 total bytes, got %d", bytes)
	}

	// Resize down drastically: maxBytes = 350
	// New quotas: probation = 350 / 4 = 87 bytes, protected = 263 bytes
	cache.SetMaxBytes(350)

	newItems, newBytes := cache.Stats()
	t.Logf("After SetMaxBytes(350): %d items, %d bytes", newItems, newBytes)

	if newBytes > 350 {
		t.Fatalf("total bytes after resize exceeded new limit: %d > 350", newBytes)
	}

	shard := cache.shards[0]
	shard.mu.RLock()
	defer shard.mu.RUnlock()

	if shard.protBytes > shard.protMaxBytes {
		t.Fatalf("protected bytes exceeded protMaxBytes: %d > %d", shard.protBytes, shard.protMaxBytes)
	}

	// Pointer integrity walk from head to tail in both segments
	walkCount := 0
	for curr := shard.probHead.next; curr != shard.probTail; curr = curr.next {
		walkCount++
		if walkCount > 100 {
			t.Fatal("cycle detected in probationary list")
		}
	}
	walkCount = 0
	for curr := shard.protHead.next; curr != shard.protTail; curr = curr.next {
		walkCount++
		if walkCount > 100 {
			t.Fatal("cycle detected in protected list")
		}
	}
}

// Point 6: 确认不会因为 PeekRAMWithNamespace、HasCache、Prefetch double-check 等现有调用路径产生意外的错误晋升
func TestSLRU_Boundary_NoAccidentalPromotionFromPeekOrPrefetch(t *testing.T) {
	tempDir := t.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	cleanPath := "assets/sound/bgm_battle.mp3"
	payload := []byte("audio-sample-data")

	// 1. Admit via prefetch -> Probationary
	_, ok := mgr.SavePrefetchWithNamespace("gbf", cleanPath, map[string]string{"content-type": "audio/mp3"}, payload)
	if !ok {
		t.Fatal("failed to save prefetch item")
	}

	ramKey := makeRAMKey("gbf", cleanPath)
	if mgr.ramCache.IsProtected(ramKey) {
		t.Fatal("prefetch item should start in probationary segment")
	}

	// 2. HasCache check should NOT promote
	if !mgr.HasCacheWithNamespace("gbf", cleanPath) {
		t.Fatal("HasCacheWithNamespace returned false for saved item")
	}
	if mgr.ramCache.IsProtected(ramKey) {
		t.Fatal("HasCacheWithNamespace accidentally promoted item to protected!")
	}

	// 3. PeekRAMWithNamespace check should NOT promote
	peekItem := mgr.PeekRAMWithNamespace("gbf", cleanPath)
	if peekItem == nil {
		t.Fatal("PeekRAMWithNamespace returned nil")
	}
	if mgr.ramCache.IsProtected(ramKey) {
		t.Fatal("PeekRAMWithNamespace accidentally promoted item to protected!")
	}

	// 4. Real foreground request via GetWithNamespace SHOULD promote to protected
	getItem, src := mgr.GetWithNamespace("gbf", cleanPath)
	if getItem == nil || src != "RAM" {
		t.Fatalf("expected RAM hit, got %s", src)
	}
	if !mgr.ramCache.IsProtected(ramKey) {
		t.Fatal("real foreground GetWithNamespace failed to promote item to protected!")
	}
}

