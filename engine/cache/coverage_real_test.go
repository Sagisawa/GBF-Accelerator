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
// It reports confirmation rates and lists the top 20 unconfirmed prefixes.
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

	totalFiles := 0
	totalBytes := int64(0)
	confirmedFiles := 0
	confirmedBytes := int64(0)
	unconfirmedFiles := 0
	unconfirmedBytes := int64(0)
	orphanExtFiles := 0
	tempFiles := 0
	quarFiles := 0
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
		if isAcceleratorTempFile(slashRel) {
			tempFiles++
			return nil
		}
		if isAcceleratorQuarantineFile(slashRel) {
			quarFiles++
			return nil
		}

		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}

		fileSize := info.Size()
		totalFiles++
		totalBytes += fileSize

		if strings.HasSuffix(slashRel, ".ext") {
			// Companion .ext or orphan .ext
			basePath := strings.TrimSuffix(path, ".ext")
			if _, err := os.Stat(basePath); os.IsNotExist(err) {
				orphanExtFiles++
			}
			// Ext files are metadata sidecars
			confirmedFiles++
			confirmedBytes += fileSize
			return nil
		}

		// Evaluate asset attribution assuming root marker is present (evaluates Tier-1 and Tier-2)
		if IsConfirmedCacheFileWithInfo(absDir, path, info, true) {
			confirmedFiles++
			confirmedBytes += fileSize
		} else {
			unconfirmedFiles++
			unconfirmedBytes += fileSize

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

	confirmRate := 0.0
	if totalFiles > 0 {
		confirmRate = float64(confirmedFiles) / float64(totalFiles) * 100.0
	}

	t.Logf("\n================ REAL CACHE COVERAGE AUDIT REPORT ================\n")
	t.Logf("Cache Directory:       %s", absDir)
	t.Logf("Root Marker On Disk:   %v", hasMarkerOnDisk)
	t.Logf("Total Files Scanned:   %d (%.2f MB)", totalFiles, float64(totalBytes)/(1024*1024))
	t.Logf("Confirmed Cache Files: %d (%.2f%%, %.2f MB)", confirmedFiles, confirmRate, float64(confirmedBytes)/(1024*1024))
	t.Logf("Unconfirmed Files:     %d (%.2f%%, %.2f MB)", unconfirmedFiles, 100.0-confirmRate, float64(unconfirmedBytes)/(1024*1024))
	t.Logf("Metadata & Temp Files: orphan .ext: %d, temp streaming: %d, quarantine: %d, system junk: %d",
		orphanExtFiles, tempFiles, quarFiles, junkFiles)

	limit := 20
	if len(prefixList) < limit {
		limit = len(prefixList)
	}
	if limit > 0 {
		t.Logf("\n--- TOP %d UNCONFIRMED PATH PREFIXES ---", limit)
		for i := 0; i < limit; i++ {
			p := prefixList[i]
			t.Logf("  [%2d] %-30s | count: %6d | size: %8.2f KB | sample: %s",
				i+1, p.prefix, p.count, float64(p.bytes)/1024, p.sample)
		}
	} else {
		t.Logf("\n--- 100%% COVERAGE: NO UNCONFIRMED FILES DETECTED ---")
	}
	t.Logf("==================================================================\n")
}
