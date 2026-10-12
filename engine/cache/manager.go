package cache

import (
	"bytes"
	"compress/gzip"
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

// MaxDiskDirectReadSize is the threshold (2MB) for RAM SLRU admission on disk hits.
// Disk cache hits of any size are read fully into memory for fast direct response,
// but only assets <= MaxDiskDirectReadSize are admitted into the RAM SLRU probation segment
// to protect RAM cache capacity from being dominated by large objects.
const MaxDiskDirectReadSize = 2 * 1024 * 1024

// diskMeta represents the persisted metadata stored in .ext files.
// It maintains backward compatibility with Version 1 metadata schemas.
type diskMeta struct {
	Namespace         string `json:"ns,omitempty"`
	LastModified      string `json:"LastModified,omitempty"`
	ETag              string `json:"ETag,omitempty"`
	AccessTime        int64  `json:"at,omitempty"`
	ContentEncoding   string `json:"ce,omitempty"`
	ContentType       string `json:"ct,omitempty"`
	Version           int    `json:"v"`
	// Verified indicates that the cache item has previously passed this project's
	// cache content validation (IsValidCacheContent) and its physical file size and
	// mtime have not changed since.
	// NOTE: This represents operational validity within the project cache lifecycle;
	// it is NOT a cryptographic proof of integrity or absolute tamper-resistance.
	Verified          bool   `json:"verified,omitempty"`
	Size              int64  `json:"size,omitempty"`
	MTimeNano         int64  `json:"mtime,omitempty"`
	LegacyContentType string `json:"ContentType,omitempty"`
}

type cacheMetadata = diskMeta

type persistTask struct {
	ns         string
	cleanKey   string
	filePath   string
	headers    map[string]string
	data       []byte
	generation uint64
	token      opToken
}

type Manager struct {
	mu                 sync.RWMutex
	cacheBase          string
	ramCache           *LRUCache
	ramBudget          *RAMBudget
	residentPool       *ResidentPool
	sf                 *SingleFlight
	missingShards      [missingCacheShardCount]missingCacheShard
	diskMetaShards     [diskMetaShardCount]diskMetaShard
	tokenShards        [tokenShardCount]tokenShard
	tempTokensMu       sync.Mutex
	tempTokens         map[string]opToken
	snapshotMu         sync.RWMutex
	lastSnapshot       *BreakdownSnapshot
	activeScanSnapshot *BreakdownSnapshot
	snapshotSeq        atomic.Uint64
	persistQueue       chan *persistTask
	stopPersist        chan struct{}
	persistDone        chan struct{}
	stopOnce           sync.Once
	persistMu          sync.RWMutex
	generation         uint64
	ramEnabled         atomic.Bool
	autoRepair         atomic.Bool
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

func resolveContentType(ct, filePath string) string {
	if ct != "" {
		return ct
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if fb, ok := mimeFallbacks[ext]; ok {
		return fb
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

const (
	defaultPersistWorkers  = 4
	missingCacheShardCount = 16
	diskMetaShardCount     = 16
)

type diskMetaEntry struct {
	namespace       string
	mtimeNano       int64
	size            int64
	contentType     string
	contentEncoding string
	etag            string
	lastModified    string
	exists          bool
	hasMeta         bool
	verified        bool
}

func isDiskFileOwnedByNamespace(ns, cleanKey string, extMeta diskMeta, hasExt bool) bool {
	normNS := sanitizeNamespace(ns)
	if hasExt && extMeta.Namespace != "" {
		return sanitizeNamespace(extMeta.Namespace) == normNS
	}
	if normNS == "gbf" && (cleanKey == "hosts" || strings.HasPrefix(cleanKey, "hosts/")) {
		return false
	}
	return true
}

func checkDiskFileOwnership(ns, cleanKey, filePath string) bool {
	metaBytes, err := os.ReadFile(filePath + ".ext")
	if err != nil {
		return isDiskFileOwnedByNamespace(ns, cleanKey, diskMeta{}, false)
	}
	meta, ok := parseExtFile(metaBytes)
	return isDiskFileOwnedByNamespace(ns, cleanKey, meta, ok)
}

type diskMetaShard struct {
	mu    sync.RWMutex
	items map[string]diskMetaEntry
}

func (m *Manager) initDiskMetaShards() {
	for i := range m.diskMetaShards {
		m.diskMetaShards[i].items = make(map[string]diskMetaEntry)
	}
}

func (m *Manager) diskMetaShard(key string) *diskMetaShard {
	return &m.diskMetaShards[int(fnv32(key)&(diskMetaShardCount-1))]
}

func (m *Manager) getDiskMeta(key string) (diskMetaEntry, bool) {
	shard := m.diskMetaShard(key)
	shard.mu.RLock()
	entry, ok := shard.items[key]
	shard.mu.RUnlock()
	return entry, ok
}

func (m *Manager) recordDiskPresence(key string, mtimeNano int64, size int64) {
	shard := m.diskMetaShard(key)
	shard.mu.Lock()
	if entry, ok := shard.items[key]; ok {
		if entry.mtimeNano != mtimeNano || entry.size != size {
			entry.hasMeta = false
			entry.contentType = ""
			entry.contentEncoding = ""
			entry.etag = ""
			entry.lastModified = ""
			entry.verified = false
		}
		entry.exists = true
		entry.mtimeNano = mtimeNano
		entry.size = size
		shard.items[key] = entry
	} else {
		shard.items[key] = diskMetaEntry{
			mtimeNano: mtimeNano,
			size:      size,
			exists:    true,
			hasMeta:   false,
			verified:  false,
		}
		if len(shard.items) > 4096 {
			count := 0
			for k := range shard.items {
				delete(shard.items, k)
				count++
				if count >= 1024 {
					break
				}
			}
		}
	}
	shard.mu.Unlock()
}

func (m *Manager) setDiskMeta(key string, entry diskMetaEntry) {
	shard := m.diskMetaShard(key)
	shard.mu.Lock()
	entry.exists = true
	shard.items[key] = entry
	if len(shard.items) > 4096 {
		count := 0
		for k := range shard.items {
			delete(shard.items, k)
			count++
			if count >= 1024 {
				break
			}
		}
	}
	shard.mu.Unlock()
}

func (m *Manager) deleteDiskMeta(key string) {
	shard := m.diskMetaShard(key)
	shard.mu.Lock()
	delete(shard.items, key)
	shard.mu.Unlock()
}

func (m *Manager) clearConflictingPhysicalPathState(ns, cleanKey string) {
	normNS := sanitizeNamespace(ns)
	if normNS == "gbf" && strings.HasPrefix(cleanKey, "hosts/") {
		parts := strings.SplitN(cleanKey, "/", 3)
		if len(parts) == 3 && parts[1] != "" && parts[2] != "" {
			subNS := sanitizeNamespace(parts[1])
			subKey := normalizeCleanKey(parts[2])
			if subKey != "" {
				otherRAMKey := makeRAMKey(subNS, subKey)
				m.deleteDiskMeta(otherRAMKey)
				m.deleteDiskMeta(subNS + "/" + subKey)
			}
		}
	} else if normNS != "gbf" {
		gbfHostsKey := "hosts/" + normNS + "/" + cleanKey
		m.deleteDiskMeta(makeRAMKey("gbf", gbfHostsKey))
	}
}

func (m *Manager) clearDiskMeta() {
	for i := range m.diskMetaShards {
		shard := &m.diskMetaShards[i]
		shard.mu.Lock()
		shard.items = make(map[string]diskMetaEntry)
		shard.mu.Unlock()
	}
}

func parseExtFile(metaBytes []byte) (diskMeta, bool) {
	var meta diskMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil || meta.Version != 1 {
		return meta, false
	}
	if meta.ContentType == "" && meta.LegacyContentType != "" {
		meta.ContentType = meta.LegacyContentType
	}
	return meta, true
}

func writeDiskExtFile(filePath string, meta diskMeta) error {
	meta.Version = 1
	metaBytes, err := json.Marshal(&meta)
	if err != nil {
		return err
	}
	extPath := filePath + ".ext"
	pid := os.Getpid()
	ts := time.Now().UnixNano()
	seq := tmpFileSeq.Add(1)
	tmpExtPath := fmt.Sprintf("%s.tmp.%d.%d.%d", extPath, pid, ts, seq)
	if err := os.WriteFile(tmpExtPath, metaBytes, 0644); err != nil {
		return err
	}
	if err := renameWithRetry(tmpExtPath, extPath, 3); err != nil {
		_ = os.Remove(tmpExtPath)
		return err
	}
	return nil
}

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
	ramCache := NewLRUCache(int64(ramMaxMB) * 1024 * 1024)
	budget := NewRAMBudget(int64(ramMaxMB)*1024*1024, ramCache)
	resident := NewResidentPool(budget)
	m := &Manager{
		cacheBase:    cacheBase,
		ramBudget:    budget,
		residentPool: resident,
		ramCache:     ramCache,
		sf:           NewSingleFlight(),
		tempTokens:   make(map[string]opToken),
		persistQueue: make(chan *persistTask, 1024),
		stopPersist:  make(chan struct{}),
		persistDone:  make(chan struct{}),
		generation:   1,
	}
	m.initMissingCacheShards()
	m.initDiskMetaShards()
	m.initTokenShards()
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
					m.saveToDiskWithNSAndToken(task.ns, task.cleanKey, task.filePath, task.headers, task.data, task.generation, task.token)
				default:
					return
				}
			}
		case task := <-m.persistQueue:
			m.saveToDiskWithNSAndToken(task.ns, task.cleanKey, task.filePath, task.headers, task.data, task.generation, task.token)
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
	m.DisableBoost()
	m.clearMissing()
	m.clearDiskMeta()
	m.clearTokenShards()
	m.clearTempTokens()
	m.invalidateSnapshots("缓存目录已变更")
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
	newTotal := int64(maxMB) * 1024 * 1024
	if m.ramBudget != nil {
		if m.residentPool != nil && m.residentPool.Enabled() && newTotal < m.ramBudget.ResidentBytes() {
			// Lowering RAM limit below currently resident payload:
			// Safely disable Boost so that resident payload does not exceed the new configured limit
			m.DisableBoost()
		}
		m.ramBudget.SetTotalBudget(newTotal, m.ramCache)
	} else {
		m.ramCache.SetMaxBytes(newTotal)
	}
}

func (m *Manager) ResidentPool() *ResidentPool {
	return m.residentPool
}

func (m *Manager) RAMBudget() *RAMBudget {
	return m.ramBudget
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
	if normNS, normKey, ok := canonicalizeNSAndKey(ns, cleanKey); ok {
		if normNS == "gbf" {
			return normKey
		}
		return normNS + "\x00" + normKey
	}
	if ns == "" || ns == "gbf" {
		return cleanKey
	}
	normNS := sanitizeNamespace(ns)
	if normNS == "gbf" {
		return cleanKey
	}
	return normNS + "\x00" + cleanKey
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
	if normKey := normalizeCleanKey(cleanKey); normKey != "" {
		cleanKey = normKey
	}
	if ns == "" || ns == "gbf" {
		ramKey = cleanKey
	} else {
		ramKey = sanitizeNamespace(ns) + "/" + cleanKey
	}
	return ns, cleanKey, ramKey, true
}

func resolveDiskFileOwner(base, path string) (ns string, cleanKey string, ramKey string, ok bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", "", "", false
	}
	slashRel := filepath.ToSlash(rel)
	ns = "gbf"
	cleanKey = slashRel
	if strings.HasPrefix(slashRel, "hosts/") {
		parts := strings.SplitN(slashRel, "/", 3)
		if len(parts) == 3 && parts[1] != "" && parts[2] != "" {
			ns = sanitizeNamespace(parts[1])
			cleanKey = parts[2]
		}
		if metaBytes, err := os.ReadFile(path + ".ext"); err == nil {
			if meta, ok := parseExtFile(metaBytes); ok && meta.Namespace != "" {
				extNS := sanitizeNamespace(meta.Namespace)
				if extNS == "gbf" {
					ns = "gbf"
					cleanKey = slashRel
				} else {
					ns = extNS
				}
			}
		}
	}
	normNS, normKey, ok := canonicalizeNSAndKey(ns, cleanKey)
	if !ok {
		return "", "", "", false
	}
	return normNS, normKey, makeRAMKey(normNS, normKey), true
}

func (m *Manager) resolvePath(urlPath string) (string, bool) {
	return m.resolvePathWithNamespace("gbf", urlPath)
}

func (m *Manager) resolvePathWithNamespace(ns, urlPath string) (string, bool) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(cleanKey))
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
	if normNS == "" || normNS == "gbf" {
		target = filepath.Join(base, clean)
	} else {
		target = filepath.Join(base, "hosts", normNS, clean)
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
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return false
	}
	ns = normNS
	ramKey := makeRAMKey(ns, cleanKey)
	if m.residentPool != nil && m.residentPool.Enabled() {
		if _, ok := m.residentPool.Get(ramKey); ok {
			return true
		}
	}
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
	tok := m.acquireOpToken(ns, cleanKey)
	isSharedHostsPath := ns != "gbf" || strings.HasPrefix(cleanKey, "hosts/")
	if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() && fi.Size() > 0 && checkDiskFileOwnership(ns, cleanKey, filePath) {
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		valid := isTokenValidLocked(shard, tok, m.generation)
		if valid && isSharedHostsPath {
			if curFi, sErr := os.Stat(filePath); sErr != nil || curFi.IsDir() || !os.SameFile(fi, curFi) || !checkDiskFileOwnership(ns, cleanKey, filePath) {
				valid = false
			}
		}
		if valid {
			m.recordDiskPresence(ramKey, fi.ModTime().UnixNano(), fi.Size())
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		if valid {
			return true
		}
	}
	// Fallback check (only in gbf namespace)
	if ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			if altPath, ok := m.resolvePathWithNamespace("gbf", altKey); ok && altPath != "" {
				if fi, err := os.Stat(altPath); err == nil && !fi.IsDir() && fi.Size() > 0 && checkDiskFileOwnership("gbf", altKey, altPath) {
					m.persistMu.RLock()
					shard := m.tokenShardByCanon(tok.canonKey)
					shard.mu.Lock()
					valid := isTokenValidLocked(shard, tok, m.generation)
					if valid {
						m.recordDiskPresence(ramKey, fi.ModTime().UnixNano(), fi.Size())
					}
					shard.mu.Unlock()
					m.persistMu.RUnlock()
					return valid
				}
			}
		}
	}
	m.persistMu.RLock()
	shard := m.tokenShardByCanon(tok.canonKey)
	shard.mu.Lock()
	if isTokenValidLocked(shard, tok, m.generation) {
		m.deleteDiskMeta(ramKey)
		m.markMissing(ramKey)
	}
	shard.mu.Unlock()
	m.persistMu.RUnlock()
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

