package cache

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu           sync.RWMutex
	cacheBase    string
	ramCache     *LRUCache
	sf           *SingleFlight
	missingCache map[string]struct{}
	missingMu    sync.RWMutex
}

var mimeFallbacks = map[string]string{
	".js":    "application/javascript",
	".css":   "text/css",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".webp":  "image/webp",
	".gif":   "image/gif",
	".svg":   "image/svg+xml",
	".mp3":   "audio/mpeg",
	".wav":   "audio/wav",
	".ogg":   "audio/ogg",
	".m4a":   "audio/mp4",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".json":  "application/json",
	".wasm":  "application/wasm",
}

func NewManager(cacheBase string, ramMaxMB int) *Manager {
	if ramMaxMB <= 0 {
		ramMaxMB = 256
	}
	return &Manager{
		cacheBase:    cacheBase,
		ramCache:     NewLRUCache(int64(ramMaxMB) * 1024 * 1024),
		sf:           NewSingleFlight(),
		missingCache: make(map[string]struct{}),
	}
}

func (m *Manager) SetCacheBase(base string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cacheBase = base
	m.ramCache.Clear()
	m.missingMu.Lock()
	m.missingCache = make(map[string]struct{})
	m.missingMu.Unlock()
}

func (m *Manager) GetCacheBase() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cacheBase
}

func (m *Manager) SetRAMLimit(maxMB int) {
	m.ramCache.SetMaxBytes(int64(maxMB) * 1024 * 1024)
}

func (m *Manager) SingleFlight() *SingleFlight {
	return m.sf
}

