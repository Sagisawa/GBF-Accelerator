package cache

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type cacheMetadata struct {
	LastModified    string `json:"LastModified"`
	ETag            string `json:"ETag"`
	ContentEncoding string `json:"ce"`
	ContentType     string `json:"ct"`
	Version         int    `json:"v"`
}

type persistTask struct {
	ns         string
	cleanKey   string
	filePath   string
	headers    map[string]string
	data       []byte
	generation uint64
}

type Manager struct {
	mu           sync.RWMutex
	cacheBase    string
	ramCache     *LRUCache
	sf           *SingleFlight
	missingShards [missingCacheShardCount]missingCacheShard
	persistQueue chan *persistTask
	stopPersist  chan struct{}
	persistDone  chan struct{}
	stopOnce     sync.Once
	persistMu    sync.RWMutex
	generation   uint64
	ramEnabled   atomic.Bool
	autoRepair   atomic.Bool
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

const (
	defaultPersistWorkers  = 4
	missingCacheShardCount = 16
)

type missingCacheShard struct {
	mu    sync.RWMutex
	items map[string]struct{}
}

func (m *Manager) initMissingCacheShards() {
	for i := range m.missingShards {
		m.missingShards[i].items = make(map[string]struct{})
	}
}

func (m *Manager) missingShard(key string) *missingCacheShard {
	return &m.missingShards[int(fnv32(key)&(missingCacheShardCount-1))]
}

func (m *Manager) isMissing(key string) bool {
	shard := m.missingShard(key)
	shard.mu.RLock()
	_, ok := shard.items[key]
	shard.mu.RUnlock()
	return ok
}

func (m *Manager) clearMissing() {
	for i := range m.missingShards {
		shard := &m.missingShards[i]
		shard.mu.Lock()
		shard.items = make(map[string]struct{})
		shard.mu.Unlock()
	}
}


var tmpFileSeq atomic.Int64

func NewManager(cacheBase string, ramMaxMB int) *Manager {
	if ramMaxMB <= 0 {
		ramMaxMB = 256
	}
	m := &Manager{
		cacheBase:    cacheBase,
		ramCache:     NewLRUCache(int64(ramMaxMB) * 1024 * 1024),
		sf:           NewSingleFlight(),
		persistQueue: make(chan *persistTask, 1024),
		stopPersist:  make(chan struct{}),
		persistDone:  make(chan struct{}),
		generation:   1,
	}
	m.initMissingCacheShards()
	m.ramEnabled.Store(true)
	m.autoRepair.Store(true)
	var wg sync.WaitGroup
	for i := 0; i < defaultPersistWorkers; i++ {
		wg.Add(1)
		go m.persistWorker(&wg)
	}
	go func() {
		wg.Wait()
		close(m.persistDone)
	}()
	return m
}

func (m *Manager) Close() {
	m.stopOnce.Do(func() {
		close(m.stopPersist)
		select {
		case <-m.persistDone:
		case <-time.After(3 * time.Second):
		}
	})
}

func (m *Manager) persistWorker(wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-m.stopPersist:
			for {
				select {
				case task := <-m.persistQueue:
					m.saveToDisk(task.filePath, task.headers, task.data, task.generation)
				default:
					return
				}
			}
		case task := <-m.persistQueue:
			m.saveToDisk(task.filePath, task.headers, task.data, task.generation)
		}
	}
}

func (m *Manager) SetCacheBase(base string) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	m.generation++
	m.mu.Lock()
	m.cacheBase = base
	m.mu.Unlock()

	m.ramCache.Clear()
	m.clearMissing()
}

func (m *Manager) SetRAMEnabled(enabled bool) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()

	m.ramEnabled.Store(enabled)
	if !enabled {
		m.ramCache.Clear()
	}
}

