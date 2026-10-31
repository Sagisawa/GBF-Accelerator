package cache

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gbf-proxy/config"
)

type prefixStats struct {
	prefix string
	count  int
	bytes  int64
	sample string
}

// TestRealCacheCoverage scans a real GBF-Accelerator or ACGPower cache directory
// specified via the GBF_REAL_CACHE_DIR environment variable.
// It reports confirmation rates evaluated strictly on actual asset files, separates
// metadata (.ext) and auxiliary files into their own accounting categories, and lists
// the top 20 unconfirmed asset prefixes.
// When GBF_REAL_CACHE_DIR is not set, the test is safely skipped.
func TestRealCacheCoverage(t *testing.T) {
	cacheDir := os.Getenv("GBF_REAL_CACHE_DIR")
	if cacheDir == "" {
		t.Skip("skipping real cache coverage test: GBF_REAL_CACHE_DIR environment variable not set")
	}

	absDir, err := filepath.Abs(cacheDir)
	if err != nil {
		t.Fatalf("failed to resolve absolute path for GBF_REAL_CACHE_DIR %q: %v", cacheDir, err)
	}

	fi, err := os.Stat(absDir)
	if err != nil {
		t.Fatalf("GBF_REAL_CACHE_DIR %q cannot be accessed: %v", absDir, err)
	}
	if !fi.IsDir() {
		t.Fatalf("GBF_REAL_CACHE_DIR %q is not a directory", absDir)
	}

	hasMarkerOnDisk := HasRootMarker(absDir)

	// Asset files metrics (data items, excluding .ext sidecars and auxiliary files)
	totalAssetFiles := 0
	totalAssetBytes := int64(0)
	actualConfirmedAssetFiles := 0
	actualConfirmedAssetBytes := int64(0)
	potentialConfirmedAssetFiles := 0
	potentialConfirmedAssetBytes := int64(0)
	unconfirmedAssetFiles := 0
	unconfirmedAssetBytes := int64(0)

	// Metadata (.ext) sidecar tracking
	validPairedExtFiles := 0
	validPairedExtBytes := int64(0)
	orphanExtFiles := 0
	orphanExtBytes := int64(0)
	invalidExtFiles := 0
	invalidExtBytes := int64(0)

	// Auxiliary files tracking
	tempFiles := 0
	tempBytes := int64(0)
	quarFiles := 0
	quarBytes := int64(0)
	junkFiles := 0

	prefixMap := make(map[string]*prefixStats)

	walkErr := filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == absDir || d.IsDir() {
			return nil
		}

		baseName := d.Name()
		rel, relErr := filepath.Rel(absDir, path)
		if relErr != nil {
			return nil
		}
		slashRel := filepath.ToSlash(rel)

		if slashRel == RootMarkerFileName {
			return nil
		}
		if config.IsSystemJunk(baseName) {
			junkFiles++
			return nil
		}

		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		fileSize := info.Size()

		// Auxiliary streaming temporary files
		if isAcceleratorTempFile(slashRel) {
			tempFiles++
			tempBytes += fileSize
			return nil
		}

		// Auxiliary quarantined files
		if isAcceleratorQuarantineFile(slashRel) {
			quarFiles++
			quarBytes += fileSize
			return nil
		}

		// Metadata sidecar files (.ext)
		if strings.HasSuffix(slashRel, ".ext") {
			baseSlashRel := strings.TrimSuffix(slashRel, ".ext")
			basePath := strings.TrimSuffix(path, ".ext")
			baseFi, baseErr := os.Lstat(basePath)
			if baseErr == nil && baseFi.Mode().IsRegular() {
				metaBytes, readErr := os.ReadFile(path)
				if readErr == nil {
					if _, ok := VerifyTier1Meta(baseSlashRel, metaBytes); ok {
						validPairedExtFiles++
						validPairedExtBytes += fileSize
						return nil
					}
				}
				invalidExtFiles++
				invalidExtBytes += fileSize
			} else if baseErr != nil && os.IsNotExist(baseErr) {
				orphanExtFiles++
				orphanExtBytes += fileSize
			} else {
				invalidExtFiles++
				invalidExtBytes += fileSize
			}
			return nil
		}

		// Regular data asset file
		totalAssetFiles++
		totalAssetBytes += fileSize

		actualOK := IsConfirmedCacheFileWithInfo(absDir, path, info, hasMarkerOnDisk)
		if actualOK {
			actualConfirmedAssetFiles++
			actualConfirmedAssetBytes += fileSize
		}

		potentialOK := IsConfirmedCacheFileWithInfo(absDir, path, info, true)
		if potentialOK {
			potentialConfirmedAssetFiles++
			potentialConfirmedAssetBytes += fileSize
		} else {
			unconfirmedAssetFiles++
			unconfirmedAssetBytes += fileSize

			cleanRel := strings.TrimPrefix(slashRel, "/")
			segs := strings.Split(cleanRel, "/")
			prefix := "root"
			if len(segs) >= 3 {
				prefix = strings.Join(segs[:2], "/")
			} else if len(segs) == 2 {
				prefix = segs[0]
			}

			entry, exists := prefixMap[prefix]
			if !exists {
				entry = &prefixStats{
					prefix: prefix,
					sample: slashRel,
				}
				prefixMap[prefix] = entry
			}
			entry.count++
			entry.bytes += fileSize
		}

		return nil
	})

	if walkErr != nil {
		t.Fatalf("WalkDir failed on %q: %v", absDir, walkErr)
	}

	// Sort unconfirmed prefixes by file count descending
	prefixList := make([]*prefixStats, 0, len(prefixMap))
	for _, ps := range prefixMap {
		prefixList = append(prefixList, ps)
	}
	sort.Slice(prefixList, func(i, j int) bool {
		if prefixList[i].count != prefixList[j].count {
			return prefixList[i].count > prefixList[j].count
		}
		return prefixList[i].bytes > prefixList[j].bytes
	})

	actualRate := 0.0
	potentialRate := 0.0
	unconfirmedRate := 0.0
	if totalAssetFiles > 0 {
		actualRate = float64(actualConfirmedAssetFiles) / float64(totalAssetFiles) * 100.0
		potentialRate = float64(potentialConfirmedAssetFiles) / float64(totalAssetFiles) * 100.0
		unconfirmedRate = float64(unconfirmedAssetFiles) / float64(totalAssetFiles) * 100.0
	}

	totalExtFiles := validPairedExtFiles + orphanExtFiles + invalidExtFiles
	totalExtBytes := validPairedExtBytes + orphanExtBytes + invalidExtBytes

	t.Logf("\n================ REAL CACHE COVERAGE AUDIT REPORT ================\n")
	t.Logf("Cache Directory:                 %s", absDir)
	t.Logf("Root Marker Present On Disk:     %v", hasMarkerOnDisk)
	t.Logf("")
	t.Logf("--- ASSET CONFIRMATION METRICS (EXCLUDING METADATA) ---")
	t.Logf("Total Asset Files Scanned:       %6d (%8.2f MB)", totalAssetFiles, float64(totalAssetBytes)/(1024*1024))
	t.Logf("Actual Confirmed (Disk Marker=%v): %6d (%6.2f%%, %8.2f MB)",
		hasMarkerOnDisk, actualConfirmedAssetFiles, actualRate, float64(actualConfirmedAssetBytes)/(1024*1024))
	t.Logf("Potential Confirmed (Root Marker): %6d (%6.2f%%, %8.2f MB)",
		potentialConfirmedAssetFiles, potentialRate, float64(potentialConfirmedAssetBytes)/(1024*1024))
	t.Logf("Unconfirmed Assets (Root Marker): %6d (%6.2f%%, %8.2f MB)",
		unconfirmedAssetFiles, unconfirmedRate, float64(unconfirmedAssetBytes)/(1024*1024))
	t.Logf("")
	t.Logf("--- METADATA (.EXT) BREAKDOWN ---")
	t.Logf("Total .ext Metadata Files:       %6d (%8.2f MB)", totalExtFiles, float64(totalExtBytes)/(1024*1024))
	t.Logf("  * Valid Paired .ext:           %6d files (%8.2f MB)", validPairedExtFiles, float64(validPairedExtBytes)/(1024*1024))
	t.Logf("  * Orphan .ext (No data file):  %6d files (%8.2f MB)", orphanExtFiles, float64(orphanExtBytes)/(1024*1024))
	t.Logf("  * Invalid .ext (Failed meta):  %6d files (%8.2f MB)", invalidExtFiles, float64(invalidExtBytes)/(1024*1024))
	t.Logf("")
	t.Logf("--- AUXILIARY & SYSTEM FILES ---")
	t.Logf("Temporary Streaming (.tmp.*):   %6d files (%8.2f MB)", tempFiles, float64(tempBytes)/(1024*1024))
	t.Logf("Quarantine Files (.quarantine.*):%6d files (%8.2f MB)", quarFiles, float64(quarBytes)/(1024*1024))
	t.Logf("System Junk Files (Ignored):     %6d files", junkFiles)

	limit := 20
	if len(prefixList) < limit {
		limit = len(prefixList)
	}
	if limit > 0 {
		t.Logf("\n--- TOP %d UNCONFIRMED ASSET PATH PREFIXES (AFTER ROOT CONFIRMATION) ---", limit)
		for i := 0; i < limit; i++ {
			p := prefixList[i]
			t.Logf("  [%2d] %-32s | count: %6d | size: %8.2f KB | sample: %s",
				i+1, p.prefix, p.count, float64(p.bytes)/1024.0, p.sample)
		}
	} else {
		t.Logf("\n--- 100%% COVERAGE: ALL ASSET FILES CONFIRMED ---")
	}
	t.Logf("==================================================================")
}