func (m *Manager) resolvePath(urlPath string) (string, bool) {
	clean := strings.TrimSpace(strings.Split(urlPath, "?")[0])
	clean = filepath.Clean(filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	// Security: Prevent path traversal
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "C:") || strings.HasPrefix(clean, "c:") {
		return "", false
	}

	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	target := filepath.Join(base, clean)
	rel, err := filepath.Rel(base, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return target, true
}

func (m *Manager) Get(urlPath string) (*CacheItem, string) {
	cleanKey := strings.TrimPrefix(strings.Split(urlPath, "?")[0], "/")

	// 1. Check RAM Cache
	if item, ok := m.ramCache.Get(cleanKey); ok {
		return item, "RAM"
	}

	// Negative cache check
	m.missingMu.RLock()
	_, missing := m.missingCache[cleanKey]
	m.missingMu.RUnlock()
	if missing {
		return nil, ""
	}

	// 2. Check Disk Cache
	filePath, ok := m.resolvePath(cleanKey)
	if !ok {
		return nil, ""
	}

	fi, err := os.Stat(filePath)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		// Try fallback: prepend or strip "assets"
		var altPath string
		if !strings.HasPrefix(cleanKey, "assets") {
			altPath, _ = m.resolvePath("assets/" + cleanKey)
		} else {
			altPath, _ = m.resolvePath(strings.TrimPrefix(cleanKey, "assets/"))
		}
		if altPath != "" {
			if fi2, err2 := os.Stat(altPath); err2 == nil && !fi2.IsDir() && fi2.Size() > 0 {
				filePath = altPath
				fi = fi2
			} else {
				m.markMissing(cleanKey)
				return nil, ""
			}
		} else {
			m.markMissing(cleanKey)
			return nil, ""
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil || len(data) == 0 {
		m.markMissing(cleanKey)
		return nil, ""
	}

	// Read metadata from .ext
	extPath := filePath + ".ext"
	contentType := ""
	contentEncoding := ""
	etag := ""
	lastMod := ""

	if metaBytes, err := os.ReadFile(extPath); err == nil {
		var meta map[string]interface{}
		if err := json.Unmarshal(metaBytes, &meta); err == nil {
			if ct, ok := meta["ct"].(string); ok {
				contentType = ct
			}
			if ce, ok := meta["ce"].(string); ok {
				contentEncoding = ce
			}
			if et, ok := meta["ETag"].(string); ok {
				etag = et
			}
			if lm, ok := meta["LastModified"].(string); ok {
				lastMod = lm
			}
		}
	}

	if contentType == "" {
		ext := strings.ToLower(filepath.Ext(filePath))
		contentType = mimeFallbacks[ext]
		if contentType == "" {
			contentType = mime.TypeByExtension(ext)
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
	}

	if !IsValidCacheContent(cleanKey, contentType, data) {
		m.markMissing(cleanKey)
		return nil, ""
	}

	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", fi.ModTime().Unix(), len(data))
	}

	// Check gzip signature
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		contentEncoding = "gzip"
	}

	item := &CacheItem{
		Key:             cleanKey,
		Data:            data,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            int64(len(data)),
	}

	// Store into RAM cache
	m.ramCache.Set(cleanKey, item)
	return item, "DISK"
}

func (m *Manager) GetFallback(urlPath string) (*CacheItem, string) {
	// First check direct
	if item, src := m.Get(urlPath); item != nil {
		return item, src
	}

	clean := strings.TrimPrefix(strings.Split(urlPath, "?")[0], "/")

	// Versioned asset fallback: /(assets(?:_(?:en|jp))?)/(\d+)/(.+)
	reVer := regexp.MustCompile(`^(assets(?:_(?:en|jp))?)/(\d+)/(.+)$`)
	if mVer := reVer.FindStringSubmatch(clean); len(mVer) == 4 {
		prefix, reqVer, subpath := mVer[1], mVer[2], mVer[3]
		m.mu.RLock()
		base := m.cacheBase
		m.mu.RUnlock()

		prefixDir := filepath.Join(base, prefix)
		entries, err := os.ReadDir(prefixDir)
		if err == nil {
			var versions []string
			for _, e := range entries {
				if e.IsDir() && e.Name() != reqVer {
					if _, err := strconv.Atoi(e.Name()); err == nil {
						versions = append(versions, e.Name())
					}
				}
			}
			sort.Slice(versions, func(i, j int) bool {
				v1, _ := strconv.Atoi(versions[i])
				v2, _ := strconv.Atoi(versions[j])
				return v1 > v2
			})

			for _, v := range versions {
				candUrl := fmt.Sprintf("/%s/%s/%s", prefix, v, subpath)
				if item, _ := m.Get(candUrl); item != nil {
					return item, fmt.Sprintf("STALE-VERSION-%s", v)
				}
			}
		}
	}

	// Cross-lang fallback: /assets_en/ <-> /assets/
	if strings.HasPrefix(clean, "assets_en/") {
		alt := "assets/" + strings.TrimPrefix(clean, "assets_en/")
		if item, _ := m.Get(alt); item != nil {
			return item, "CROSS-LANG-JP"
		}
	} else if strings.HasPrefix(clean, "assets/") {
		alt := "assets_en/" + strings.TrimPrefix(clean, "assets/")
		if item, _ := m.Get(alt); item != nil {
			return item, "CROSS-LANG-EN"
		}
	}

	return nil, ""
}

func getHeader(h map[string]string, key string) string {
	kLower := strings.ToLower(key)
	for k, v := range h {
		if strings.ToLower(k) == kLower {
			return v
		}
	}
	return ""
}

func (m *Manager) Save(urlPath string, headers map[string]string, data []byte) bool {
	cleanKey := strings.TrimPrefix(strings.Split(urlPath, "?")[0], "/")
	ct := getHeader(headers, "content-type")
	if !IsValidCacheContent(cleanKey, ct, data) {
		return false
	}

	filePath, ok := m.resolvePath(cleanKey)
	if !ok {
		return false
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return false
	}

	ce := getHeader(headers, "content-encoding")
	etag := getHeader(headers, "etag")
	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", time.Now().Unix(), len(data))
	}
	lastMod := getHeader(headers, "last-modified")

	pid := os.Getpid()
	ts := time.Now().UnixNano()
	tmpDataPath := fmt.Sprintf("%s.tmp.%d.%d", filePath, pid, ts)
	if err := os.WriteFile(tmpDataPath, data, 0644); err != nil {
		_ = os.Remove(tmpDataPath)
		return false
	}
	_ = os.Rename(tmpDataPath, filePath)

	// Write .ext
	extPath := filePath + ".ext"
	sum := md5.Sum(data)
	meta := map[string]interface{}{
		"LastModified": lastMod,
		"ETag":         etag,
		"at":           time.Now().Unix(),
		"md5":          hex.EncodeToString(sum[:]),
		"ce":           ce,
		"ct":           ct,
		"v":            1,
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	tmpExtPath := fmt.Sprintf("%s.tmp.%d.%d", extPath, pid, ts)
	if err := os.WriteFile(tmpExtPath, metaBytes, 0644); err == nil {
		_ = os.Rename(tmpExtPath, extPath)
	}

	// Update RAM cache
	item := &CacheItem{
		Key:             cleanKey,
		Data:            data,
		ContentType:     ct,
		ContentEncoding: ce,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            int64(len(data)),
	}
	m.ramCache.Set(cleanKey, item)

	m.missingMu.Lock()
	delete(m.missingCache, cleanKey)
	m.missingMu.Unlock()

	return true
}

func (m *Manager) markMissing(cleanKey string) {
	m.missingMu.Lock()
	m.missingCache[cleanKey] = struct{}{}
	if len(m.missingCache) > 4096 {
		m.missingCache = make(map[string]struct{})
	}
	m.missingMu.Unlock()
}

func (m *Manager) ClearRAM() {
	m.ramCache.Clear()
}

func (m *Manager) ClearAll() (int, int64) {
	m.ClearRAM()
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	var deletedFiles int
	var freedBytes int64

	_ = filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			freedBytes += fi.Size()
			deletedFiles++
			_ = os.Remove(p)
		}
		return nil
	})

	m.missingMu.Lock()
	m.missingCache = make(map[string]struct{})
	m.missingMu.Unlock()

	return deletedFiles, freedBytes
}