func (m *Manager) SetAutoRepair(enabled bool) {
	m.autoRepair.Store(enabled)
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

func sanitizeNamespace(ns string) string {
	clean := strings.ToLower(strings.TrimSpace(ns))
	clean = strings.ReplaceAll(clean, ":", "_")
	clean = strings.ReplaceAll(clean, "/", "_")
	clean = strings.ReplaceAll(clean, "\\", "_")
	clean = strings.ReplaceAll(clean, "..", "")
	if clean == "" {
		return "gbf"
	}
	return clean
}

func makeRAMKey(ns, cleanKey string) string {
	if ns == "" || ns == "gbf" {
		return cleanKey
	}
	return sanitizeNamespace(ns) + "/" + cleanKey
}

func extractNamespaceAndKey(base, path string) (ns string, cleanKey string, ramKey string, ok bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", "", "", false
	}
	slashRel := filepath.ToSlash(rel)
	ns = "gbf"
	cleanKey = slashRel
	if strings.HasPrefix(slashRel, "hosts/") {
		parts := strings.SplitN(slashRel, "/", 3)
		if len(parts) == 3 {
			ns = parts[1]
			cleanKey = parts[2]
		}
	}
	ramKey = makeRAMKey(ns, cleanKey)
	return ns, cleanKey, ramKey, true
}

func (m *Manager) resolvePath(urlPath string) (string, bool) {
	return m.resolvePathWithNamespace("gbf", urlPath)
}

func (m *Manager) resolvePathWithNamespace(ns, urlPath string) (string, bool) {
	clean := strings.TrimSpace(strings.Split(urlPath, "?")[0])
	clean = filepath.Clean(filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	if clean == "" || clean == "." {
		return "", false
	}

	// Security: Prevent path traversal, Windows drive letters, and UNC paths
	if strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	if filepath.IsAbs(clean) || (len(clean) >= 2 && clean[1] == ':') || strings.HasPrefix(clean, `\\`) {
		return "", false
	}

	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	var target string
	if ns == "" || ns == "gbf" {
		target = filepath.Join(base, clean)
	} else {
		target = filepath.Join(base, "hosts", sanitizeNamespace(ns), clean)
	}

	rel, err := filepath.Rel(base, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return target, true
}

func (m *Manager) HasCache(urlPath string) bool {
	return m.HasCacheWithNamespace("gbf", urlPath)
}

func (m *Manager) HasCacheWithNamespace(ns, urlPath string) bool {
	cleanKey := strings.TrimPrefix(strings.Split(urlPath, "?")[0], "/")
	if cleanKey == "" {
		return false
	}
	ramKey := makeRAMKey(ns, cleanKey)
	if m.ramEnabled.Load() && m.ramCache.Contains(ramKey) {
		return true
	}
	if m.isMissing(ramKey) {
		return false
	}
	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return false
	}
	if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() && fi.Size() > 0 {
		return true
	}
	// Fallback check (only in gbf namespace)
	if ns == "" || ns == "gbf" {
		var altPath string
		if !strings.HasPrefix(cleanKey, "assets") {
			altPath, _ = m.resolvePathWithNamespace("gbf", "assets/"+cleanKey)
		} else {
			altPath, _ = m.resolvePathWithNamespace("gbf", strings.TrimPrefix(cleanKey, "assets/"))
		}
		if altPath != "" {
			if fi, err := os.Stat(altPath); err == nil && !fi.IsDir() && fi.Size() > 0 {
				return true
			}
		}
	}
	return false
}

var reHollowedFunc = regexp.MustCompile(`(?:function(?:\s+[a-zA-Z0-9_]+)?\s*\([a-zA-Z0-9_,\s]*\)\s*|\([a-zA-Z0-9_,\s]*\)\s*=>\s*)\{\s*(?:void\s+0\s*;?|;?)\s*\}`)

func (m *Manager) CheckAndQuarantineTamperedJS(filePath string) bool {
	if !strings.HasSuffix(filePath, "set-error-handler.js") {
		return false
	}
	data, err := os.ReadFile(filePath)
	if err != nil || len(data) == 0 {
		return false
	}

	rawBytes := data
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		if gr, err := gzip.NewReader(bytes.NewReader(data)); err == nil {
			if decompressed, err := io.ReadAll(gr); err == nil {
				rawBytes = decompressed
			}
			_ = gr.Close()
		}
	}
	rawText := string(rawBytes)
	rawLower := strings.ToLower(rawText)

	hasHollowed := reHollowedFunc.MatchString(rawText)
	hasErrorSig := strings.Contains(rawLower, "error") || strings.Contains(rawLower, "onerror")
	isMissingOriginal := !strings.Contains(rawText, "window.location.reload") && !strings.Contains(rawText, "alert(")

	if hasHollowed && hasErrorSig && isMissingOriginal {
		ts := time.Now().Unix()
		quarantineTarget := fmt.Sprintf("%s.quarantine.%d", filePath, ts)
		_ = os.Rename(filePath, quarantineTarget)
		extPath := filePath + ".ext"
		if fi, err := os.Stat(extPath); err == nil && !fi.IsDir() {
			_ = os.Rename(extPath, fmt.Sprintf("%s.ext.quarantine.%d", filePath, ts))
		}
		return true
	}
	return false
}