// PeekRAMWithNamespace returns an item from RAM cache without promoting it or updating LRU order.
func (m *Manager) PeekRAMWithNamespace(ns, urlPath string) *CacheItem {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil
	}
	ramKey := makeRAMKey(normNS, cleanKey)
	if m.ramEnabled.Load() {
		if item, ok := m.ramCache.Peek(ramKey); ok {
			return item
		}
	}
	return nil
}

// TouchRAMWithNamespace touches the item in RAM cache, promoting it to Protected if it was in Probation.
func (m *Manager) TouchRAMWithNamespace(ns, urlPath string) {
	if !m.ramEnabled.Load() {
		return
	}
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return
	}
	ramKey := makeRAMKey(normNS, cleanKey)
	_, _ = m.ramCache.Get(ramKey)
}

// IsRAMProtected reports whether the asset is in the Protected segment of RAM cache.
func (m *Manager) IsRAMProtected(ns, urlPath string) bool {
	if !m.ramEnabled.Load() {
		return false
	}
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return false
	}
	ramKey := makeRAMKey(normNS, cleanKey)
	return m.ramCache.IsProtected(ramKey)
}

// loadDiskMetaFast retrieves disk metadata (size, mtime, ContentType, ContentEncoding, ETag, LastModified)
// without reading the asset file body.
// loadDiskMetaWithStat checks if an item has verified metadata matching the provided mtimeNano and size.
// If verified, it returns a CacheItem with Data == nil and ok == true.
func (m *Manager) loadDiskMetaWithStat(ns, cleanKey, filePath string, mtimeNano, size int64, tok opToken) (*CacheItem, bool) {
	ramKey := makeRAMKey(ns, cleanKey)
	normNS := sanitizeNamespace(ns)
	isSharedHostsPath := normNS != "gbf" || strings.HasPrefix(cleanKey, "hosts/")

	// 1. Check in-memory metadata index
	if meta, ok := m.getDiskMeta(ramKey); ok && meta.hasMeta && meta.verified && meta.mtimeNano == mtimeNano && meta.size == size && (meta.namespace == "" || meta.namespace == normNS) {
		if isSharedHostsPath {
			m.persistMu.RLock()
			shard := m.tokenShardByCanon(tok.canonKey)
			shard.mu.Lock()
			valid := isTokenValidLocked(shard, tok, m.generation)
			if valid {
				if curFi, sErr := os.Stat(filePath); sErr != nil || curFi.IsDir() || curFi.Size() != size || curFi.ModTime().UnixNano() != mtimeNano || !checkDiskFileOwnership(normNS, cleanKey, filePath) {
					valid = false
				}
			}
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			if !valid {
				return nil, false
			}
		}
		ct := resolveContentType(meta.contentType, filePath)
		etag := meta.etag
		if etag == "" {
			etag = fmt.Sprintf("\"%x-%x\"", mtimeNano/1e9, size)
		}
		return &CacheItem{
			Key:             ramKey,
			ContentType:     ct,
			ContentEncoding: meta.contentEncoding,
			ETag:            etag,
			LastModified:    meta.lastModified,
			Size:            size,
			Data:            nil,
		}, true
	}

	// 2. Check .ext on disk
	extPath := filePath + ".ext"
	var metaBytes []byte
	var err error
	if isSharedHostsPath {
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		if curFi, sErr := os.Stat(filePath); sErr != nil || curFi.IsDir() || curFi.Size() != size || curFi.ModTime().UnixNano() != mtimeNano {
			err = os.ErrNotExist
		} else {
			metaBytes, err = os.ReadFile(extPath)
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
	} else {
		metaBytes, err = os.ReadFile(extPath)
	}
	if err != nil {
		return nil, false
	}

	meta, ok := parseExtFile(metaBytes)
	if !ok || !meta.Verified || meta.Size != size || meta.MTimeNano != mtimeNano || !isDiskFileOwnedByNamespace(normNS, cleanKey, meta, true) {
		return nil, false
	}

	contentType := resolveContentType(meta.ContentType, filePath)

	etag := meta.ETag
	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", mtimeNano/1e9, size)
	}

	// Populate in-memory index for future fast lookups only if opToken is still valid
	m.persistMu.RLock()
	shard := m.tokenShardByCanon(tok.canonKey)
	shard.mu.Lock()
	if !isTokenValidLocked(shard, tok, m.generation) {
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return nil, false
	}
	if isSharedHostsPath {
		if curFi, sErr := os.Stat(filePath); sErr != nil || curFi.IsDir() || curFi.Size() != size || curFi.ModTime().UnixNano() != mtimeNano || !checkDiskFileOwnership(normNS, cleanKey, filePath) {
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			return nil, false
		}
	}
	m.setDiskMeta(ramKey, diskMetaEntry{
		namespace:       normNS,
		mtimeNano:       mtimeNano,
		size:            size,
		contentType:     contentType,
		contentEncoding: meta.ContentEncoding,
		etag:            etag,
		lastModified:    meta.LastModified,
		exists:          true,
		hasMeta:         true,
		verified:        true,
	})
	shard.mu.Unlock()
	m.persistMu.RUnlock()

	return &CacheItem{
		Key:             ramKey,
		ContentType:     contentType,
		ContentEncoding: meta.ContentEncoding,
		ETag:            etag,
		LastModified:    meta.LastModified,
		Size:            size,
		Data:            nil,
	}, true
}

