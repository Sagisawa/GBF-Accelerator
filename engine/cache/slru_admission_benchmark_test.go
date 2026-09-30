package cache

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// WorkloadMetrics records detailed operational telemetry for cache evaluation.
type WorkloadMetrics struct {
	RAMHits         int64
	DiskHits        int64
	Misses          int64
	TotalRequests   int64
	HotAssetHits    int64
	HotAssetTotal   int64
	TotalLatency    time.Duration
	DiskReadTime    time.Duration
}

func (m *WorkloadMetrics) RAMHitRate() float64 {
	if m.TotalRequests == 0 {
		return 0
	}
	return float64(m.RAMHits) / float64(m.TotalRequests) * 100
}

func (m *WorkloadMetrics) HotAssetRetentionRate() float64 {
	if m.HotAssetTotal == 0 {
		return 0
	}
	return float64(m.HotAssetHits) / float64(m.HotAssetTotal) * 100
}

func (m *WorkloadMetrics) AvgDiskHitLatency() time.Duration {
	if m.DiskHits == 0 {
		return 0
	}
	return m.DiskReadTime / time.Duration(m.DiskHits)
}

type AdmissionStrategy int

const (
	StrategyA_DiskHitProtected AdmissionStrategy = iota // Old baseline: Disk Hit -> Direct Protected
	StrategyB_DiskHitProbation                          // New policy: Disk Hit -> Probation
)