func (m *Manager) Get(urlPath string) (*CacheItem, string) {
	return m.GetWithNamespace("gbf", urlPath)
}

func (m *Manager) GetWithNamespace(ns, urlPath string) (*CacheItem, string) {
	basePath, _, _ := strings.Cut(urlPath, "?")
	cleanKey := strings.TrimPrefix(basePath, "/")
	ramKey := makeRAMKey(ns, cleanKey)

	// 1. Check RAM Cache
	if m.ramEnabled.Load() {
		if item, ok := m.ramCache.Get(ramKey); ok {
			return item, "RAM"
		}
	}

	// Negative cache check
	if m.isMissing(ramKey) {
		return nil, ""
	}

	// 2. Check Disk Cache
	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, ""
	}

	fi, err := os.Stat(filePath)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		if ns == "" || ns == "gbf" {
			// Try fallback: prepend or strip "assets" for gbf namespace
			var altPath string
			if !strings.HasPrefix(cleanKey, "assets") {
				altPath, _ = m.resolvePathWithNamespace("gbf", "assets/"+cleanKey)
			} else {
				altPath, _ = m.resolvePathWithNamespace("gbf", strings.TrimPrefix(cleanKey, "assets/"))
			}
			if altPath != "" {
				if fi2, err2 := os.Stat(altPath); err2 == nil && !fi2.IsDir() && fi2.Size() > 0 {
					filePath = altPath
					fi = fi2
				} else {
					m.markMissing(ramKey)
					return nil, ""
				}
			} else {
				m.markMissing(ramKey)
				return nil, ""
			}
		} else {
			m.markMissing(ramKey)
			return nil, ""
		}
	}

	if m.autoRepair.Load() && strings.HasSuffix(cleanKey, "set-error-handler.js") {
		if m.CheckAndQuarantineTamperedJS(filePath) {
			m.markMissing(ramKey)
			return nil, ""
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil || len(data) == 0 {
		m.markMissing(ramKey)
		return nil, ""
	}

	// Read metadata from .ext
	extPath := filePath + ".ext"
	contentType := ""
	contentEncoding := ""
	etag := ""
	lastMod := ""

	if metaBytes, err := os.ReadFile(extPath); err == nil {
		var meta cacheMetadata
		if err := json.Unmarshal(metaBytes, &meta); err == nil && meta.Version == 1 {
			contentType = meta.ContentType
			contentEncoding = meta.ContentEncoding
			etag = meta.ETag
			lastMod = meta.LastModified
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
		if m.autoRepair.Load() {
			_ = os.Remove(filePath)
			_ = os.Remove(extPath)
		}
		m.markMissing(ramKey)
		return nil, ""
	}

	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", fi.ModTime().Unix(), len(data))
	}

	// Check gzip signature
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		contentEncoding = "gzip"
	} else if contentEncoding == "gzip" {
		// Strip misleading gzip encoding from decompressed plaintext to prevent ERR_CONTENT_DECODING_FAILED
		contentEncoding = ""
	}

	item := &CacheItem{
		Key:             ramKey,
		Data:            data,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            int64(len(data)),
	}

	// Store into RAM cache only when enabled.
	if m.ramEnabled.Load() {
		m.ramCache.Set(ramKey, item)
	}
	return item, "DISK"
}