// loadDiskMetaFast verifies disk cache metadata without reading the body.
// It verifies that:
// 1. The physical file exists, is not a directory, and has non-zero size.
// 2. The file's size and mtime match either the in-memory verified metadata or the .ext file metadata.
// 3. The cache item has been previously verified (Verified == true).
// If verified, it returns a CacheItem with Data == nil and ok == true.
// If unverified, changed, or missing, it returns nil, false.
func (m *Manager) loadDiskMetaFast(ns, cleanKey, filePath string) (*CacheItem, bool) {
	// Exclude special JS files that require runtime AST quarantine checking
	if m.autoRepair.Load() && strings.HasSuffix(cleanKey, "set-error-handler.js") {
		return nil, false
	}

	tok := m.acquireOpToken(ns, cleanKey)

	fi, err := os.Stat(filePath)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return nil, false
	}

	return m.loadDiskMetaWithStat(ns, cleanKey, filePath, fi.ModTime().UnixNano(), fi.Size(), tok)
}

// GetMetadataWithNamespace returns a CacheItem containing metadata (ContentType, ContentEncoding,
// ETag, LastModified, Size) without reading the underlying file body into memory (Data is nil for disk hits).
// It queries RAM-BOOST, RAM Cache, and verified disk cache metadata.
// If the asset is not in RAM and has not been verified on disk, it returns nil to allow the caller
// to fall back to the standard GetWithNamespace path.
func (m *Manager) GetMetadataWithNamespace(ns, urlPath string) (*CacheItem, string) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, ""
	}
	ns = normNS
	ramKey := makeRAMKey(ns, cleanKey)

	// 1. Check Resident Pool if Boost is active
	if m.residentPool != nil && m.residentPool.Enabled() {
		if item, ok := m.residentPool.Get(ramKey); ok {
			return item, "RAM-BOOST"
		}
	}

	// 2. Check RAM Cache
	if m.ramEnabled.Load() {
		if item, ok := m.ramCache.Get(ramKey); ok {
			return item, "RAM"
		}
	}

	// Negative cache check
	if m.isMissing(ramKey) {
		return nil, ""
	}

	// 3. Check Disk Cache metadata fastpath
	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, ""
	}

	if item, ok := m.loadDiskMetaFast(ns, cleanKey, filePath); ok {
		return item, "DISK"
	}

	// Check fallback path for "gbf" namespace
	if ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			if altPath, ok := m.resolvePathWithNamespace("gbf", altKey); ok {
				if item, ok := m.loadDiskMetaFast("gbf", altKey, altPath); ok {
					item.Key = ramKey
					return item, "DISK"
				}
			}
		}
	}

	return nil, ""
}

// VerifyAndGetMetadataWithNamespace validates an unverified disk cache file in-place (reading only
// the 512B header for uncompressed files or streaming Gzip) without loading the entire file into RAM,
// promotes its metadata to Verified=true, and returns a metadata-only CacheItem (Data == nil).
func (m *Manager) VerifyAndGetMetadataWithNamespace(ns, urlPath string) (*CacheItem, string) {
	if item, src := m.GetMetadataWithNamespace(ns, urlPath); item != nil {
		return item, src
	}

	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, ""
	}
	ns = normNS
	ramKey := makeRAMKey(ns, cleanKey)

	if m.isMissing(ramKey) {
		return nil, ""
	}

	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, ""
	}

	tok := m.acquireOpToken(ns, cleanKey)
	item, err := m.verifyDiskMetaOnlyWithToken(ns, cleanKey, filePath, tok)
	if err != nil {
		if ns == "gbf" {
			if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
				if altPath, ok := m.resolvePathWithNamespace("gbf", altKey); ok && altPath != "" {
					if item2, err2 := m.verifyDiskMetaOnlyWithToken("gbf", altKey, altPath, tok); err2 == nil && item2 != nil {
						item2.Key = ramKey
						m.persistMu.RLock()
						shard := m.tokenShardByCanon(tok.canonKey)
						shard.mu.Lock()
						if isTokenValidLocked(shard, tok, m.generation) {
							if altMeta, hasAlt := m.getDiskMeta(makeRAMKey("gbf", altKey)); hasAlt {
								m.setDiskMeta(ramKey, diskMetaEntry{
									namespace:       "gbf",
									mtimeNano:       altMeta.mtimeNano,
									size:            altMeta.size,
									contentType:     item2.ContentType,
									contentEncoding: item2.ContentEncoding,
									etag:            item2.ETag,
									lastModified:    item2.LastModified,
									exists:          true,
									hasMeta:         true,
									verified:        altMeta.verified,
								})
							}
						}
						shard.mu.Unlock()
						m.persistMu.RUnlock()
						return item2, "DISK"
					}
				}
			}
		}
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		if isTokenValidLocked(shard, tok, m.generation) {
			m.markMissing(ramKey)
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return nil, ""
	}
	return item, "DISK"
}

func (m *Manager) verifyDiskMetaOnly(ns, cleanKey, filePath string) (*CacheItem, error) {
	tok := m.acquireOpToken(ns, cleanKey)
	return m.verifyDiskMetaOnlyWithToken(ns, cleanKey, filePath, tok)
}

