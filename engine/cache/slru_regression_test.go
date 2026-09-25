package cache

import (
	"fmt"
	"testing"
)

// TestSLRU_ProbationPromotionAndScanResistance verifies the core invariants of
// Segmented LRU:
// 1. New items enter the Probationary list (isProtected = false).
// 2. A subsequent Get promotes the item to Protected (isProtected = true).
// 3. A burst of cold items fills and evicts from Probationary, but CANNOT evict
//    protected items as long as protected quota is respected.
func TestSLRU_ProbationPromotionAndScanResistance(t *testing.T) {
	// 1000 bytes cache: 250 bytes probationary, 750 bytes protected
	lru := NewLRUCache(1000)

	// Step 1: Add item via SetProbation (simulating background prefetch)
	probItem := &CacheItem{Key: "prefetch_sound.mp3", Data: make([]byte, 100)}
	lru.SetProbation("prefetch_sound.mp3", probItem)

	if lru.IsProtected("prefetch_sound.mp3") {
		t.Fatal("prefetch item should start in probationary segment, not protected")
	}

	// Step 2: Access probationary item (simulating player triggering the asset) -> should promote to protected
	item, ok := lru.Get("prefetch_sound.mp3")
	if !ok || item == nil {
		t.Fatal("failed to get probationary item")
	}
	if !lru.IsProtected("prefetch_sound.mp3") {
		t.Fatal("item should have been promoted to protected after Get()")
	}

	// Step 3: Add hot item via Set (simulating foreground direct request) -> enters protected directly
	hotItem := &CacheItem{Key: "hot_combat.js", Data: make([]byte, 100)}
	lru.Set("hot_combat.js", hotItem)
	if !lru.IsProtected("hot_combat.js") {
		t.Fatal("foreground item should start directly in protected segment")
	}

	// Step 4: Flood the cache with a prefetch burst of 20 cold items (each 80 bytes = 1600 bytes total)
	for i := 0; i < 20; i++ {
		coldKey := fmt.Sprintf("cold_audio_%d.mp3", i)
		coldItem := &CacheItem{Key: coldKey, Data: make([]byte, 80)}
		lru.SetProbation(coldKey, coldItem)
	}

	// Step 4: Verify that the hot protected items were completely unharmed!
	if !lru.Contains("hot_combat.js") {
		t.Fatal("CRITICAL: hot_combat.js was evicted by cold prefetch burst! Scan resistance failed")
	}
	if !lru.IsProtected("hot_combat.js") {
		t.Fatal("hot_combat.js lost its protected status")
	}

	if !lru.Contains("prefetch_sound.mp3") {
		t.Fatal("CRITICAL: prefetch_sound.mp3 was evicted! Promoted protected item should survive cold burst")
	}
	if !lru.IsProtected("prefetch_sound.mp3") {
		t.Fatal("prefetch_sound.mp3 lost its protected status")
	}
}