var fallbackTimestampRe = regexp.MustCompile(`^(assets(?:_(?:en|jp))?)/(\d+)/(.+)$`)

func (m *Manager) GetFallback(urlPath string) (*CacheItem, string) {
	// First check direct
	if item, src := m.Get(urlPath); item != nil {
		return item, src
	}

	clean := strings.TrimPrefix(strings.Split(urlPath, "?")[0], "/")

	// Versioned asset fallback: /(assets(?:_(?:en|jp))?)/(\d+)/(.+)
	if mVer := fallbackTimestampRe.FindStringSubmatch(clean); len(mVer) == 4 {
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
	if v, ok := h[kLower]; ok {
		return v
	}
	for k, v := range h {
		if strings.EqualFold(k, kLower) {
			return v
		}
	}
	return ""
}

func (m *Manager) saveRAMInternal(ns, urlPath string, headers map[string]string, data []byte) (*CacheItem, string, string, uint64, bool) {
	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	generation := m.generation
	cleanKey := strings.TrimPrefix(strings.Split(urlPath, "?")[0], "/")
	ct := getHeader(headers, "content-type")
	if !IsValidCacheContent(cleanKey, ct, data) {
		return nil, "", "", 0, false
	}

	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, "", "", 0, false
	}

	ce := getHeader(headers, "content-encoding")
	if !(len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b) && ce == "gzip" {
		ce = ""
	}
	etag := getHeader(headers, "etag")
	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", time.Now().Unix(), len(data))
	}
	lastMod := getHeader(headers, "last-modified")

	ramKey := makeRAMKey(ns, cleanKey)
	item := &CacheItem{
		Key:             ramKey,
		Data:            data,
		ContentType:     ct,
		ContentEncoding: ce,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            int64(len(data)),
	}
	if m.ramEnabled.Load() {
		m.ramCache.Set(ramKey, item)
	}

	shard := m.missingShard(ramKey)
	shard.mu.Lock()
	delete(shard.items, ramKey)
	shard.mu.Unlock()

	return item, cleanKey, filePath, generation, true
}

func (m *Manager) SaveRAM(urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.SaveRAMWithNamespace("gbf", urlPath, headers, data)
}

func (m *Manager) SaveRAMWithNamespace(ns, urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	item, cleanKey, filePath, generation, ok := m.saveRAMInternal(ns, urlPath, headers, data)
	if !ok || item == nil {
		return nil, false
	}

	// Guard against enqueuing when manager is stopping/stopped
	select {
	case <-m.stopPersist:
		return item, true
	default:
	}

	// Enqueue async disk persistence (bounded, non-blocking)
	select {
	case m.persistQueue <- &persistTask{
		ns:         ns,
		cleanKey:   cleanKey,
		filePath:   filePath,
		headers:    headers,
		data:       data,
		generation: generation,
	}:
	default:
		// Queue full, drop disk persist without delaying foreground
	}

	return item, true
}

func (m *Manager) Save(urlPath string, headers map[string]string, data []byte) bool {
	return m.SaveWithNamespace("gbf", urlPath, headers, data)
}

func (m *Manager) SaveWithNamespace(ns, urlPath string, headers map[string]string, data []byte) bool {
	item, _, filePath, generation, ok := m.saveRAMInternal(ns, urlPath, headers, data)
	if !ok || item == nil {
		return false
	}
	return m.saveToDisk(filePath, headers, data, generation)
}

func renameWithRetry(src, dst string, maxAttempts int) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