func (m *Manager) verifyDiskMetaOnlyWithToken(ns, cleanKey, filePath string, tok opToken) (*CacheItem, error) {
	ramKey := makeRAMKey(ns, cleanKey)
	normNS := sanitizeNamespace(ns)
	isSharedHostsPath := normNS != "gbf" || strings.HasPrefix(cleanKey, "hosts/")

	if m.autoRepair.Load() && strings.HasSuffix(cleanKey, "set-error-handler.js") {
		if m.CheckAndQuarantineTamperedJS(filePath) {
			m.deleteDiskMeta(ramKey)
			if m.residentPool != nil {
				m.residentPool.Delete(ramKey)
			}
			return nil, fmt.Errorf("tampered JS quarantined: %s", filePath)
		}
	}

	extPath := filePath + ".ext"
	contentType := ""
	contentEncoding := ""
	etag := ""
	lastMod := ""
	var parsedMeta diskMeta
	var hasExt bool
	var fi os.FileInfo
	var mtimeNano, size int64
	var magic [2]byte
	var nMagic int

	if isSharedHostsPath {
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		f, err := os.Open(filePath)
		if err != nil {
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			m.deleteDiskMeta(ramKey)
			return nil, err
		}
		fi, err = f.Stat()
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			_ = f.Close()
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			m.deleteDiskMeta(ramKey)
			return nil, fmt.Errorf("invalid cache file: %s", filePath)
		}
		mtimeNano = fi.ModTime().UnixNano()
		size = fi.Size()
		nMagic, _ = io.ReadFull(f, magic[:])
		_ = f.Close()
		metaBytes, rErr := os.ReadFile(extPath)
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		if rErr == nil {
			if meta, ok := parseExtFile(metaBytes); ok {
				parsedMeta = meta
				hasExt = true
				contentType = meta.ContentType
				contentEncoding = meta.ContentEncoding
				etag = meta.ETag
				lastMod = meta.LastModified
			}
		}
	} else {
		f, err := os.Open(filePath)
		if err != nil {
			m.deleteDiskMeta(ramKey)
			return nil, err
		}
		fi, err = f.Stat()
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			_ = f.Close()
			m.deleteDiskMeta(ramKey)
			return nil, fmt.Errorf("invalid cache file: %s", filePath)
		}
		mtimeNano = fi.ModTime().UnixNano()
		size = fi.Size()
		nMagic, _ = io.ReadFull(f, magic[:])
		_ = f.Close()
		if metaBytes, err := os.ReadFile(extPath); err == nil {
			if meta, ok := parseExtFile(metaBytes); ok {
				parsedMeta = meta
				hasExt = true
				contentType = meta.ContentType
				contentEncoding = meta.ContentEncoding
				etag = meta.ETag
				lastMod = meta.LastModified
			}
		}
	}
	if !isDiskFileOwnedByNamespace(normNS, cleanKey, parsedMeta, hasExt) {
		m.deleteDiskMeta(ramKey)
		return nil, fmt.Errorf("namespace mismatch for cache file: %s", filePath)
	}
	contentType = resolveContentType(contentType, filePath)

	if !IsValidCacheFile(cleanKey, contentType, filePath) {
		m.deleteDiskMeta(ramKey)
		if m.autoRepair.Load() {
			_ = os.Remove(filePath)
			_ = os.Remove(extPath)
			if m.residentPool != nil {
				m.residentPool.Delete(ramKey)
			}
		}
		return nil, fmt.Errorf("invalid cache content: %s", filePath)
	}

	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", mtimeNano/1e9, size)
	}
	if nMagic >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		contentEncoding = "gzip"
	} else if contentEncoding == "gzip" {
		contentEncoding = ""
	}

	m.persistMu.RLock()
	shard := m.tokenShardByCanon(tok.canonKey)
	shard.mu.Lock()
	if isTokenValidLocked(shard, tok, m.generation) {
		stillSame := true
		if isSharedHostsPath {
			if curFi, sErr := os.Stat(filePath); sErr != nil || curFi.IsDir() || !os.SameFile(fi, curFi) || curFi.Size() != size || curFi.ModTime().UnixNano() != mtimeNano || !checkDiskFileOwnership(normNS, cleanKey, filePath) {
				stillSame = false
			}
		}
		if !stillSame {
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			m.deleteDiskMeta(ramKey)
			return nil, fmt.Errorf("cache file ownership changed during verification: %s", filePath)
		}
		m.setDiskMeta(ramKey, diskMetaEntry{
			namespace:       normNS,
			mtimeNano:       mtimeNano,
			size:            size,
			contentType:     contentType,
			contentEncoding: contentEncoding,
			etag:            etag,
			lastModified:    lastMod,
			exists:          true,
			hasMeta:         true,
			verified:        true,
		})
		_ = writeDiskExtFile(filePath, diskMeta{
			Namespace:       normNS,
			LastModified:    lastMod,
			ETag:            etag,
			AccessTime:      time.Now().Unix(),
			ContentEncoding: contentEncoding,
			ContentType:     contentType,
			Version:         1,
			Verified:        true,
			Size:            size,
			MTimeNano:       mtimeNano,
		})
	}
	shard.mu.Unlock()
	m.persistMu.RUnlock()

	return &CacheItem{
		Key:             ramKey,
		Data:            nil,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            size,
	}, nil
}

func (m *Manager) Get(urlPath string) (*CacheItem, string) {
	return m.GetWithNamespace("gbf", urlPath)
}

func (m *Manager) GetWithNamespace(ns, urlPath string) (*CacheItem, string) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, ""
	}
	ns = normNS
	ramKey := makeRAMKey(ns, cleanKey)

	// 1. Check Resident Pool if Boost is active
	if m.residentPool != nil && m.residentPool.Enabled() {
		if item, ok := m.residentPool.Get(ramKey); ok {
			return item, "RAM-BOOST"
		}
	}

	// 2. Check RAM Cache
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

	tok := m.acquireOpToken(ns, cleanKey)
	usedAltKey := ""
	item, err := m.loadAndValidateDiskItemWithToken(ns, cleanKey, filePath, tok)
	if err != nil {
		if ns == "gbf" {
			// Try fallback: prepend or strip "assets" for gbf namespace
			if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
				if altPath, ok := m.resolvePathWithNamespace("gbf", altKey); ok && altPath != "" {
					if item2, err2 := m.loadAndValidateDiskItemWithToken("gbf", altKey, altPath, tok); err2 == nil && item2 != nil {
						item2.Key = ramKey
						item = item2
						usedAltKey = altKey
					} else {
						m.markMissingIfTokenValid(ramKey, tok)
						return nil, ""
					}
				} else {
					m.markMissingIfTokenValid(ramKey, tok)
					return nil, ""
				}
			} else {
				m.markMissingIfTokenValid(ramKey, tok)
				return nil, ""
			}
		} else {
			m.markMissingIfTokenValid(ramKey, tok)
			return nil, ""
		}
	}

	// Store into RAM cache and alias diskMeta only when opToken is still valid.
	// If the entry was invalidated while disk read was in flight, the in-flight request
	// completes using the loaded bytes, but MUST NOT backfill stale state into RAM or diskMeta.
	if item != nil {
		isSharedHostsPath := ns != "gbf" || strings.HasPrefix(cleanKey, "hosts/")
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		if isTokenValidLocked(shard, tok, m.generation) {
			canBackfill := true
			if isSharedHostsPath && !checkDiskFileOwnership(ns, cleanKey, filePath) {
				canBackfill = false
			}
			if canBackfill {
				if usedAltKey != "" {
					if altMeta, hasAlt := m.getDiskMeta(makeRAMKey("gbf", usedAltKey)); hasAlt {
						m.setDiskMeta(ramKey, diskMetaEntry{
							namespace:       "gbf",
							mtimeNano:       altMeta.mtimeNano,
							size:            altMeta.size,
							contentType:     item.ContentType,
							contentEncoding: item.ContentEncoding,
							etag:            item.ETag,
							lastModified:    item.LastModified,
							exists:          true,
							hasMeta:         true,
							verified:        altMeta.verified,
						})
					}
				}
				if m.ramEnabled.Load() && len(item.Data) > 0 && item.Size <= MaxDiskDirectReadSize {
					m.ramCache.SetProbation(ramKey, item)
				}
			}
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
	}
	return item, "DISK"
}

func (m *Manager) markMissingIfTokenValid(ramKey string, tok opToken) {
	m.persistMu.RLock()
	shard := m.tokenShardByCanon(tok.canonKey)
	shard.mu.Lock()
	if isTokenValidLocked(shard, tok, m.generation) {
		m.markMissing(ramKey)
	}
	shard.mu.Unlock()
	m.persistMu.RUnlock()
}

func (m *Manager) loadAndValidateDiskItem(ns string, cleanKey string, filePath string) (*CacheItem, error) {
	tok := m.acquireOpToken(ns, cleanKey)
	return m.loadAndValidateDiskItemWithToken(ns, cleanKey, filePath, tok)
}