// runScenarioComparison seeds a temp directory once and evaluates Strategy A vs Strategy B
// on identical disk assets, halving disk writes and eliminating filesystem variance.
func runScenarioComparison(
	b *testing.B,
	title string,
	ramMB int,
	numHot int,
	numWarm int,
	numColdOneTime int,
	rounds int,
	burstColdSize int,
) {
	tempDir := b.TempDir()
	seedMgr := NewManager(tempDir, ramMB)

	hotPayload := makeFakeJS(25 * 1024)   // 25 KB each
	warmPayload := makeFakePNG(35 * 1024) // 35 KB each
	coldPayload := makeFakePNG(50 * 1024) // 50 KB each

	headersJS := map[string]string{"content-type": "application/javascript"}
	headersPNG := map[string]string{"content-type": "image/png"}

	// Seed hot assets on disk
	hotPaths := make([]string, numHot)
	for i := 0; i < numHot; i++ {
		hotPaths[i] = fmt.Sprintf("/assets/js/hot_%d.js", i)
		filePath, _ := seedMgr.resolvePathWithNamespace("gbf", strings.TrimPrefix(hotPaths[i], "/"))
		seedMgr.saveToDisk(filePath, headersJS, hotPayload, seedMgr.generation)
	}

	// Seed warm assets on disk
	warmPaths := make([]string, numWarm)
	for i := 0; i < numWarm; i++ {
		warmPaths[i] = fmt.Sprintf("/assets/img/warm_%d.png", i)
		filePath, _ := seedMgr.resolvePathWithNamespace("gbf", strings.TrimPrefix(warmPaths[i], "/"))
		seedMgr.saveToDisk(filePath, headersPNG, warmPayload, seedMgr.generation)
	}

	// Seed cold one-time assets on disk
	coldPaths := make([]string, numColdOneTime)
	for i := 0; i < numColdOneTime; i++ {
		coldPaths[i] = fmt.Sprintf("/assets/img/cold_%d.png", i)
		filePath, _ := seedMgr.resolvePathWithNamespace("gbf", strings.TrimPrefix(coldPaths[i], "/"))
		seedMgr.saveToDisk(filePath, headersPNG, coldPayload, seedMgr.generation)
	}

	// Seed burst assets on disk
	burstPaths := make([]string, burstColdSize)
	for i := 0; i < burstColdSize; i++ {
		burstPaths[i] = fmt.Sprintf("/assets/img/burst_%d.png", i)
		filePath, _ := seedMgr.resolvePathWithNamespace("gbf", strings.TrimPrefix(burstPaths[i], "/"))
		seedMgr.saveToDisk(filePath, headersPNG, coldPayload, seedMgr.generation)
	}
	seedMgr.Close()

	runWorkload := func(strategy AdmissionStrategy) WorkloadMetrics {
		mgr := NewManager(tempDir, ramMB)
		defer mgr.Close()
		var metrics WorkloadMetrics

		getWithStrategy := func(path string, isHot bool) {
			t0 := time.Now()
			metrics.TotalRequests++
			if isHot {
				metrics.HotAssetTotal++
			}

			cleanKey := strings.TrimPrefix(path, "/")
			ramKey := makeRAMKey("gbf", cleanKey)

			// 1. RAM check
			if item, ok := mgr.ramCache.Get(ramKey); ok && item != nil {
				metrics.RAMHits++
				if isHot {
					metrics.HotAssetHits++
				}
				metrics.TotalLatency += time.Since(t0)
				return
			}

			// 2. Disk check
			diskT0 := time.Now()
			filePath, ok := mgr.resolvePathWithNamespace("gbf", cleanKey)
			if ok {
				item, err := mgr.loadAndValidateDiskItem("gbf", cleanKey, filePath)
				if err == nil && item != nil {
					diskElapsed := time.Since(diskT0)
					metrics.DiskHits++
					metrics.DiskReadTime += diskElapsed

					if strategy == StrategyA_DiskHitProtected {
						mgr.ramCache.Set(ramKey, item)
					} else {
						mgr.ramCache.SetProbation(ramKey, item)
					}
					metrics.TotalLatency += time.Since(t0)
					return
				}
			}

			metrics.Misses++
			metrics.TotalLatency += time.Since(t0)
		}

		// Initial warm-up for hot assets (session startup)
		for _, p := range hotPaths {
			getWithStrategy(p, true)
			// Access twice to ensure baseline promotion to protected
			getWithStrategy(p, true)
		}

		coldIdx := 0
		rGen := rand.New(rand.NewPCG(12345, 67890))

		for r := 0; r < rounds; r++ {
			// 1. Repeated hot access (every round)
			for _, hp := range hotPaths {
				getWithStrategy(hp, true)
			}

			// 2. Warm access (sub-sampled, 30% chance per warm item)
			for _, wp := range warmPaths {
				if rGen.IntN(10) < 3 {
					getWithStrategy(wp, false)
				}
			}

			// 3. Cold one-time disk reads (1-2 per round)
			if coldIdx < len(coldPaths) {
				getWithStrategy(coldPaths[coldIdx], false)
				coldIdx++
			}

			// 4. Repeated hot access within round (e.g. multi-turn combat)
			for k := 0; k < 5; k++ {
				idx := rGen.IntN(numHot)
				getWithStrategy(hotPaths[idx], true)
			}

			// 5. Trigger burst cold pollution at round 10 if configured
			if r == 10 && burstColdSize > 0 {
				for _, bp := range burstPaths {
					getWithStrategy(bp, false)
				}
			}
		}

		return metrics
	}

	mA := runWorkload(StrategyA_DiskHitProtected)
	mB := runWorkload(StrategyB_DiskHitProbation)
	logComparison(b, title, mA, mB)
}

// ---------------------------------------------------------------------------
// Benchmark Scenarios covering all 5 requested cases
// ---------------------------------------------------------------------------