func (m *Manager) saveToDisk(filePath string, headers map[string]string, data []byte, generation uint64) bool {
	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	if generation != m.generation {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return false
	}

	ce := getHeader(headers, "content-encoding")
	if !(len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b) && ce == "gzip" {
		ce = ""
	}
	etag := getHeader(headers, "etag")
	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", time.Now().Unix(), len(data))
	}
	lastMod := getHeader(headers, "last-modified")
	ct := getHeader(headers, "content-type")

	pid := os.Getpid()
	ts := time.Now().UnixNano()
	seq := tmpFileSeq.Add(1)
	tmpDataPath := fmt.Sprintf("%s.tmp.%d.%d.%d", filePath, pid, ts, seq)
	if err := os.WriteFile(tmpDataPath, data, 0644); err != nil {
		_ = os.Remove(tmpDataPath)
		return false
	}
	if err := renameWithRetry(tmpDataPath, filePath, 3); err != nil {
		_ = os.Remove(tmpDataPath)
		return false
	}

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
	tmpExtPath := fmt.Sprintf("%s.tmp.%d.%d.%d", extPath, pid, ts, seq)
	if err := os.WriteFile(tmpExtPath, metaBytes, 0644); err == nil {
		if err := renameWithRetry(tmpExtPath, extPath, 3); err != nil {
			_ = os.Remove(tmpExtPath)
		}
	}

	return true
}

func (m *Manager) markMissing(ramKey string) {
	shard := m.missingShard(ramKey)
	shard.mu.Lock()
	shard.items[ramKey] = struct{}{}
	if len(shard.items) > 256 {
		shard.items = make(map[string]struct{})
	}
	shard.mu.Unlock()
}

func (m *Manager) ClearRAM() {
	m.ramCache.Clear()
}

func (m *Manager) ClearAll() (int, int64) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.generation++
	m.ClearRAM()
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	var deletedFiles int
	var freedBytes int64

	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				freedBytes += info.Size()
			}
			deletedFiles++
			_ = os.Remove(p)
		}
		return nil
	})

	m.clearMissing()

	return deletedFiles, freedBytes
}

func (m *Manager) Stats() (items int, bytes int64) {
	return m.ramCache.Stats()
}

type AuditProgress struct {
	Scanned   int  `json:"scanned"`
	Healthy   int  `json:"healthy"`
	Corrupted int  `json:"corrupted"`
	Done      bool `json:"done"`
}

type SlimProgress struct {
	CurrentDir   string `json:"current_dir"`
	CurrentIdx   int    `json:"current_idx"`
	TotalDirs    int    `json:"total_dirs"`
	DeletedFiles int    `json:"deleted_files"`
	FreedBytes   int64  `json:"freed_bytes"`
	Done         bool   `json:"done"`
}

func (m *Manager) AuditAndRepairWithProgress(progressCb func(p AuditProgress), cancelCh <-chan struct{}) map[string]interface{} {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	scanned := 0
	corrupted := 0
	healthy := 0

	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		select {
		case <-cancelCh:
			return filepath.SkipAll
		default:
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".ext") || strings.Contains(p, ".tmp.") || strings.Contains(p, ".quarantine") {
			if strings.Contains(p, ".tmp.") {
				_ = os.Remove(p)
			}
			return nil
		}
		scanned++

		_, _, ramKey, ok := extractNamespaceAndKey(base, p)
		if !ok {
			return nil
		}

		if strings.HasSuffix(p, "set-error-handler.js") {
			if m.CheckAndQuarantineTamperedJS(p) {
				corrupted++
				m.ramCache.Delete(ramKey)
				return nil
			}
		}

		isBad := false
		info, iErr := d.Info()
		if iErr != nil || info.Size() == 0 {
			isBad = true
		} else {
			f, err := os.Open(p)
			if err != nil {
				isBad = true
			} else {
				var header [512]byte
				n, rErr := io.ReadFull(f, header[:])
				_ = f.Close()
				if (rErr != nil && rErr != io.EOF && rErr != io.ErrUnexpectedEOF) || n == 0 {
					isBad = true
				} else if !IsValidCacheContent(p, "", header[:n]) {
					isBad = true
				}
			}
		}

		if isBad {
			corrupted++
			_ = os.Remove(p)
			_ = os.Remove(p + ".ext")
			m.ramCache.Delete(ramKey)
		} else {
			healthy++
		}

		if progressCb != nil && scanned%500 == 0 {
			progressCb(AuditProgress{
				Scanned:   scanned,
				Healthy:   healthy,
				Corrupted: corrupted,
				Done:      false,
			})
		}
		return nil
	})

	if progressCb != nil {
		progressCb(AuditProgress{
			Scanned:   scanned,
			Healthy:   healthy,
			Corrupted: corrupted,
			Done:      true,
		})
	}

	return map[string]interface{}{
		"scanned":   scanned,
		"healthy":   healthy,
		"corrupted": corrupted,
		"ok":        true,
	}
}