func (m *Manager) loadAndValidateDiskItemWithToken(ns string, cleanKey string, filePath string, tok opToken) (*CacheItem, error) {
	ramKey := makeRAMKey(ns, cleanKey)
	normNS := sanitizeNamespace(ns)
	isSharedHostsPath := normNS != "gbf" || strings.HasPrefix(cleanKey, "hosts/")

	if m.autoRepair.Load() && strings.HasSuffix(cleanKey, "set-error-handler.js") {
		if m.CheckAndQuarantineTamperedJS(filePath) {
			m.deleteDiskMeta(ramKey)
			if m.residentPool != nil {
				m.residentPool.Delete(ramKey)
			}
			return nil, fmt.Errorf("tampered JS quarantined: %s", filePath)
		}
	}

	extPath := filePath + ".ext"
	contentType := ""
	contentEncoding := ""
	etag := ""
	lastMod := ""
	needsIndexSave := false
	extVerified := false
	var fi os.FileInfo
	var mtimeNano, size int64
	var data []byte

	if isSharedHostsPath {
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		f, err := os.Open(filePath)
		if err != nil {
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			m.deleteDiskMeta(ramKey)
			return nil, err
		}
		fi, err = f.Stat()
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			_ = f.Close()
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			m.deleteDiskMeta(ramKey)
			if fi != nil && fi.Size() == 0 {
				return nil, fmt.Errorf("empty cache file: %s", filePath)
			}
			return nil, fmt.Errorf("invalid cache file: %s", filePath)
		}
		mtimeNano = fi.ModTime().UnixNano()
		size = fi.Size()
		data, err = readDiskFile(f, size)
		if err == nil && int64(len(data)) != size {
			size = int64(len(data))
			if fi2, err2 := f.Stat(); err2 == nil {
				mtimeNano = fi2.ModTime().UnixNano()
			}
		}
		_ = f.Close()
		if err != nil || len(data) == 0 {
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			m.deleteDiskMeta(ramKey)
			return nil, fmt.Errorf("failed to read cache file: %s", filePath)
		}
		metaBytes, rErr := os.ReadFile(extPath)
		shard.mu.Unlock()
		m.persistMu.RUnlock()

		var parsedMeta diskMeta
		var hasExt bool
		if rErr == nil {
			if meta, ok := parseExtFile(metaBytes); ok {
				parsedMeta = meta
				hasExt = true
				contentType = meta.ContentType
				contentEncoding = meta.ContentEncoding
				etag = meta.ETag
				lastMod = meta.LastModified
				if meta.Verified && meta.Size == size && meta.MTimeNano == mtimeNano {
					extVerified = true
				}
			}
		}
		if !isDiskFileOwnedByNamespace(normNS, cleanKey, parsedMeta, hasExt) {
			m.deleteDiskMeta(ramKey)
			return nil, fmt.Errorf("namespace mismatch for cache file: %s", filePath)
		}
		if meta, ok := m.getDiskMeta(ramKey); !ok || !meta.hasMeta || meta.mtimeNano != mtimeNano || meta.size != size || meta.namespace != normNS {
			needsIndexSave = true
		}
	} else {
		f, err := os.Open(filePath)
		if err != nil {
			m.deleteDiskMeta(ramKey)
			return nil, err
		}

		fi, err = f.Stat()
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			_ = f.Close()
			m.deleteDiskMeta(ramKey)
			if fi != nil && fi.Size() == 0 {
				return nil, fmt.Errorf("empty cache file: %s", filePath)
			}
			return nil, fmt.Errorf("invalid cache file: %s", filePath)
		}

		mtimeNano = fi.ModTime().UnixNano()
		size = fi.Size()

		data, err = readDiskFile(f, size)
		if err == nil && int64(len(data)) != size {
			size = int64(len(data))
			if fi2, err2 := f.Stat(); err2 == nil {
				mtimeNano = fi2.ModTime().UnixNano()
			}
		}
		_ = f.Close()
		if err != nil || len(data) == 0 {
			m.deleteDiskMeta(ramKey)
			return nil, fmt.Errorf("failed to read cache file: %s", filePath)
		}

		if meta, ok := m.getDiskMeta(ramKey); ok && meta.hasMeta && meta.mtimeNano == mtimeNano && meta.size == size && (meta.namespace == "" || meta.namespace == normNS) {
			contentType = meta.contentType
			contentEncoding = meta.contentEncoding
			etag = meta.etag
			lastMod = meta.lastModified
			extVerified = meta.verified
		} else {
			needsIndexSave = true
			var parsedMeta diskMeta
			var hasExt bool
			if metaBytes, err := os.ReadFile(extPath); err == nil {
				if meta, ok := parseExtFile(metaBytes); ok {
					parsedMeta = meta
					hasExt = true
					contentType = meta.ContentType
					contentEncoding = meta.ContentEncoding
					etag = meta.ETag
					lastMod = meta.LastModified
					if meta.Verified && meta.Size == size && meta.MTimeNano == mtimeNano {
						extVerified = true
					}
				}
			}
			if !isDiskFileOwnedByNamespace(normNS, cleanKey, parsedMeta, hasExt) {
				m.deleteDiskMeta(ramKey)
				return nil, fmt.Errorf("namespace mismatch for cache file: %s", filePath)
			}
		}
	}

	contentType = resolveContentType(contentType, filePath)

	validContent := false
	if extVerified && len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		// This file previously passed full Gzip stream + inner magic/HTML validation
		// and its physical size + mtimeNano still match the verified metadata.
		validContent = !strings.Contains(strings.ToLower(contentType), "text/html")
	} else {
		validContent = IsValidCacheContent(cleanKey, contentType, data)
	}
	if !validContent {
		m.deleteDiskMeta(ramKey)
		if m.autoRepair.Load() {
			_ = os.Remove(filePath)
			_ = os.Remove(extPath)
			if m.residentPool != nil {
				m.residentPool.Delete(ramKey)
			}
		}
		return nil, fmt.Errorf("invalid cache content: %s", filePath)
	}

	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", mtimeNano/1e9, size)
	}

	// Check gzip signature
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		contentEncoding = "gzip"
	} else if contentEncoding == "gzip" {
		// Strip misleading gzip encoding from decompressed plaintext to prevent ERR_CONTENT_DECODING_FAILED
		contentEncoding = ""
	}

	if needsIndexSave || !extVerified {
		m.persistMu.RLock()
		shard := m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		if isTokenValidLocked(shard, tok, m.generation) {
			stillSame := true
			if isSharedHostsPath {
				if curFi, sErr := os.Stat(filePath); sErr != nil || curFi.IsDir() || !os.SameFile(fi, curFi) || curFi.Size() != size || curFi.ModTime().UnixNano() != mtimeNano || !checkDiskFileOwnership(normNS, cleanKey, filePath) {
					stillSame = false
				}
			}
			if !stillSame {
				shard.mu.Unlock()
				m.persistMu.RUnlock()
				m.deleteDiskMeta(ramKey)
				return nil, fmt.Errorf("cache file ownership changed during load: %s", filePath)
			}
			m.setDiskMeta(ramKey, diskMetaEntry{
				namespace:       normNS,
				mtimeNano:       mtimeNano,
				size:            size,
				contentType:     contentType,
				contentEncoding: contentEncoding,
				etag:            etag,
				lastModified:    lastMod,
				exists:          true,
				hasMeta:         true,
				verified:        true,
			})
			if !extVerified {
				_ = writeDiskExtFile(filePath, diskMeta{
					Namespace:       normNS,
					LastModified:    lastMod,
					ETag:            etag,
					AccessTime:      time.Now().Unix(),
					ContentEncoding: contentEncoding,
					ContentType:     contentType,
					Version:         1,
					Verified:        true,
					Size:            size,
					MTimeNano:       mtimeNano,
				})
			}
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
	}

	return &CacheItem{
		Key:             ramKey,
		Data:            data,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            size,
	}, nil
}

// readDiskFile reads the complete contents of f using a buffer preallocated
// according to known file size, preserving byte-for-byte integrity and handling
// edge cases such as files shrinking or growing between Stat and Read.
func readDiskFile(f *os.File, size int64) ([]byte, error) {
	if size <= 0 || size > 1<<30 {
		return io.ReadAll(f)
	}

	data := make([]byte, 0, size+1)
	for {
		if len(data) >= cap(data) {
			d := append(data[:cap(data)], 0)
			data = d[:len(data)]
		}
		n, err := f.Read(data[len(data):cap(data)])
		data = data[:len(data)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return data, err
		}
	}
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

func (m *Manager) saveRAMInternal(ns, urlPath string, headers map[string]string, data []byte, isProbation bool, validated bool) (*CacheItem, string, string, uint64, opToken, bool) {
	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	generation := m.generation
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok || len(data) == 0 {
		return nil, "", "", 0, opToken{}, false
	}
	ns = normNS
	ct := getHeader(headers, "content-type")
	if !validated && !IsValidCacheContent(cleanKey, ct, data) {
		return nil, "", "", 0, opToken{}, false
	}

	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, "", "", 0, opToken{}, false
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

	shard, canonKey := m.tokenShardFor(ns, cleanKey)
	shard.mu.Lock()
	tok := opToken{
		gen:      generation,
		seq:      shard.seq,
		canonKey: canonKey,
	}

	if m.ramEnabled.Load() {
		if isProbation {
			m.ramCache.SetProbation(ramKey, item)
		} else {
			m.ramCache.Set(ramKey, item)
		}
	}

	// Section V.5: Invalidate old Resident Pool entry when new cache item arrives
	if m.residentPool != nil {
		m.residentPool.Delete(ramKey)
	}
	m.deleteMissingKey(ramKey)

	if ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			altRamKey := makeRAMKey("gbf", altKey)
			if m.ramCache != nil {
				m.ramCache.Delete(altRamKey)
			}
			if m.residentPool != nil {
				m.residentPool.Delete(altRamKey)
			}
			m.deleteMissingKey(altRamKey)
		}
	}
	shard.mu.Unlock()

	return item, cleanKey, filePath, generation, tok, true
}

