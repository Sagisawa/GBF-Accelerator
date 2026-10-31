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
// It reports confirmation rates under both actual on-disk conditions and potential
// post-confirmation conditions, properly tracks valid paired .ext vs orphan and invalid .ext,
// and lists the top 20 unconfirmed prefixes.
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

	totalRegularFiles := 0
	totalRegularBytes := int64(0)

	// Actual on-disk confirmation (hasRootMarker = hasMarkerOnDisk)
	actualConfirmedFiles := 0
	actualConfirmedBytes := int64(0)

	// Potential post-confirmation (hasRootMarker = true)
	potentialConfirmedFiles := 0
	potentialConfirmedBytes := int64(0)
	potentialUnconfirmedFiles := 0
	potentialUnconfirmedBytes := int64(0)

	// Metadata & auxiliary file tracking
	pairedExtFiles := 0
	pairedExtBytes := int64(0)
	orphanExtFiles := 0
	orphanExtBytes := int64(0)
	invalidExtFiles := 0
	invalidExtBytes := int64(0)

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

		if isAcceleratorTempFile(slashRel) {
			tempFiles++
			tempBytes += fileSize
			totalRegularFiles++
			totalRegularBytes += fileSize
			return nil
		}
		if isAcceleratorQuarantineFile(slashRel) {
			quarFiles++
			quarBytes += fileSize
			totalRegularFiles++
			totalRegularBytes += fileSize
			return nil
		}

		totalRegularFiles++
		totalRegularBytes += fileSize

		// Handle .ext metadata files separately
		if strings.HasSuffix(slashRel, ".ext") {
			baseSlashRel := strings.TrimSuffix(slashRel, ".ext")
			basePath := strings.TrimSuffix(path, ".ext")
			baseFi, baseErr := os.Lstat(basePath)
			if baseErr == nil && baseFi.Mode().IsRegular() {
				metaBytes, readErr := os.ReadFile(path)
				if readErr == nil {
					if _, ok := VerifyTier1Meta(baseSlashRel, metaBytes); ok {
						// Only valid paired .ext files count as confirmed metadata!
						pairedExtFiles++
						pairedExtBytes += fileSize
						actualConfirmedFiles++
						actualConfirmedBytes += fileSize
						potentialConfirmedFiles++
						potentialConfirmedBytes += fileSize
						return nil
					}
				}
				// Companion exists, but meta parsing or validation failed
				invalidExtFiles++
				invalidExtBytes += fileSize
			} else if baseErr != nil && os.IsNotExist(baseErr) {
				// No companion data file -> orphan .ext
				orphanExtFiles++
				orphanExtBytes += fileSize
			} else {
				invalidExtFiles++
				invalidExtBytes += fileSize
			}
			return nil
		}

		// Regular data asset:
		actualOK := IsConfirmedCacheFileWithInfo(absDir, path, info, hasMarkerOnDisk)
		if actualOK {
			actualConfirmedFiles++
			actualConfirmedBytes += fileSize
		}

		potentialOK := IsConfirmedCacheFileWithInfo(absDir, path, info, true)
		if potentialOK {
			potentialConfirmedFiles++
			potentialConfirmedBytes += fileSize
		} else {
			potentialUnconfirmedFiles++
			potentialUnconfirmedBytes += fileSize

			// Compute prefix (first 2 or 3 directory segments)
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
	if totalRegularFiles > 0 {
		actualRate = float64(actualConfirmedFiles) / float64(totalRegularFiles) * 100.0
		potentialRate = float64(potentialConfirmedFiles) / float64(totalRegularFiles) * 100.0
		unconfirmedRate = float64(potentialUnconfirmedFiles) / float64(totalRegularFiles) * 100.0
	}

	t.Logf("\n================ REAL CACHE COVERAGE AUDIT REPORT ================\n")
	t.Logf("Cache Directory:             %s", absDir)
	t.Logf("Root Marker Present On Disk: %v", hasMarkerOnDisk)
	t.Logf("Total Regular Files Scanned: %d (%.2f MB)", totalRegularFiles, float64(totalRegularBytes)/(1024*1024))
	t.Logf("")
	t.Logf("--- CONFIRMATION METRICS ---")
	t.Logf("Actual Confirmed (Disk Marker=%v):     %6d (%6.2f%%, %8.2f MB)",
		hasMarkerOnDisk, actualConfirmedFiles, actualRate, float64(actualConfirmedBytes)/(1024*1024))
	t.Logf("Potential Confirmed (Root Confirmed):   %6d (%6.2f%%, %8.2f MB)",
		potentialConfirmedFiles, potentialRate, float64(potentialConfirmedBytes)/(1024*1024))
	t.Logf("Unconfirmed Files (Root Confirmed):     %6d (%6.2f%%, %8.2f MB)",
		potentialUnconfirmedFiles, unconfirmedRate, float64(potentialUnconfirmedBytes)/(1024*1024))
	t.Logf("")
	t.Logf("--- METADATA & AUXILIARY BREAKDOWN ---")
	t.Logf("Valid Paired .ext (Confirmed Meta):     %6d files (%8.2f MB)", pairedExtFiles, float64(pairedExtBytes)/(1024*1024))
	t.Logf("Orphan .ext (No companion asset):       %6d files (%8.2f MB)", orphanExtFiles, float64(orphanExtBytes)/(1024*1024))
	t.Logf("Invalid .ext (Failed Tier-1 meta):      %6d files (%8.2f MB)", invalidExtFiles, float64(invalidExtBytes)/(1024*1024))
	t.Logf("Temporary Streaming (.tmp.*):           %6d files (%8.2f MB)", tempFiles, float64(tempBytes)/(1024*1024))
	t.Logf("Quarantine Files (.quarantine.*):       %6d files (%8.2f MB)", quarFiles, float64(quarBytes)/(1024*1024))
	t.Logf("System Junk Files (Ignored):            %6d files", junkFiles)

	limit := 20
	if len(prefixList) < limit {
		limit = len(prefixList)
	}
	if limit > 0 {
		t.Logf("\n--- TOP %d UNCONFIRMED PATH PREFIXES (AFTER ROOT CONFIRMATION) ---", limit)
		for i := 0; i < limit; i++ {
			p := prefixList[i]
			t.Logf("  [%2d] %-30s | count: %6d | size: %8.2f KB | sample: %s",
				i+1, p.prefix, p.count, float64(p.bytes)/1024.0, p.sample)
		}
	} else {
		t.Logf("\n--- 100%% COVERAGE: NO UNCONFIRMED FILES DETECTED ---")
	}
	t.Logf("==================================================================")
}