func (m *Manager) Stats() (items int, bytes int64) {
	return m.ramCache.Stats()
}

func (m *Manager) AuditAndRepair() map[string]interface{} {
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	scanned := 0
	corrupted := 0
	healthy := 0

	_ = filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".ext") || strings.Contains(p, ".tmp.") {
			if strings.Contains(p, ".tmp.") {
				_ = os.Remove(p)
			}
			return nil
		}
		scanned++
		if fi.Size() == 0 {
			corrupted++
			_ = os.Remove(p)
			_ = os.Remove(p + ".ext")
			return nil
		}

		data, err := os.ReadFile(p)
		if err != nil || len(data) == 0 {
			corrupted++
			_ = os.Remove(p)
			_ = os.Remove(p + ".ext")
			return nil
		}

		if !IsValidCacheContent(p, "", data) {
			corrupted++
			_ = os.Remove(p)
			_ = os.Remove(p + ".ext")
			return nil
		}

		healthy++
		return nil
	})

	return map[string]interface{}{
		"scanned":   scanned,
		"healthy":   healthy,
		"corrupted": corrupted,
		"ok":        true,
	}
}

func (m *Manager) PruneStaleVersions(keepCount int) (int, int, int64) {
	if keepCount <= 0 {
		keepCount = 8
	}
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	var deletedDirs, deletedFiles int
	var freedBytes int64

	for _, prefix := range []string{"assets", "assets_en"} {
		prefixDir := filepath.Join(base, prefix)
		entries, err := os.ReadDir(prefixDir)
		if err != nil {
			continue
		}

		var versions []string
		for _, e := range entries {
			if e.IsDir() {
				if _, err := strconv.Atoi(e.Name()); err == nil {
					versions = append(versions, e.Name())
				}
			}
		}

		sort.Slice(versions, func(i, j int) bool {
			v1, _ := strconv.Atoi(versions[i])
			v2, _ := strconv.Atoi(versions[j])
			return v1 > v2
		})

		if len(versions) > keepCount {
			stale := versions[keepCount:]
			for _, v := range stale {
				dirToDel := filepath.Join(prefixDir, v)
				_ = filepath.Walk(dirToDel, func(p string, fi os.FileInfo, err error) error {
					if err == nil && !fi.IsDir() {
						deletedFiles++
						freedBytes += fi.Size()
					}
					return nil
				})
				if err := os.RemoveAll(dirToDel); err == nil {
					deletedDirs++
				}
			}
		}
	}

	return deletedDirs, deletedFiles, freedBytes
}