// SaveRAM saves an asset into RAM (Protected segment) with mandatory IsValidCacheContent validation,
// and enqueues asynchronous disk persistence.
func (m *Manager) SaveRAM(urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.SaveRAMWithNamespace("gbf", urlPath, headers, data)
}

// SaveRAMWithNamespace saves an asset into RAM (Protected segment) with mandatory IsValidCacheContent validation,
// and enqueues asynchronous disk persistence.
func (m *Manager) SaveRAMWithNamespace(ns, urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.saveRAMWithNamespaceAndValidation(ns, urlPath, headers, data, false, false)
}

// SaveRAMValidated saves a pre-validated asset into RAM (Protected segment) and enqueues disk persistence.
// PRECONDITION: The caller MUST have already verified IsValidCacheContent(cleanKey, contentType, data).
// Calling this with unvalidated data violates P0 Byte-for-Byte Integrity and cache safety standards.
// For untrusted/unverified inputs, use SaveRAM or SaveRAMWithNamespace instead.
func (m *Manager) SaveRAMValidated(urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.SaveRAMValidatedWithNamespace("gbf", urlPath, headers, data)
}

// SaveRAMValidatedWithNamespace saves a pre-validated asset into RAM (Protected segment) and enqueues disk persistence.
// PRECONDITION: The caller MUST have already verified IsValidCacheContent(cleanKey, contentType, data).
// This is strictly intended for internal fast paths (such as proxy upstream fetch) where validation
// has already been performed immediately prior to admission, avoiding redundant validation passes.
// For untrusted/unverified inputs, use SaveRAM or SaveRAMWithNamespace instead.
func (m *Manager) SaveRAMValidatedWithNamespace(ns, urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.saveRAMWithNamespaceAndValidation(ns, urlPath, headers, data, false, true)
}

// SavePrefetch saves an asset into RAM (Probationary segment) with mandatory IsValidCacheContent validation,
// and enqueues asynchronous disk persistence.
func (m *Manager) SavePrefetch(urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.SavePrefetchWithNamespace("gbf", urlPath, headers, data)
}

// SavePrefetchWithNamespace saves an asset into RAM (Probationary segment) with mandatory IsValidCacheContent validation,
// and enqueues asynchronous disk persistence.
func (m *Manager) SavePrefetchWithNamespace(ns, urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.saveRAMWithNamespaceAndValidation(ns, urlPath, headers, data, true, false)
}

// SavePrefetchValidated saves a pre-validated asset into RAM (Probationary segment) and enqueues disk persistence.
// PRECONDITION: The caller MUST have already verified IsValidCacheContent(cleanKey, contentType, data).
// Calling this with unvalidated data violates P0 Byte-for-Byte Integrity and cache safety standards.
// For untrusted/unverified inputs, use SavePrefetch or SavePrefetchWithNamespace instead.
func (m *Manager) SavePrefetchValidated(urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.SavePrefetchValidatedWithNamespace("gbf", urlPath, headers, data)
}

// SavePrefetchValidatedWithNamespace saves a pre-validated asset into RAM (Probationary segment) and enqueues disk persistence.
// PRECONDITION: The caller MUST have already verified IsValidCacheContent(cleanKey, contentType, data).
// This is strictly intended for internal fast paths (such as prefetch engine fetch) where validation
// has already been performed immediately prior to admission, avoiding redundant validation passes.
// For untrusted/unverified inputs, use SavePrefetch or SavePrefetchWithNamespace instead.
func (m *Manager) SavePrefetchValidatedWithNamespace(ns, urlPath string, headers map[string]string, data []byte) (*CacheItem, bool) {
	return m.saveRAMWithNamespaceAndValidation(ns, urlPath, headers, data, true, true)
}

func (m *Manager) saveRAMWithNamespaceAndValidation(ns, urlPath string, headers map[string]string, data []byte, isProbation bool, validated bool) (*CacheItem, bool) {
	normNS, normKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, false
	}
	item, cleanKey, filePath, generation, tok, ok := m.saveRAMInternal(normNS, normKey, headers, data, isProbation, validated)
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
		ns:         normNS,
		cleanKey:   cleanKey,
		filePath:   filePath,
		headers:    headers,
		data:       data,
		generation: generation,
		token:      tok,
	}:
	default:
		// Queue full, drop disk persist without delaying foreground
	}

	return item, true
}

// Save saves an asset into RAM and synchronously persists it to disk,
// enforcing mandatory IsValidCacheContent validation.
func (m *Manager) Save(urlPath string, headers map[string]string, data []byte) bool {
	return m.SaveWithNamespace("gbf", urlPath, headers, data)
}

// SaveWithNamespace saves an asset into RAM and synchronously persists it to disk,
// enforcing mandatory IsValidCacheContent validation.
func (m *Manager) SaveWithNamespace(ns, urlPath string, headers map[string]string, data []byte) bool {
	normNS, normKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return false
	}
	item, cleanKey, filePath, generation, tok, ok := m.saveRAMInternal(normNS, normKey, headers, data, false, false)
	if !ok || item == nil {
		return false
	}
	return m.saveToDiskWithNSAndToken(normNS, cleanKey, filePath, headers, data, generation, tok)
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
	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	var tok opToken
	ns, cleanKey, _, ok := resolveDiskFileOwner(base, filePath)
	if ok {
		tok = m.acquireOpTokenWithGen(ns, cleanKey, generation)
	}
	return m.saveToDiskWithNSAndToken(ns, cleanKey, filePath, headers, data, generation, tok)
}

func (m *Manager) saveToDiskWithToken(filePath string, headers map[string]string, data []byte, generation uint64, tok opToken) bool {
	return m.saveToDiskWithNSAndToken("", "", filePath, headers, data, generation, tok)
}

func (m *Manager) saveToDiskWithNSAndToken(ns, cleanKey, filePath string, headers map[string]string, data []byte, generation uint64, tok opToken) bool {
	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	if generation != m.generation {
		return false
	}

	m.mu.RLock()
	base := m.cacheBase
	m.mu.RUnlock()
	if ns == "" || cleanKey == "" {
		if extNS, extKey, _, ok := resolveDiskFileOwner(base, filePath); ok {
			ns = extNS
			cleanKey = extKey
		}
	}
	normNS := sanitizeNamespace(ns)
	normKey := cleanKey
	if cNS, cKey, ok := canonicalizeNSAndKey(ns, cleanKey); ok {
		normNS = cNS
		normKey = cKey
	}
	ramKey := makeRAMKey(normNS, normKey)

	shard, _ := m.tokenShardFor(normNS, normKey)
	if tok.canonKey != "" {
		shard = m.tokenShardByCanon(tok.canonKey)
		shard.mu.Lock()
		valid := isTokenValidLocked(shard, tok, m.generation)
		shard.mu.Unlock()
		if !valid {
			return false
		}
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
		if ramKey != "" {
			m.deleteDiskMeta(ramKey)
		}
		return false
	}

	shard.mu.Lock()
	defer shard.mu.Unlock()
	if tok.canonKey != "" && !isTokenValidLocked(shard, tok, m.generation) {
		_ = os.Remove(tmpDataPath)
		return false
	}

	m.clearConflictingPhysicalPathState(normNS, normKey)
	isSharedHostsPath := normNS != "gbf" || strings.HasPrefix(normKey, "hosts/")

	if err := renameWithRetry(tmpDataPath, filePath, 3); err != nil {
		_ = os.Remove(tmpDataPath)
		if ramKey != "" {
			m.deleteDiskMeta(ramKey)
		}
		return false
	}

	// Write .ext
	fi, err := os.Stat(filePath)
	var mtimeNano, fileSize int64
	if err == nil {
		mtimeNano = fi.ModTime().UnixNano()
		fileSize = fi.Size()
	} else {
		fileSize = int64(len(data))
		mtimeNano = time.Now().UnixNano()
	}
	meta := diskMeta{
		LastModified:    lastMod,
		ETag:            etag,
		AccessTime:      time.Now().Unix(),
		ContentEncoding: ce,
		ContentType:     ct,
		Version:         1,
		Verified:        true,
		Size:            fileSize,
		MTimeNano:       mtimeNano,
		Namespace:       normNS,
	}
	if err := writeDiskExtFile(filePath, meta); err != nil && isSharedHostsPath {
		_ = os.Remove(filePath)
		_ = os.Remove(filePath + ".ext")
		if ramKey != "" {
			m.deleteDiskMeta(ramKey)
		}
		return false
	}

	if ramKey != "" && fi != nil {
		m.setDiskMeta(ramKey, diskMetaEntry{
			mtimeNano:       mtimeNano,
			size:            fileSize,
			contentType:     ct,
			contentEncoding: ce,
			etag:            etag,
			lastModified:    lastMod,
			namespace:       normNS,
			exists:          true,
			hasMeta:         true,
			verified:        true,
		})
	}
	m.clearConflictingPhysicalPathState(normNS, normKey)

	return true
}