func BenchmarkAdmissionScenarios(b *testing.B) {
	// Scenario 1: Normal Capacity (16 MB, comfortable cache)
	b.Run("Scenario1_NormalCapacity", func(b *testing.B) {
		runScenarioComparison(b, "Scenario 1: Normal Capacity (16 MB)", 16, 30, 40, 50, 25, 0)
	})

	// Scenario 2: Small RAM Capacity (2 MB, constrained cache with high pressure)
	b.Run("Scenario2_SmallRAMCapacity", func(b *testing.B) {
		runScenarioComparison(b, "Scenario 2: Small RAM Capacity (2 MB)", 2, 30, 40, 50, 25, 0)
	})

	// Scenario 3: Hot/Warm/Cold Mixed Access (4 MB, realistic active gameplay)
	b.Run("Scenario3_HotWarmColdMixed", func(b *testing.B) {
		runScenarioComparison(b, "Scenario 3: Hot/Warm/Cold Mixed Access", 4, 25, 50, 80, 30, 0)
	})

	// Scenario 4: Continuous Long Sessions (4 MB, 100 rounds)
	b.Run("Scenario4_ContinuousLongSessions", func(b *testing.B) {
		runScenarioComparison(b, "Scenario 4: Continuous Long Sessions (100 rounds)", 4, 30, 40, 100, 100, 0)
	})

	// Scenario 5: One-Off Resource Burst Pollution (4 MB, 100-item cutscene flood)
	b.Run("Scenario5_BurstPollution", func(b *testing.B) {
		runScenarioComparison(b, "Scenario 5: One-Off Resource Burst Pollution", 4, 30, 20, 30, 25, 100)
	})
}

func logComparison(b *testing.B, title string, a, bMetrics WorkloadMetrics) {
	b.Logf("\n=== %s ===\n"+
		"| Metric                    | Strategy A (Disk -> Protected) | Strategy B (Disk -> Probation) | Delta (B vs A)         |\n"+
		"|---------------------------|--------------------------------|--------------------------------|------------------------|\n"+
		"| Total Requests            | %-30d | %-30d | %-22d |\n"+
		"| RAM Hits                  | %-30d | %-30d | %+22d |\n"+
		"| Disk Hits                 | %-30d | %-30d | %+22d |\n"+
		"| RAM Hit Rate              | %-29.2f%% | %-29.2f%% | %+21.2f%% |\n"+
		"| Hot Asset Retention Rate  | %-29.2f%% | %-29.2f%% | %+21.2f%% |\n"+
		"| Avg Disk Hit Cost         | %-30v | %-30v | %-22v |\n"+
		"| Total Workload Latency    | %-30v | %-30v | %-22v |\n",
		title,
		a.TotalRequests, bMetrics.TotalRequests, bMetrics.TotalRequests-a.TotalRequests,
		a.RAMHits, bMetrics.RAMHits, bMetrics.RAMHits-a.RAMHits,
		a.DiskHits, bMetrics.DiskHits, bMetrics.DiskHits-a.DiskHits,
		a.RAMHitRate(), bMetrics.RAMHitRate(), bMetrics.RAMHitRate()-a.RAMHitRate(),
		a.HotAssetRetentionRate(), bMetrics.HotAssetRetentionRate(), bMetrics.HotAssetRetentionRate()-a.HotAssetRetentionRate(),
		a.AvgDiskHitLatency(), bMetrics.AvgDiskHitLatency(), bMetrics.AvgDiskHitLatency()-a.AvgDiskHitLatency(),
		a.TotalLatency, bMetrics.TotalLatency, bMetrics.TotalLatency-a.TotalLatency,
	)
}

func BenchmarkSingleDiskHitCost(b *testing.B) {
	tempDir := b.TempDir()
	mgr := NewManager(tempDir, 16)
	defer mgr.Close()

	payload := makeFakePNG(64 * 1024)
	headers := map[string]string{"content-type": "image/png"}
	testPath := "/assets/img/bench_disk.png"
	cleanKey := strings.TrimPrefix(testPath, "/")
	filePath, _ := mgr.resolvePathWithNamespace("gbf", cleanKey)
	mgr.saveToDisk(filePath, headers, payload, mgr.generation)

	b.Run("StrategyA_DiskHitDirectProtected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mgr.ClearRAM()
			item, _ := mgr.loadAndValidateDiskItem("gbf", cleanKey, filePath)
			mgr.ramCache.Set(makeRAMKey("gbf", cleanKey), item)
		}
	})

	b.Run("StrategyB_DiskHitProbation", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			mgr.ClearRAM()
			item, _ := mgr.loadAndValidateDiskItem("gbf", cleanKey, filePath)
			mgr.ramCache.SetProbation(makeRAMKey("gbf", cleanKey), item)
		}
	})
}