func (m *Manager) AuditAndRepair() map[string]interface{} {
	return m.AuditAndRepairWithProgress(nil, nil)
}

func (m *Manager) PruneStaleVersionsWithProgress(keepCount int, progressCb func(p SlimProgress), cancelCh <-chan struct{}) (int, int, int64) {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	if keepCount <= 0 {
		keepCount = 8
	}
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	type staleDirEntry struct {
		fullPath string
		display  string
	}

	collectStale := func(root string) []staleDirEntry {
		var stales []staleDirEntry
		for _, prefix := range []string{"assets", "assets_en", "assets_jp"} {
			prefixDir := filepath.Join(root, prefix)
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
				for _, v := range versions[keepCount:] {
					stales = append(stales, staleDirEntry{
						fullPath: filepath.Join(prefixDir, v),
						display:  filepath.Join(prefix, v),
					})
				}
			}
		}
		return stales
	}

	var allStales []staleDirEntry
	allStales = append(allStales, collectStale(base)...)

	hostsDir := filepath.Join(base, "hosts")
	if hostEntries, err := os.ReadDir(hostsDir); err == nil {
		for _, he := range hostEntries {
			if he.IsDir() {
				allStales = append(allStales, collectStale(filepath.Join(hostsDir, he.Name()))...)
			}
		}
	}

	var deletedDirs, deletedFiles int
	var freedBytes int64
	totalDirs := len(allStales)

	for idx, s := range allStales {
		select {
		case <-cancelCh:
			return deletedDirs, deletedFiles, freedBytes
		default:
		}

		if progressCb != nil {
			progressCb(SlimProgress{
				CurrentDir:   s.display,
				CurrentIdx:   idx + 1,
				TotalDirs:    totalDirs,
				DeletedFiles: deletedFiles,
				FreedBytes:   freedBytes,
				Done:         false,
			})
		}

		_ = filepath.WalkDir(s.fullPath, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				deletedFiles++
				if info, err := d.Info(); err == nil {
					freedBytes += info.Size()
				}
			}
			return nil
		})
		if err := os.RemoveAll(s.fullPath); err == nil {
			deletedDirs++
		}
	}

	if progressCb != nil {
		progressCb(SlimProgress{
			CurrentDir:   "",
			CurrentIdx:   totalDirs,
			TotalDirs:    totalDirs,
			DeletedFiles: deletedFiles,
			FreedBytes:   freedBytes,
			Done:         true,
		})
	}

	return deletedDirs, deletedFiles, freedBytes
}

func (m *Manager) PruneStaleVersions(keepCount int) (int, int, int64) {
	return m.PruneStaleVersionsWithProgress(keepCount, nil, nil)
}

type warmupCandidate struct {
	path    string
	modTime time.Time
}

func (m *Manager) Warmup(maxItems int) int {
	if maxItems <= 0 {
		maxItems = 1500
	}
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()

	var candidates []warmupCandidate

	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".ext") || strings.Contains(p, ".tmp.") || strings.Contains(p, ".quarantine") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 {
			return nil
		}
		candidates = append(candidates, warmupCandidate{
			path:    p,
			modTime: info.ModTime(),
		})
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})

	if len(candidates) > maxItems {
		candidates = candidates[:maxItems]
	}

	loaded := 0
	for _, c := range candidates {
		ns, cleanKey, _, ok := extractNamespaceAndKey(base, c.path)
		if !ok {
			continue
		}
		if item, _ := m.GetWithNamespace(ns, cleanKey); item != nil {
			loaded++
		}
	}
	return loaded
}