// MaxRAMItemSize returns the maximum byte size of a single asset that can be admitted
// into the RAM cache (specifically, a single SLRU shard capacity).
// Assets exceeding this size will not fit into RAM cache and should be persisted
// directly to disk cache.
func (m *Manager) MaxRAMItemSize() int64 {
	if m == nil || !m.ramEnabled.Load() || m.ramCache == nil {
		return 0
	}
	maxBytes := m.ramCache.MaxBytes()
	if maxBytes <= 0 {
		return 16 * 1024 * 1024 // Default fallback: 16 MB
	}
	numShards := int64(m.ramCache.numShards)
	if numShards <= 0 {
		numShards = 1
	}
	shardMax := maxBytes / numShards
	if shardMax <= 0 {
		return 16 * 1024 * 1024
	}
	return shardMax
}

// ResolvePathWithNamespace returns the absolute disk cache path for an asset.
func (m *Manager) ResolvePathWithNamespace(ns, urlPath string) (string, bool) {
	return m.resolvePathWithNamespace(ns, urlPath)
}

// ReadDiskItemData loads raw bytes from disk for the given namespace and URL path.
func (m *Manager) ReadDiskItemData(ns, urlPath string) ([]byte, error) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, fmt.Errorf("failed to resolve path for %s", urlPath)
	}
	ns = normNS
	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, fmt.Errorf("failed to resolve path for %s", urlPath)
	}
	var data []byte
	var err error
	if checkDiskFileOwnership(ns, cleanKey, filePath) {
		data, err = os.ReadFile(filePath)
	} else {
		err = os.ErrNotExist
	}
	if err != nil && ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			if altPath, ok := m.resolvePathWithNamespace("gbf", altKey); ok && checkDiskFileOwnership("gbf", altKey, altPath) {
				if altData, altErr := os.ReadFile(altPath); altErr == nil {
					return altData, nil
				}
			}
		}
	}
	return data, err
}

// OpenDiskStream opens a file from the disk cache for streaming.
// It verifies that the opened file's actual size and mtime match the previously verified metadata.
// If size or mtime has changed after verification, the cache is treated as invalidated and an error is returned.
// It handles namespace resolution and the "assets/" fallback for the "gbf" namespace.
// The caller is responsible for closing the returned ReadCloser.
func (m *Manager) OpenDiskStream(ns, urlPath string) (io.ReadCloser, int64, error) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, 0, fmt.Errorf("failed to resolve path: %s", urlPath)
	}
	ns = normNS
	tok := m.acquireOpToken(ns, cleanKey)

	openFileWithFallback := func(k string) (*os.File, int64, error) {
		p, ok := m.resolvePathWithNamespace(ns, k)
		if !ok {
			return nil, 0, fmt.Errorf("failed to resolve path: %s", k)
		}
		if !checkDiskFileOwnership(ns, k, p) {
			return nil, 0, fmt.Errorf("cache file namespace mismatch: %s", p)
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, 0, err
		}
		fi, err := f.Stat()
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			_ = f.Close()
			if fi != nil && fi.Size() == 0 {
				return nil, 0, fmt.Errorf("empty cache file: %s", p)
			}
			return nil, 0, fmt.Errorf("invalid cache file: %s", p)
		}

		actualSize := fi.Size()
		actualMtime := fi.ModTime().UnixNano()
		isSharedHostsPath := ns != "gbf" || strings.HasPrefix(k, "hosts/")
		if isSharedHostsPath {
			m.persistMu.RLock()
			shard := m.tokenShardByCanon(tok.canonKey)
			shard.mu.Lock()
			curFi, sErr := os.Stat(p)
			sameOwner := sErr == nil && !curFi.IsDir() && os.SameFile(fi, curFi) && curFi.Size() == actualSize && curFi.ModTime().UnixNano() == actualMtime && checkDiskFileOwnership(ns, k, p)
			shard.mu.Unlock()
			m.persistMu.RUnlock()
			if !sameOwner {
				_ = f.Close()
				return nil, 0, fmt.Errorf("cache file replaced or ownership changed: %s", p)
			}
		}

		// Requirement 3: Check actual size + mtime against verified metadata.
		// If inconsistent, treat as cache invalidation.
		ramKey := makeRAMKey(ns, k)
		matched := false
		if meta, ok := m.getDiskMeta(ramKey); ok && meta.hasMeta && meta.verified && (meta.namespace == "" || meta.namespace == ns) {
			if actualSize == meta.size && actualMtime == meta.mtimeNano {
				matched = true
			} else {
				_ = f.Close()
				m.deleteDiskMeta(ramKey)
				return nil, 0, fmt.Errorf("cache file size/mtime modified after verification: %s (size %d vs %d, mtime %d vs %d)", p, actualSize, meta.size, actualMtime, meta.mtimeNano)
			}
		} else if metaBytes, err := os.ReadFile(p + ".ext"); err == nil {
			if extMeta, ok := parseExtFile(metaBytes); ok && extMeta.Verified && isDiskFileOwnedByNamespace(ns, k, extMeta, true) {
				if actualSize == extMeta.Size && actualMtime == extMeta.MTimeNano {
					m.persistMu.RLock()
					shard := m.tokenShardByCanon(tok.canonKey)
					shard.mu.Lock()
					valid := isTokenValidLocked(shard, tok, m.generation)
					if valid && isSharedHostsPath {
						if curFi, sErr := os.Stat(p); sErr != nil || curFi.IsDir() || !os.SameFile(fi, curFi) || !checkDiskFileOwnership(ns, k, p) {
							valid = false
						}
					}
					if valid {
						matched = true
						m.setDiskMeta(ramKey, diskMetaEntry{
							mtimeNano:       actualMtime,
							size:            actualSize,
							contentType:     resolveContentType(extMeta.ContentType, p),
							contentEncoding: extMeta.ContentEncoding,
							etag:            extMeta.ETag,
							lastModified:    extMeta.LastModified,
							namespace:       ns,
							exists:          true,
							hasMeta:         true,
							verified:        true,
						})
					}
					shard.mu.Unlock()
					m.persistMu.RUnlock()
					if !valid {
						_ = f.Close()
						return nil, 0, fmt.Errorf("cache item invalidated during stream open: %s", p)
					}
				} else {
					_ = f.Close()
					m.deleteDiskMeta(ramKey)
					return nil, 0, fmt.Errorf("cache file size/mtime mismatch with .ext metadata: %s (size %d vs %d, mtime %d vs %d)", p, actualSize, extMeta.Size, actualMtime, extMeta.MTimeNano)
				}
			}
		}

		if !matched && k != cleanKey {
			origKey := makeRAMKey(ns, cleanKey)
			if meta, ok := m.getDiskMeta(origKey); ok && meta.hasMeta && meta.verified && (meta.namespace == "" || meta.namespace == ns) {
				if actualSize == meta.size && actualMtime == meta.mtimeNano {
					matched = true
				} else {
					_ = f.Close()
					m.deleteDiskMeta(origKey)
					return nil, 0, fmt.Errorf("cache file size/mtime modified after verification: %s", p)
				}
			}
		}

		if !matched {
			_ = f.Close()
			return nil, 0, fmt.Errorf("cache file has no verified metadata: %s", p)
		}

		return f, actualSize, nil
	}

	f, size, err := openFileWithFallback(cleanKey)
	if err == nil {
		return f, size, nil
	}

	if ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			if altF, altSize, altErr := openFileWithFallback(altKey); altErr == nil {
				return altF, altSize, nil
			}
		}
	}
	return nil, 0, err
}

// Invalidate removes an asset from memory cache and disk metadata, and deletes the cached file from disk.
func (m *Manager) Invalidate(ns, urlPath string) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return
	}
	ns = normNS

	m.persistMu.RLock()
	shard, canonKey := m.tokenShardFor(ns, cleanKey)
	shard.mu.Lock()

	bumpInvalidateTokenLocked(shard, canonKey)
	m.clearKeyMemoryState(ns, cleanKey)

	if p, ok := m.resolvePathWithNamespace(ns, cleanKey); ok {
		_ = os.Remove(p)
		_ = os.Remove(p + ".ext")
	}

	if ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			if altPath, ok := m.resolvePathWithNamespace("gbf", altKey); ok {
				_ = os.Remove(altPath)
				_ = os.Remove(altPath + ".ext")
			}
		}
	}

	shard.mu.Unlock()
	m.persistMu.RUnlock()

	if m.ramBudget != nil && m.residentPool != nil && m.residentPool.Enabled() {
		m.ramBudget.SyncSLRU(m.ramCache)
	}
	m.invalidateSnapshots("已执行缓存定点失效")
}

// CreateDiskTempFile creates a secure temporary file in the cache directory adjacent to the destination path.
// The file is opened with O_CREATE|os.O_WRONLY|os.O_EXCL.
// Returns the file handle, temporary path, cache generation, and error.
func (m *Manager) CreateDiskTempFile(ns, urlPath string) (*os.File, string, uint64, error) {
	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		return nil, "", 0, fmt.Errorf("failed to resolve path for %s", urlPath)
	}
	ns = normNS

	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	generation := m.generation
	tok := m.acquireOpTokenWithGen(ns, cleanKey, generation)

	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		return nil, "", 0, fmt.Errorf("failed to resolve path for %s", urlPath)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, "", 0, err
	}

	pid := os.Getpid()
	ts := time.Now().UnixNano()
	seq := tmpFileSeq.Add(1)
	tmpDataPath := fmt.Sprintf("%s.tmp.%d.%d.%d", filePath, pid, ts, seq)

	f, err := os.OpenFile(tmpDataPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return nil, "", 0, err
	}
	if !m.registerTempFileTokenLocked(tmpDataPath, tok) {
		_ = f.Close()
		_ = os.Remove(tmpDataPath)
		return nil, "", 0, fmt.Errorf("cache generation changed during temp file creation")
	}
	return f, tmpDataPath, generation, nil
}

// CommitDiskTempFile commits a completed temporary download file to the disk cache atomically.
// It renames the temp file to the final path, writes the .ext metadata file, updates diskMeta,
// and clears any negative cache entry.
func (m *Manager) CommitDiskTempFile(ns, urlPath string, headers map[string]string, tmpFilePath string, totalSize int64, generation uint64) (*CacheItem, bool) {
	tok, hasTok := m.popTempFileToken(tmpFilePath)

	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	if generation != m.generation {
		_ = os.Remove(tmpFilePath)
		return nil, false
	}

	normNS, cleanKey, ok := canonicalizeNSAndKey(ns, urlPath)
	if !ok {
		_ = os.Remove(tmpFilePath)
		return nil, false
	}
	ns = normNS
	filePath, ok := m.resolvePathWithNamespace(ns, cleanKey)
	if !ok {
		_ = os.Remove(tmpFilePath)
		return nil, false
	}

	shard, _ := m.tokenShardFor(ns, cleanKey)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if !hasTok || !isTokenValidLocked(shard, tok, m.generation) {
		_ = os.Remove(tmpFilePath)
		return nil, false
	}

	ramKey := makeRAMKey(ns, cleanKey)
	m.clearConflictingPhysicalPathState(ns, cleanKey)
	isSharedHostsPath := ns != "gbf" || strings.HasPrefix(cleanKey, "hosts/")

	if err := renameWithRetry(tmpFilePath, filePath, 3); err != nil {
		_ = os.Remove(tmpFilePath)
		m.deleteDiskMeta(ramKey)
		return nil, false
	}

	ce := getHeader(headers, "content-encoding")
	if ce == "gzip" {
		var magic [2]byte
		if f, err := os.Open(filePath); err == nil {
			n, _ := io.ReadFull(f, magic[:])
			_ = f.Close()
			if n < 2 || magic[0] != 0x1f || magic[1] != 0x8b {
				ce = ""
			}
		}
	}
	etag := getHeader(headers, "etag")
	if etag == "" {
		etag = fmt.Sprintf("\"%x-%x\"", time.Now().Unix(), totalSize)
	}
	lastMod := getHeader(headers, "last-modified")
	ct := getHeader(headers, "content-type")

	// Write .ext
	fi, err := os.Stat(filePath)
	var mtimeNano int64
	if err == nil {
		mtimeNano = fi.ModTime().UnixNano()
	} else {
		mtimeNano = time.Now().UnixNano()
	}
	meta := diskMeta{
		LastModified:    lastMod,
		ETag:            etag,
		AccessTime:      time.Now().Unix(),
		ContentEncoding: ce,
		ContentType:     ct,
		Version:         1,
		Verified:        true,
		Size:            totalSize,
		MTimeNano:       mtimeNano,
		Namespace:       ns,
	}
	if err := writeDiskExtFile(filePath, meta); err != nil && isSharedHostsPath {
		_ = os.Remove(filePath)
		_ = os.Remove(filePath + ".ext")
		m.deleteDiskMeta(ramKey)
		return nil, false
	}

	if fi != nil {
		m.setDiskMeta(ramKey, diskMetaEntry{
			mtimeNano:       mtimeNano,
			size:            fi.Size(),
			contentType:     ct,
			contentEncoding: ce,
			etag:            etag,
			lastModified:    lastMod,
			namespace:       ns,
			exists:          true,
			hasMeta:         true,
			verified:        true,
		})
	}
	m.clearConflictingPhysicalPathState(ns, cleanKey)

	m.ramCache.Delete(ramKey)
	if m.residentPool != nil {
		m.residentPool.Delete(ramKey)
	}
	m.deleteMissingKey(ramKey)

	if ns == "gbf" {
		if altKey, hasAlt := gbfFallbackCleanKey(cleanKey); hasAlt {
			altRamKey := makeRAMKey("gbf", altKey)
			m.ramCache.Delete(altRamKey)
			if m.residentPool != nil {
				m.residentPool.Delete(altRamKey)
			}
			m.deleteMissingKey(altRamKey)
		}
	}

	item := &CacheItem{
		Key:             ramKey,
		ContentType:     ct,
		ContentEncoding: ce,
		ETag:            etag,
		LastModified:    lastMod,
		Size:            totalSize,
	}

	return item, true
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
	m.DisableBoost()
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
	m.clearDiskMeta()
	m.clearTokenShards()
	m.clearTempTokens()
	m.invalidateSnapshots("已执行全盘清空")

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
				m.persistMu.Lock()
				_ = os.Remove(p)
				m.persistMu.Unlock()
			}
			return nil
		}
		scanned++

		ns, cleanKey, _, ok := resolveDiskFileOwner(base, p)
		if !ok {
			return nil
		}

		if strings.HasSuffix(p, "set-error-handler.js") {
			m.persistMu.Lock()
			quarantined := m.CheckAndQuarantineTamperedJS(p)
			if quarantined {
				corrupted++
				shard, canonKey := m.tokenShardFor(ns, cleanKey)
				shard.mu.Lock()
				bumpInvalidateTokenLocked(shard, canonKey)
				m.clearKeyMemoryState(ns, cleanKey)
				shard.mu.Unlock()
			}
			m.persistMu.Unlock()
			if quarantined {
				return nil
			}
		}

		isBad := false
		info, iErr := d.Info()
		if iErr != nil || info.Size() == 0 {
			isBad = true
		} else if !IsValidCacheFile(p, "", p) {
			isBad = true
		}

		if isBad {
			corrupted++
			m.persistMu.Lock()
			shard, canonKey := m.tokenShardFor(ns, cleanKey)
			shard.mu.Lock()
			bumpInvalidateTokenLocked(shard, canonKey)
			_ = os.Remove(p)
			_ = os.Remove(p + ".ext")
			m.clearKeyMemoryState(ns, cleanKey)
			shard.mu.Unlock()
			m.persistMu.Unlock()
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

	if corrupted > 0 {
		m.invalidateSnapshots("缓存体检清理了损坏文件")
	}

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
	if keepCount <= 0 {
		keepCount = DefaultVersionKeep
	}
	res, err := m.PruneStaleVersionsDetailed(keepCount, "", progressCb, cancelCh)
	if err != nil || res == nil {
		return 0, 0, 0
	}
	return res.DeletedDirs, res.DeletedFiles, res.FreedBytes
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
		ns, cleanKey, _, ok := resolveDiskFileOwner(base, c.path)
		if !ok {
			continue
		}
		if item, _ := m.GetWithNamespace(ns, cleanKey); item != nil {
			loaded++
		}
	}
	return loaded
}
