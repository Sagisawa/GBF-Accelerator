package cache

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Category constants for static cache classification.
const (
	CategoryVersioned  = "versioned"
	CategoryBattleCJS  = "battle_cjs"
	CategoryStory      = "story"
	CategoryCoreUI     = "core_ui"
	CategoryEventGacha = "event_gacha"
	CategoryAudio      = "audio"
	CategoryOther      = "other"
)

// AllCategories defines the canonical display and reporting order for categories.
var AllCategories = []string{
	CategoryVersioned,
	CategoryBattleCJS,
	CategoryStory,
	CategoryCoreUI,
	CategoryEventGacha,
	CategoryAudio,
	CategoryOther,
}

var categoryMeta = map[string]struct {
	Label       string
	Description string
	Selectable  bool
}{
	CategoryVersioned: {
		Label:       "版本化静态资源",
		Description: "位于 assets / assets_en / assets_jp 数字版本目录下的脚本、样式与清单文件，仅通过版本保留策略清理。",
		Selectable:  false,
	},
	CategoryBattleCJS: {
		Label:       "战斗动画与特效 (CJS)",
		Description: "非版本目录中的 img/sp/cjs 战斗帧动画与特效脚本，战斗中可能频繁使用。",
		Selectable:  true,
	},
	CategoryStory: {
		Label:       "剧情与支线素材",
		Description: "主线/活动剧情立绘与背景 (quest/scene)、支线剧情 (sidestory) 与小剧场漫画 (comic)。",
		Selectable:  true,
	},
	CategoryCoreUI: {
		Label:       "常驻 UI 与基础素材",
		Description: "主页、编成、共斗、转世、商店及活动公共框架 (event/common) 等高频界面素材。",
		Selectable:  true,
	},
	CategoryEventGacha: {
		Label:       "活动与宣发素材",
		Description: "特定活动专属目录 (排除 event/common)、卡池 (gacha)、横幅 (banner)、宣传 (campaign) 与登录奖励。",
		Selectable:  true,
	},
	CategoryAudio: {
		Label:       "音频素材",
		Description: "非版本目录中的语音、BGM 与音效文件 (sound/** 及 .mp3/.wav/.ogg/.m4a)。",
		Selectable:  true,
	},
	CategoryOther: {
		Label:       "其它未归类素材",
		Description: "未命中已知业务目录规则的受管理缓存文件及非 GBF 域名资源。",
		Selectable:  true,
	},
}

// SupportedVersionPrefixes lists the resource prefixes eligible for version directory grouping.
var SupportedVersionPrefixes = []string{"assets", "assets_en", "assets_jp"}

var confirmedQuestUIDirs = map[string]struct{}{
	"assets":     {},
	"free":       {},
	"assist":     {},
	"banner":     {},
	"fate":       {},
	"calamitous": {},
	"stage":      {},
	"select":     {},
	"pro_quest":  {},
	"search":     {},
	"supporter":  {},
	"extra":      {},
	"hl":         {},
	"main":       {},
	"tower":      {},
	"v2":         {},
}

var confirmedCoreUISpDirs = map[string]struct{}{
	"ui":          {},
	"assets":      {},
	"raid":        {},
	"top":         {},
	"css_img":     {},
	"shop":        {},
	"coopraid":    {},
	"party":       {},
	"enhancement": {},
	"archaic":     {},
	"frontier":    {},
	"guild":       {},
	"mypage":      {},
	"profile":     {},
	"arcarum":     {},
	"arcarum2":    {},
	"arcarum3":    {},
	"replicard":   {},
	"skycompass":  {},
	"list":        {},
	"treasure":    {},
	"gacha_cjs":   {},
	"title":       {},
	"tutorial":    {},
	"setting":     {},
	"present":     {},
	"supplies":    {},
	"job":         {},
	"zenith":      {},
	"bahamut":     {},
	"seraphic":    {},
	"awakening":   {},
	"artifact":    {},
	"wonder":      {},
	"skin":        {},
	"book":        {},
	"trophy":      {},
	"crew":        {},
	"island":      {},
	"resort":      {},
	"alchemy":     {},
	"cooking":     {},
}

var supportedAudioExts = map[string]struct{}{
	".mp3": {},
	".wav": {},
	".ogg": {},
	".m4a": {},
}

// Validation errors for breakdown/search/cleanup operations.
var (
	ErrSnapshotNotFound       = errors.New("未找到可用的缓存扫描快照，请先执行扫描")
	ErrSnapshotStale          = errors.New("缓存扫描快照已过期，请重新扫描后再执行操作")
	ErrEmptySearchQuery       = errors.New("搜索关键词不能为空")
	ErrSearchQueryTooLong     = errors.New("搜索关键词长度超出上限 (最多 512 字符)")
	ErrInvalidSearchLimit     = errors.New("搜索返回数量上限无效 (允许范围 1~200)")
	ErrVersionedCatDisallowed = errors.New("versioned 类别不支持按类别直接清空，请使用版本保留策略清理")
	ErrInvalidCategory        = errors.New("包含无效的缓存分类名称")
	ErrEmptyCleanupSelection  = errors.New("未选择需要清理的缓存分类或条目")
	ErrBatchLimitExceeded     = errors.New("单次定点删除条目数超出上限 (最多 200 项)")
	ErrInvalidKeepCount       = errors.New("保留版本数量参数无效 (必须为正整数)")
)

const (
	MaxSearchQueryLen      = 512
	DefaultSearchLimit     = 50
	MaxSearchLimit         = 200
	MaxInvalidateBatchSize = 200
	DefaultVersionKeep     = 8
)

// IsValidCategory reports whether cat is one of the 7 defined categories.
func IsValidCategory(cat string) bool {
	_, ok := categoryMeta[cat]
	return ok
}

// IsSelectableCleanupCategory reports whether cat can be cleared via ClearCategories.
func IsSelectableCleanupCategory(cat string) bool {
	meta, ok := categoryMeta[cat]
	return ok && meta.Selectable
}

func isASCIIControl(b byte) bool {
	return b < 0x20 || b == 0x7f
}

func isASCIISpaceOrControl(b byte) bool {
	return b <= ' ' || b == 0x7f
}

func trimLeadingASCIIControl(s string) string {
	start := 0
	for start < len(s) && isASCIIControl(s[start]) {
		start++
	}
	return s[start:]
}

func trimTrailingASCIISpaceOrControl(s string) string {
	end := len(s)
	for end > 0 && isASCIISpaceOrControl(s[end-1]) {
		end--
	}
	return s[:end]
}

func trimTrailingDotOrASCIISpaceOrControl(s string) string {
	end := len(s)
	for end > 0 && (s[end-1] == '.' || isASCIISpaceOrControl(s[end-1])) {
		end--
	}
	return s[:end]
}

func splitCleanSegments(cleanKey string) []string {
	rawKey, _, _ := strings.Cut(cleanKey, "?")
	if strings.IndexByte(rawKey, 0) >= 0 {
		return nil
	}
	leadTrimmed := rawKey
	for len(leadTrimmed) > 0 && isASCIISpaceOrControl(leadTrimmed[0]) {
		leadTrimmed = leadTrimmed[1:]
	}
	if strings.HasPrefix(leadTrimmed, `\\`) {
		return nil
	}
	trimmed := trimTrailingASCIISpaceOrControl(trimLeadingASCIIControl(rawKey))
	if trimmed == "" {
		return nil
	}
	norm := strings.ReplaceAll(trimmed, "\\", "/")
	raw := strings.Split(norm, "/")
	segs := make([]string, 0, len(raw))
	for _, s := range raw {
		s = trimLeadingASCIIControl(s)
		s = trimTrailingASCIISpaceOrControl(s)
		if s == "" || s == "." {
			continue
		}
		for i := 0; i < len(s); i++ {
			if isASCIIControl(s[i]) {
				return nil
			}
		}
		if s == ".." || strings.Contains(s, "..") || strings.Contains(s, ":") {
			return nil
		}
		s = trimTrailingDotOrASCIISpaceOrControl(s)
		if s == "" {
			return nil
		}
		segs = append(segs, s)
	}
	return segs
}

func parsePositiveVersionSegment(seg string) (int, bool) {
	if seg == "" || len(seg) > 18 {
		return 0, false
	}
	for i := 0; i < len(seg); i++ {
		if seg[i] < '0' || seg[i] > '9' {
			return 0, false
		}
	}
	if len(seg) > 1 && seg[0] == '0' {
		return 0, false
	}
	v, err := strconv.Atoi(seg)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// ParseVersionedEntry checks whether cleanKey is under a supported resource prefix
// (assets, assets_en, assets_jp) followed by a positive integer version directory.
func ParseVersionedEntry(ns, cleanKey string) (prefix string, version string, versionNum int, ok bool) {
	segs := splitCleanSegments(cleanKey)
	if len(segs) < 3 {
		return "", "", 0, false
	}
	p := segs[0]
	if p != "assets" && p != "assets_en" && p != "assets_jp" {
		return "", "", 0, false
	}
	vNum, validVer := parsePositiveVersionSegment(segs[1])
	if !validVer {
		return "", "", 0, false
	}
	return p, segs[1], vNum, true
}

// ClassifyCacheEntry assigns a cache data file to exactly one of the 7 categories.
// Scanning, search results, category cleanup candidate selection, and space estimation
// all share this function. It is strictly management-plane only and never used in
// data-plane static request routing.
func ClassifyCacheEntry(ns, cleanKey string) string {
	normNS := sanitizeNamespace(ns)
	segs := splitCleanSegments(cleanKey)
	if len(segs) == 0 {
		return CategoryOther
	}

	// Priority 1: versioned assets under supported resource prefixes
	if _, _, _, ok := ParseVersionedEntry(normNS, cleanKey); ok {
		return CategoryVersioned
	}

	// Non-GBF namespaces do not apply GBF business directory semantics
	if normNS != "gbf" {
		return CategoryOther
	}

	// Normalize resource prefix for GBF namespace while supporting GBF fallback paths
	var subSegs []string
	first := segs[0]
	if first == "assets" || first == "assets_en" || first == "assets_jp" {
		subSegs = segs[1:]
	} else if strings.HasPrefix(first, "assets") {
		// Unsupported assets_* prefix (e.g. assets_kr) falls into other
		return CategoryOther
	} else {
		// GBF fallback path without leading "assets/" prefix
		subSegs = segs
	}

	if len(subSegs) == 0 {
		return CategoryOther
	}

	// Priority 2: battle_cjs (non-versioned img/sp/cjs/**)
	if len(subSegs) >= 4 && subSegs[0] == "img" && subSegs[1] == "sp" && subSegs[2] == "cjs" {
		return CategoryBattleCJS
	}

	// Priority 3: story (img/sp/quest/scene/**, img/sp/sidestory/**, img/sp/comic/**)
	if len(subSegs) >= 5 && subSegs[0] == "img" && subSegs[1] == "sp" && subSegs[2] == "quest" && subSegs[3] == "scene" {
		return CategoryStory
	}
	if len(subSegs) >= 4 && subSegs[0] == "img" && subSegs[1] == "sp" && (subSegs[2] == "sidestory" || subSegs[2] == "comic") {
		return CategoryStory
	}

	// Priority 4: core_ui (event/common/**, confirmed quest UI subdirs, confirmed sp UI dirs, img/sprite/**)
	if len(subSegs) >= 5 && subSegs[0] == "img" && subSegs[1] == "sp" && subSegs[2] == "event" && subSegs[3] == "common" {
		return CategoryCoreUI
	}
	if len(subSegs) >= 5 && subSegs[0] == "img" && subSegs[1] == "sp" && subSegs[2] == "quest" {
		if _, ok := confirmedQuestUIDirs[subSegs[3]]; ok {
			return CategoryCoreUI
		}
	}
	if len(subSegs) >= 4 && subSegs[0] == "img" && subSegs[1] == "sp" {
		if _, ok := confirmedCoreUISpDirs[subSegs[2]]; ok {
			return CategoryCoreUI
		}
	}
	if len(subSegs) >= 3 && subSegs[0] == "img" && (subSegs[1] == "sprite" || subSegs[1] == "ui" || subSegs[1] == "common") {
		return CategoryCoreUI
	}

	// Priority 5: event_gacha (remaining img/sp/event/** after excluding event/common, plus gacha/banner/campaign/loginbonus)
	if len(subSegs) >= 4 && subSegs[0] == "img" && subSegs[1] == "sp" {
		switch subSegs[2] {
		case "event", "gacha", "banner", "campaign", "loginbonus":
			return CategoryEventGacha
		}
	}

	// Priority 6: audio (non-versioned sound/** or supported audio extensions)
	if len(subSegs) >= 2 && subSegs[0] == "sound" {
		return CategoryAudio
	}
	lastSeg := subSegs[len(subSegs)-1]
	ext := strings.ToLower(filepath.Ext(lastSeg))
	if _, ok := supportedAudioExts[ext]; ok {
		return CategoryAudio
	}

	// Priority 7: other
	return CategoryOther
}

// --- Per-Key Invalidation Token Table (Section 5.3) ---

const (
	tokenShardCount      = 16
	maxInvalidSeqEntries = 4096
)

type opToken struct {
	gen      uint64
	seq      uint64
	canonKey string
}

type tokenRingEntry struct {
	key string
	seq uint64
}

type tokenShard struct {
	mu          sync.Mutex
	seq         uint64
	minValidSeq uint64
	invalidSeq  map[string]uint64
	ring        [maxInvalidSeqEntries]tokenRingEntry
	ringPos     int
}

func isCleanRelativeSlashPath(s string) bool {
	if s == "" || s[0] == '/' || s[len(s)-1] == '/' {
		return false
	}
	segStart := 0
	for i := 0; i <= len(s); i++ {
		if i < len(s) {
			c := s[i]
			if isASCIIControl(c) || c == '\\' || c == ':' || c == '?' || (c == '.' && i > 0 && s[i-1] == '.') {
				return false
			}
			if c != '/' {
				continue
			}
		}
		segLen := i - segStart
		if segLen == 0 || s[i-1] == '.' || s[i-1] == ' ' {
			return false
		}
		segStart = i + 1
	}
	return true
}

// normalizeCleanKey canonicalizes a URL path or cache key into a clean relative
// forward-slash path with zero heap allocations on the common clean path.
// It preserves interior and leading filename spaces (e.g. "/assets/js/ app.js")
// and Unicode non-breaking spaces while stripping ASCII control chars and trailing dots/spaces.
func normalizeCleanKey(urlPath string) string {
	s, _, _ := strings.Cut(urlPath, "?")
	if s != "" && s[0] == '/' {
		s = s[1:]
	}
	if isCleanRelativeSlashPath(s) {
		return s
	}
	segs := splitCleanSegments(urlPath)
	if len(segs) == 0 {
		return ""
	}
	return strings.Join(segs, "/")
}

// canonicalizeNSAndKey normalizes a (namespace, urlPath) pair while keeping
// ("gbf", "hosts/<subNS>/<subKey>") distinct from external host ("<subNS>", "<subKey>")
// so that different upstream hosts never share RAM or disk cache entries.
func canonicalizeNSAndKey(ns, urlPath string) (string, string, bool) {
	cleanKey := normalizeCleanKey(urlPath)
	if cleanKey == "" {
		return "", "", false
	}
	rawNS := strings.TrimSpace(ns)
	normNS := "gbf"
	if rawNS != "" && !strings.EqualFold(rawNS, "gbf") {
		if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) {
			return "", "", false
		}
		normNS = sanitizeNamespace(rawNS)
	}
	if normNS == "" || normNS == "hosts" {
		return "", "", false
	}
	if normNS == "gbf" && (cleanKey == "hosts" || strings.HasPrefix(cleanKey, "hosts/")) {
		parts := strings.SplitN(cleanKey, "/", 3)
		if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
			return "", "", false
		}
		rawSubNS := parts[1]
		if strings.Contains(rawSubNS, "..") || strings.ContainsAny(rawSubNS, `/\:`) {
			return "", "", false
		}
		subNS := sanitizeNamespace(rawSubNS)
		if subNS == "" || subNS == "hosts" || subNS == "gbf" || subNS != strings.ToLower(rawSubNS) {
			return "", "", false
		}
		subKey := normalizeCleanKey(parts[2])
		if subKey == "" {
			return "", "", false
		}
		return "gbf", "hosts/" + subNS + "/" + subKey, true
	}
	return normNS, cleanKey, true
}

func canonicalTokenKey(ns, cleanKey string) string {
	normNS, normKey, ok := canonicalizeNSAndKey(ns, cleanKey)
	if !ok {
		normKey = normalizeCleanKey(cleanKey)
		if normKey == "" {
			return ""
		}
		normNS = "gbf"
		if ns != "" && ns != "gbf" {
			normNS = sanitizeNamespace(ns)
		}
	}
	if normNS == "gbf" {
		if strings.HasPrefix(normKey, "hosts/") {
			parts := strings.SplitN(normKey, "/", 3)
			if len(parts) == 3 && parts[1] != "" && parts[2] != "" {
				return sanitizeNamespace(parts[1]) + ":" + parts[2]
			}
		}
		if strings.HasPrefix(normKey, "assets/") {
			rem := normKey[len("assets/"):]
			if rem != "" && !strings.HasPrefix(rem, "assets") {
				normKey = rem
			}
		}
		if !strings.Contains(normKey, ":") {
			return normKey
		}
		return "gbf:" + normKey
	}
	return normNS + ":" + normKey
}

func gbfFallbackCleanKey(cleanKey string) (string, bool) {
	normKey := normalizeCleanKey(cleanKey)
	if normKey == "" || normKey == "hosts" || strings.HasPrefix(normKey, "hosts/") {
		return "", false
	}
	if !strings.HasPrefix(normKey, "assets") {
		return "assets/" + normKey, true
	}
	if strings.HasPrefix(normKey, "assets/") {
		rem := normKey[len("assets/"):]
		if rem != "" && !strings.HasPrefix(rem, "assets") && rem != "hosts" && !strings.HasPrefix(rem, "hosts/") {
			return rem, true
		}
	}
	return "", false
}

func (m *Manager) initTokenShards() {
	for i := range m.tokenShards {
		m.tokenShards[i].seq = 1
		m.tokenShards[i].minValidSeq = 1
		m.tokenShards[i].invalidSeq = make(map[string]uint64)
	}
}

func (m *Manager) clearTokenShards() {
	for i := range m.tokenShards {
		shard := &m.tokenShards[i]
		shard.mu.Lock()
		shard.seq++
		shard.minValidSeq = shard.seq
		shard.invalidSeq = make(map[string]uint64)
		shard.ring = [maxInvalidSeqEntries]tokenRingEntry{}
		shard.ringPos = 0
		shard.mu.Unlock()
	}
}

func (m *Manager) tokenShardByCanon(canonKey string) *tokenShard {
	return &m.tokenShards[int(fnv32(canonKey)&(tokenShardCount-1))]
}

func (m *Manager) tokenShardFor(ns, cleanKey string) (*tokenShard, string) {
	canonKey := canonicalTokenKey(ns, cleanKey)
	return m.tokenShardByCanon(canonKey), canonKey
}

func (m *Manager) currentGeneration() uint64 {
	m.persistMu.RLock()
	gen := m.generation
	m.persistMu.RUnlock()
	return gen
}

func (m *Manager) currentGenAndBase() (uint64, string) {
	m.persistMu.RLock()
	gen := m.generation
	base := m.GetCacheBase()
	m.persistMu.RUnlock()
	return gen, base
}

func (m *Manager) acquireOpTokenWithGen(ns, cleanKey string, gen uint64) opToken {
	shard, canonKey := m.tokenShardFor(ns, cleanKey)
	shard.mu.Lock()
	seq := shard.seq
	shard.mu.Unlock()
	return opToken{
		gen:      gen,
		seq:      seq,
		canonKey: canonKey,
	}
}

func (m *Manager) acquireOpToken(ns, cleanKey string) opToken {
	m.persistMu.RLock()
	gen := m.generation
	tok := m.acquireOpTokenWithGen(ns, cleanKey, gen)
	m.persistMu.RUnlock()
	return tok
}

func isTokenValidLocked(shard *tokenShard, tok opToken, currentGen uint64) bool {
	if tok.gen == 0 || tok.gen != currentGen {
		return false
	}
	if tok.seq < shard.minValidSeq {
		return false
	}
	if invSeq, ok := shard.invalidSeq[tok.canonKey]; ok && tok.seq < invSeq {
		return false
	}
	return true
}

func (m *Manager) isOpTokenValid(tok opToken) bool {
	if tok.gen == 0 || tok.canonKey == "" {
		return false
	}
	m.persistMu.RLock()
	gen := m.generation
	if tok.gen != gen {
		m.persistMu.RUnlock()
		return false
	}
	shard := m.tokenShardByCanon(tok.canonKey)
	shard.mu.Lock()
	valid := isTokenValidLocked(shard, tok, gen)
	shard.mu.Unlock()
	m.persistMu.RUnlock()
	return valid
}

func bumpInvalidateTokenLocked(shard *tokenShard, canonKey string) uint64 {
	shard.seq++
	newSeq := shard.seq
	old := shard.ring[shard.ringPos]
	if old.seq > 0 {
		if cur, ok := shard.invalidSeq[old.key]; ok && cur == old.seq {
			delete(shard.invalidSeq, old.key)
		}
		if old.seq > shard.minValidSeq {
			shard.minValidSeq = old.seq
		}
	}
	shard.ring[shard.ringPos] = tokenRingEntry{
		key: canonKey,
		seq: newSeq,
	}
	shard.ringPos = (shard.ringPos + 1) % maxInvalidSeqEntries
	shard.invalidSeq[canonKey] = newSeq
	return newSeq
}

func (m *Manager) clearSingleRAMKeyState(ramKey string) {
	if ramKey == "" {
		return
	}
	m.deleteDiskMeta(ramKey)
	if m.residentPool != nil {
		m.residentPool.Delete(ramKey)
	}
	if m.ramCache != nil {
		m.ramCache.Delete(ramKey)
	}
	m.deleteMissingKey(ramKey)
}

func (m *Manager) clearKeyMemoryState(ns, cleanKey string) {
	normNS, normKey, ok := canonicalizeNSAndKey(ns, cleanKey)
	if !ok {
		normNS = sanitizeNamespace(ns)
		normKey = normalizeCleanKey(cleanKey)
	}
	m.clearSingleRAMKeyState(makeRAMKey(normNS, normKey))
	if normNS != "" && normNS != "gbf" {
		m.clearSingleRAMKeyState(normNS + "/" + normKey)
		m.clearSingleRAMKeyState(makeRAMKey("gbf", "hosts/"+normNS+"/"+normKey))
	}

	if normNS == "gbf" {
		if strings.HasPrefix(normKey, "hosts/") {
			parts := strings.SplitN(normKey, "/", 3)
			if len(parts) == 3 && parts[1] != "" && parts[2] != "" {
				subNS := sanitizeNamespace(parts[1])
				subKey := parts[2]
				m.clearSingleRAMKeyState(makeRAMKey(subNS, subKey))
				m.clearSingleRAMKeyState(subNS + "/" + subKey)
			}
		}
		if altKey, ok := gbfFallbackCleanKey(normKey); ok {
			m.clearSingleRAMKeyState(makeRAMKey("gbf", altKey))
		}
	}
}

func (m *Manager) deleteMissingKey(ramKey string) {
	shard := m.missingShard(ramKey)
	shard.mu.Lock()
	delete(shard.items, ramKey)
	shard.mu.Unlock()
}

func (m *Manager) registerTempFileToken(tmpPath string, tok opToken) bool {
	m.persistMu.RLock()
	defer m.persistMu.RUnlock()
	return m.registerTempFileTokenLocked(tmpPath, tok)
}

func (m *Manager) registerTempFileTokenLocked(tmpPath string, tok opToken) bool {
	curGen := m.generation
	if tok.gen == 0 || tok.gen != curGen {
		return false
	}
	m.tempTokensMu.Lock()
	defer m.tempTokensMu.Unlock()
	if m.tempTokens == nil {
		m.tempTokens = make(map[string]opToken)
	}
	if len(m.tempTokens) >= 1024 {
		// First sweep entries whose generation doesn't match the current generation or whose temp file was already removed
		for k, v := range m.tempTokens {
			if v.gen != curGen {
				delete(m.tempTokens, k)
				continue
			}
			if _, err := os.Stat(k); os.IsNotExist(err) {
				delete(m.tempTokens, k)
			}
		}
	}
	if len(m.tempTokens) >= 1024 {
		// Evict excess entries; CommitDiskTempFile fails closed when a token is missing (!hasTok)
		count := 0
		for k := range m.tempTokens {
			delete(m.tempTokens, k)
			count++
			if count >= 256 {
				break
			}
		}
	}
	m.tempTokens[tmpPath] = tok
	return true
}

func (m *Manager) popTempFileToken(tmpPath string) (opToken, bool) {
	m.tempTokensMu.Lock()
	if m.tempTokens == nil {
		m.tempTokensMu.Unlock()
		return opToken{}, false
	}
	tok, ok := m.tempTokens[tmpPath]
	if ok {
		delete(m.tempTokens, tmpPath)
	}
	m.tempTokensMu.Unlock()
	return tok, ok
}

// DiscardDiskTempFile removes a temporary download file's token registration
// and deletes the temporary file from disk if present.
func (m *Manager) DiscardDiskTempFile(tmpPath string) {
	if tmpPath == "" {
		return
	}
	m.popTempFileToken(tmpPath)
	_ = os.Remove(tmpPath)
}

func (m *Manager) clearTempTokens() {
	m.tempTokensMu.Lock()
	m.tempTokens = make(map[string]opToken)
	m.tempTokensMu.Unlock()
}

// --- Symlink / Junction Containment Verification (Section 5.4) ---

// testBeforeUnlinkHook is an internal test-only hook invoked after all path and identity checks
// have completed and immediately before the handle-relative parentRoot.Remove(leafName) call.
var testBeforeUnlinkHook func(targetPath string)

func resolveContainedRealBaseWithInfo(baseDir string) (absBase string, realBase string, rootInfo os.FileInfo, exists bool, err error) {
	absBase, err = filepath.Abs(baseDir)
	if err != nil {
		return "", "", nil, false, err
	}
	lfi, lstatErr := os.Lstat(absBase)
	if lstatErr != nil {
		if os.IsNotExist(lstatErr) {
			return absBase, absBase, nil, false, nil
		}
		return absBase, "", nil, false, lstatErr
	}
	if lfi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return absBase, "", nil, true, fmt.Errorf("cache base cannot be a symlink or reparse point: %s", absBase)
	}
	if !lfi.IsDir() {
		return absBase, "", nil, true, fmt.Errorf("cache base is not a directory: %s", absBase)
	}
	realBase, err = filepath.EvalSymlinks(absBase)
	if err != nil {
		return absBase, "", nil, true, err
	}
	realFi, statErr := os.Stat(realBase)
	if statErr != nil {
		return absBase, "", nil, true, statErr
	}
	if !os.SameFile(lfi, realFi) {
		return absBase, "", nil, true, fmt.Errorf("cache base resolved identity mismatch: %s", absBase)
	}
	return absBase, realBase, realFi, true, nil
}

func resolveContainedRealBase(baseDir string) (absBase string, realBase string, exists bool, err error) {
	absBase, realBase, _, exists, err = resolveContainedRealBaseWithInfo(baseDir)
	return absBase, realBase, exists, err
}

func verifyStrictRootIdentity(absBase, expectedRealBase string, expectedRootFi os.FileInfo) error {
	curAbs, curReal, curRootFi, exists, err := resolveContainedRealBaseWithInfo(absBase)
	if err != nil {
		return err
	}
	if !exists {
		return os.ErrNotExist
	}
	if curAbs != absBase {
		return fmt.Errorf("cache base path mismatch: %s vs %s", curAbs, absBase)
	}
	if expectedRealBase != "" && curReal != expectedRealBase {
		return fmt.Errorf("cache base real path changed: %s vs %s", curReal, expectedRealBase)
	}
	if expectedRootFi != nil && !os.SameFile(expectedRootFi, curRootFi) {
		return fmt.Errorf("cache root directory identity changed: %s", absBase)
	}
	return nil
}

func isRealPathWithinBase(realBase, candidateRealPath string) bool {
	rel, err := filepath.Rel(realBase, candidateRealPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return false
	}
	return true
}

// verifyStrictPathChain verifies that absBase has not been replaced or redirected by a symlink/junction,
// and walks every path component from absBase to targetPath using os.Lstat so that no intermediate
// directory or leaf entry is a symlink or reparse point.
func verifyStrictPathChain(absBase, realBase string, expectedRootFi os.FileInfo, targetPath string) error {
	if err := verifyStrictRootIdentity(absBase, realBase, expectedRootFi); err != nil {
		return err
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	lexRel, err := filepath.Rel(absBase, absTarget)
	if err != nil || lexRel == "." || lexRel == ".." || strings.HasPrefix(lexRel, ".."+string(filepath.Separator)) || filepath.IsAbs(lexRel) {
		return fmt.Errorf("path escapes cache base: %s", targetPath)
	}

	parts := strings.Split(lexRel, string(filepath.Separator))
	cur := absBase
	var leafFi os.FileInfo
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid path component in %s", targetPath)
		}
		cur = filepath.Join(cur, part)
		fi, lErr := os.Lstat(cur)
		if lErr != nil {
			if os.IsNotExist(lErr) {
				// All existing ancestor segments up to this missing component have been verified by Lstat
				return nil
			}
			return lErr
		}
		if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return fmt.Errorf("refusing symlink or reparse component in path chain: %s", cur)
		}
		if i < len(parts)-1 && !fi.IsDir() {
			return fmt.Errorf("ancestor in path chain is not a directory: %s", cur)
		}
		if i == len(parts)-1 {
			leafFi = fi
		}
	}

	if leafFi != nil && realBase != "" {
		realTarget, evalErr := filepath.EvalSymlinks(absTarget)
		if evalErr != nil {
			return evalErr
		}
		if !isRealPathWithinBase(realBase, realTarget) {
			return fmt.Errorf("resolved path escapes cache base: %s", targetPath)
		}
		if realTarget == realBase {
			return fmt.Errorf("resolved path equals cache root: %s", targetPath)
		}
	}
	return nil
}

func verifyStrictDirChain(absBase, realBase string, expectedRootFi os.FileInfo, dirPath string) error {
	return verifyStrictDirChainWithDirInfos(absBase, realBase, expectedRootFi, nil, dirPath)
}

func verifyStrictDirChainWithDirInfos(absBase, realBase string, expectedRootFi os.FileInfo, dirInfos map[string]os.FileInfo, dirPath string) error {
	if err := verifyStrictPathChain(absBase, realBase, expectedRootFi, dirPath); err != nil {
		return err
	}
	fi, err := os.Lstat(dirPath)
	if err != nil {
		return err
	}
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !fi.IsDir() {
		return fmt.Errorf("target is not a regular directory: %s", dirPath)
	}
	if dirInfos != nil {
		if expBaseFi, ok := dirInfos[absBase]; ok && expBaseFi != nil {
			if curBaseFi, bErr := os.Lstat(absBase); bErr != nil || !os.SameFile(expBaseFi, curBaseFi) {
				return fmt.Errorf("cache root directory identity changed since scan: %s", absBase)
			}
		}
		if rel, rErr := filepath.Rel(absBase, dirPath); rErr == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			cur := absBase
			for _, seg := range strings.Split(rel, string(filepath.Separator)) {
				if seg == "" || seg == "." {
					continue
				}
				cur = filepath.Join(cur, seg)
				if expFi, ok := dirInfos[cur]; ok && expFi != nil {
					curFi, cErr := os.Lstat(cur)
					if cErr != nil {
						return cErr
					}
					if !os.SameFile(expFi, curFi) {
						return fmt.Errorf("directory physical identity changed since discovery: %s", cur)
					}
				}
			}
		}
	}
	return nil
}

func verifyTargetContainment(absBase, realBase, targetPath string) error {
	return verifyStrictPathChain(absBase, realBase, nil, targetPath)
}

// openPinnedParentRoot opens and verifies each directory component from absBase down to the immediate
// parent directory of targetPath using kernel directory handles (os.Root / openat O_NOFOLLOW|O_DIRECTORY).
// It verifies os.SameFile against expectedRootFi and any recorded snapshot/walk directory FileInfo at every step,
// returning a pinned *os.Root for the parent directory and the single-segment leaf name so subsequent
// Lstat and Remove calls execute via fstatat/unlinkat on the pinned parent handle without re-resolving path strings.
func openPinnedParentRoot(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	dirInfos map[string]os.FileInfo,
	targetPath string,
) (*os.Root, string, func(), error) {
	if err := verifyStrictRootIdentity(absBase, realBase, expectedRootFi); err != nil {
		return nil, "", nil, err
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return nil, "", nil, err
	}
	lexRel, err := filepath.Rel(absBase, absTarget)
	if err != nil || lexRel == "." || lexRel == ".." || strings.HasPrefix(lexRel, ".."+string(filepath.Separator)) || filepath.IsAbs(lexRel) {
		return nil, "", nil, fmt.Errorf("path escapes cache base: %s", targetPath)
	}
	parts := strings.Split(lexRel, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, "", nil, fmt.Errorf("invalid path component in %s", targetPath)
		}
	}
	leafName := parts[len(parts)-1]
	dirSegs := parts[:len(parts)-1]

	curRoot, err := os.OpenRoot(absBase)
	if err != nil {
		return nil, "", nil, err
	}
	rootDotFi, err := curRoot.Lstat(".")
	if err != nil {
		_ = curRoot.Close()
		return nil, "", nil, err
	}
	if rootDotFi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !rootDotFi.IsDir() {
		_ = curRoot.Close()
		return nil, "", nil, fmt.Errorf("cache base is not a regular directory: %s", absBase)
	}
	if expectedRootFi != nil && !os.SameFile(expectedRootFi, rootDotFi) {
		_ = curRoot.Close()
		return nil, "", nil, fmt.Errorf("cache root directory identity changed: %s", absBase)
	}
	if dirInfos != nil {
		if expBaseFi, ok := dirInfos[absBase]; ok && expBaseFi != nil && !os.SameFile(expBaseFi, rootDotFi) {
			_ = curRoot.Close()
			return nil, "", nil, fmt.Errorf("cache root directory identity changed since scan: %s", absBase)
		}
	}

	curPath := absBase
	for _, seg := range dirSegs {
		curPath = filepath.Join(curPath, seg)
		segLfi, lErr := curRoot.Lstat(seg)
		if lErr != nil {
			_ = curRoot.Close()
			return nil, "", nil, lErr
		}
		if segLfi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			_ = curRoot.Close()
			return nil, "", nil, fmt.Errorf("refusing symlink or reparse component in path chain: %s", curPath)
		}
		if !segLfi.IsDir() {
			_ = curRoot.Close()
			return nil, "", nil, fmt.Errorf("ancestor in path chain is not a directory: %s", curPath)
		}
		nextRoot, oErr := curRoot.OpenRoot(seg)
		if oErr != nil {
			_ = curRoot.Close()
			return nil, "", nil, oErr
		}
		nextDotFi, dErr := nextRoot.Lstat(".")
		if dErr != nil {
			_ = nextRoot.Close()
			_ = curRoot.Close()
			return nil, "", nil, dErr
		}
		if !os.SameFile(segLfi, nextDotFi) {
			_ = nextRoot.Close()
			_ = curRoot.Close()
			return nil, "", nil, fmt.Errorf("ancestor directory identity changed during traversal: %s", curPath)
		}
		if dirInfos != nil {
			if expDirFi, ok := dirInfos[curPath]; ok && expDirFi != nil && !os.SameFile(expDirFi, nextDotFi) {
				_ = nextRoot.Close()
				_ = curRoot.Close()
				return nil, "", nil, fmt.Errorf("ancestor directory identity changed since scan: %s", curPath)
			}
		}
		_ = curRoot.Close()
		curRoot = nextRoot
	}

	closeFn := func() {
		_ = curRoot.Close()
	}
	return curRoot, leafName, closeFn, nil
}

// removeVerifiedEmptyDir removes targetDir only if it is a regular empty directory whose entire ancestor
// chain from absBase matches expectedRootFi and dirInfos, using parentRoot.Remove(dirLeaf) on the pinned parent handle.
func removeVerifiedEmptyDir(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	dirInfos map[string]os.FileInfo,
	targetDir string,
) (removed bool, nonEmpty bool, err error) {
	parentRoot, dirLeaf, closeParent, chainErr := openPinnedParentRoot(absBase, realBase, expectedRootFi, dirInfos, targetDir)
	if chainErr != nil {
		if os.IsNotExist(chainErr) {
			return false, false, nil
		}
		return false, false, chainErr
	}
	defer closeParent()

	dLfi, lErr := parentRoot.Lstat(dirLeaf)
	if lErr != nil {
		if os.IsNotExist(lErr) {
			return false, false, nil
		}
		return false, false, lErr
	}
	if dLfi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !dLfi.IsDir() {
		return false, false, fmt.Errorf("target is not a regular directory: %s", targetDir)
	}
	if dirInfos != nil {
		if expFi, ok := dirInfos[targetDir]; ok && expFi != nil && !os.SameFile(expFi, dLfi) {
			return false, false, fmt.Errorf("directory identity changed since scan: %s", targetDir)
		}
	}

	childRoot, oErr := parentRoot.OpenRoot(dirLeaf)
	if oErr != nil {
		if os.IsNotExist(oErr) {
			return false, false, nil
		}
		return false, false, oErr
	}
	childDotFi, dotErr := childRoot.Lstat(".")
	if dotErr != nil {
		_ = childRoot.Close()
		return false, false, dotErr
	}
	if !os.SameFile(dLfi, childDotFi) {
		_ = childRoot.Close()
		return false, false, fmt.Errorf("directory identity changed before removal: %s", targetDir)
	}
	dirFile, openErr := childRoot.Open(".")
	if openErr != nil {
		_ = childRoot.Close()
		return false, false, openErr
	}
	entries, readErr := dirFile.ReadDir(1)
	_ = dirFile.Close()
	_ = childRoot.Close()
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) && readErr.Error() != "EOF" {
		return false, false, readErr
	}
	if len(entries) > 0 {
		return false, true, nil
	}

	if testBeforeUnlinkHook != nil {
		testBeforeUnlinkHook(targetDir)
	}

	if rmErr := parentRoot.Remove(dirLeaf); rmErr != nil {
		if os.IsNotExist(rmErr) {
			return false, false, nil
		}
		// Check whether a concurrent file creation made the directory non-empty
		if cRoot, cErr := parentRoot.OpenRoot(dirLeaf); cErr == nil {
			if df, dfErr := cRoot.Open("."); dfErr == nil {
				ents, _ := df.ReadDir(1)
				_ = df.Close()
				_ = cRoot.Close()
				if len(ents) > 0 {
					return false, true, nil
				}
			} else {
				_ = cRoot.Close()
			}
		}
		return false, false, rmErr
	}
	return true, false, nil
}

func validateManagedNamespaceAndKey(ns, cleanKey string) (string, string, bool) {
	rawNS := strings.TrimSpace(ns)
	if rawNS == "" {
		rawNS = "gbf"
	}
	if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) || strings.EqualFold(rawNS, "hosts") {
		return "", "", false
	}
	normNS := sanitizeNamespace(rawNS)
	if normNS != strings.ToLower(rawNS) {
		return "", "", false
	}

	if cleanKey == "" || strings.Contains(cleanKey, "?") {
		return "", "", false
	}
	trimmed := trimTrailingASCIISpaceOrControl(trimLeadingASCIIControl(cleanKey))
	if trimmed == "" {
		return "", "", false
	}
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) || (len(trimmed) >= 2 && trimmed[1] == ':') {
		return "", "", false
	}
	segs := splitCleanSegments(trimmed)
	if len(segs) == 0 {
		return "", "", false
	}
	if normNS == "gbf" && segs[0] == "hosts" {
		return "", "", false
	}
	normKey := strings.Join(segs, "/")
	if strings.HasSuffix(normKey, ".ext") || strings.Contains(normKey, ".tmp.") || strings.Contains(normKey, ".quarantine") {
		return "", "", false
	}
	return normNS, normKey, true
}

// --- Breakdown Snapshot & Statistics Models (Sections 3.2, 4.2, 6) ---

// CategoryStat represents file and byte statistics for one category.
type CategoryStat struct {
	Category             string  `json:"category"`
	Label                string  `json:"label"`
	Description          string  `json:"description"`
	Selectable           bool    `json:"selectable"`
	AssetFiles           int     `json:"asset_files"`
	AssetBytes           int64   `json:"asset_bytes"`
	MetadataFiles        int     `json:"metadata_files"`
	MetadataBytes        int64   `json:"metadata_bytes"`
	EstimatedDeleteBytes int64   `json:"estimated_delete_bytes"`
	Percentage           float64 `json:"percentage"`
}

// NamespaceStat represents file and byte statistics for one namespace.
type NamespaceStat struct {
	Namespace            string                  `json:"namespace"`
	AssetFiles           int                     `json:"asset_files"`
	AssetBytes           int64                   `json:"asset_bytes"`
	MetadataFiles        int                     `json:"metadata_files"`
	MetadataBytes        int64                   `json:"metadata_bytes"`
	EstimatedDeleteBytes int64                   `json:"estimated_delete_bytes"`
	Categories           map[string]CategoryStat `json:"categories,omitempty"`
}

// VersionDirStat represents one numeric version directory under a namespace and resource prefix.
type VersionDirStat struct {
	Version              string `json:"version"`
	VersionNum           int    `json:"version_num"`
	RelativeDir          string `json:"relative_dir"`
	AssetFiles           int    `json:"asset_files"`
	AssetBytes           int64  `json:"asset_bytes"`
	MetadataFiles        int    `json:"metadata_files"`
	MetadataBytes        int64  `json:"metadata_bytes"`
	EstimatedDeleteBytes int64  `json:"estimated_delete_bytes"`
}

// VersionGroupStat represents all numeric version directories for a single (namespace, resource_prefix) pair.
type VersionGroupStat struct {
	Namespace            string           `json:"namespace"`
	ResourcePrefix       string           `json:"resource_prefix"`
	GroupKey             string           `json:"group_key"`
	TotalDirs            int              `json:"total_dirs"`
	AssetFiles           int              `json:"asset_files"`
	AssetBytes           int64            `json:"asset_bytes"`
	MetadataFiles        int              `json:"metadata_files"`
	MetadataBytes        int64            `json:"metadata_bytes"`
	EstimatedDeleteBytes int64            `json:"estimated_delete_bytes"`
	Versions             []VersionDirStat `json:"versions"`
}

// VersionGroupRetentionPreview shows what will be kept and pruned for one (namespace, resource_prefix) group.
type VersionGroupRetentionPreview struct {
	Namespace            string   `json:"namespace"`
	ResourcePrefix       string   `json:"resource_prefix"`
	GroupKey             string   `json:"group_key"`
	TotalDirs            int      `json:"total_dirs"`
	KeptVersions         []string `json:"kept_versions"`
	PruneVersions        []string `json:"prune_versions"`
	PruneDirs            int      `json:"prune_dirs"`
	PruneAssetFiles      int      `json:"prune_asset_files"`
	PruneAssetBytes      int64    `json:"prune_asset_bytes"`
	PruneMetadataFiles   int      `json:"prune_metadata_files"`
	PruneMetadataBytes   int64    `json:"prune_metadata_bytes"`
	EstimatedDeleteBytes int64    `json:"estimated_delete_bytes"`
}

// VersionRetentionPreview summarizes the expected cleanup scope when keeping newest K versions per group.
type VersionRetentionPreview struct {
	Keep                 int                            `json:"keep"`
	TotalGroups          int                            `json:"total_groups"`
	TotalVersionDirs     int                            `json:"total_version_dirs"`
	PruneDirs            int                            `json:"prune_dirs"`
	PruneAssetFiles      int                            `json:"prune_asset_files"`
	PruneAssetBytes      int64                          `json:"prune_asset_bytes"`
	PruneMetadataFiles   int                            `json:"prune_metadata_files"`
	PruneMetadataBytes   int64                          `json:"prune_metadata_bytes"`
	EstimatedDeleteBytes int64                          `json:"estimated_delete_bytes"`
	Groups               []VersionGroupRetentionPreview `json:"groups"`
}

// SnapshotEntry represents an indexed managed data file in a breakdown snapshot.
type SnapshotEntry struct {
	NS           string      `json:"ns"`
	Key          string      `json:"key"`
	Category     string      `json:"category"`
	Size         int64       `json:"asset_bytes"`
	MetaBytes    int64       `json:"metadata_bytes"`
	HasMeta      bool        `json:"has_metadata"`
	MTimeNano    int64       `json:"mtime_nano"`
	MTimeMS      int64       `json:"mtime_ms"`
	fileInfo     os.FileInfo
	extMTimeNano int64
	extFileInfo  os.FileInfo
}

type orphanExtCandidate struct {
	ns        string
	cleanKey  string
	slashRel  string
	fullPath  string
	size      int64
	mtimeNano int64
	fileInfo  os.FileInfo
}

// BreakdownSnapshot holds the full result of a background cache breakdown scan.
type BreakdownSnapshot struct {
	SnapshotID           string                             `json:"snapshot_id"`
	Generation           uint64                             `json:"generation"`
	CacheBase            string                             `json:"cache_base"`
	Status               string                             `json:"status"`
	Valid                bool                               `json:"valid"`
	Stale                bool                               `json:"stale"`
	StaleReason          string                             `json:"stale_reason,omitempty"`
	StartedAtMS          int64                              `json:"started_at_ms"`
	CompletedAtMS        int64                              `json:"completed_at_ms"`
	DurationMS           int64                              `json:"duration_ms"`
	AssetFiles           int                                `json:"asset_files"`
	AssetBytes           int64                              `json:"asset_bytes"`
	MetadataFiles        int                                `json:"metadata_files"`
	MetadataBytes        int64                              `json:"metadata_bytes"`
	OrphanMetadataFiles  int                                `json:"orphan_metadata_files"`
	OrphanMetadataBytes  int64                              `json:"orphan_metadata_bytes"`
	ExcludedFiles        int                                `json:"excluded_files"`
	ExcludedBytes        int64                              `json:"excluded_bytes"`
	SkippedCount         int                                `json:"skipped_count"`
	ReadErrors           int                                `json:"read_errors"`
	Categories           []CategoryStat                     `json:"categories"`
	Namespaces           []NamespaceStat                    `json:"namespaces"`
	VersionGroups        []VersionGroupStat                 `json:"version_groups"`
	RetentionPreviews    map[string]VersionRetentionPreview `json:"retention_previews,omitempty"`
	ErrorMessage         string                             `json:"error,omitempty"`
	Entries              []SnapshotEntry                    `json:"-"`
	entryByNSKey         map[string]int
	orphanExts           map[string]orphanExtCandidate
	dirInfos             map[string]os.FileInfo
	absBase              string
	realBase             string
	rootInfo             os.FileInfo
	baseExists           bool
}

// ScanProgress reports background breakdown scan progress.
type ScanProgress struct {
	SnapshotID   string `json:"snapshot_id"`
	Phase        string `json:"phase"`
	ScannedFiles int    `json:"scanned_files"`
	ScannedBytes int64  `json:"scanned_bytes"`
	SkippedCount int    `json:"skipped_count"`
	ReadErrors   int    `json:"read_errors"`
	Done         bool   `json:"done"`
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
}

// SearchItem represents a single matched cache entry returned by SearchSnapshot.
type SearchItem struct {
	NS                   string `json:"ns"`
	Key                  string `json:"key"`
	Category             string `json:"category"`
	CategoryLabel        string `json:"category_label"`
	AssetBytes           int64  `json:"asset_bytes"`
	MetadataBytes        int64  `json:"metadata_bytes"`
	EstimatedDeleteBytes int64  `json:"estimated_delete_bytes"`
	HasMetadata          bool   `json:"has_metadata"`
	FileModifiedMS       int64  `json:"file_modified_ms"`
	FileModifiedISO      string `json:"file_modified_iso"`
	HasAlias             bool   `json:"has_alias,omitempty"`
	AliasKey             string `json:"alias_key,omitempty"`
}

// SearchResponse represents the response of GET /api/cache/search.
type SearchResponse struct {
	OK            bool         `json:"ok"`
	SnapshotID    string       `json:"snapshot_id"`
	CompletedAtMS int64        `json:"completed_at_ms"`
	Query         string       `json:"query"`
	Limit         int          `json:"limit"`
	TotalMatches  int          `json:"total_matches"`
	Truncated     bool         `json:"truncated"`
	Items         []SearchItem `json:"items"`
}

// DeleteFailure records why a specific entry failed to delete.
type DeleteFailure struct {
	NS     string `json:"ns"`
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// DeleteResult records detailed execution outcomes of targeted deletion, category cleanup, or version pruning.
type DeleteResult struct {
	TaskType            string          `json:"task_type"`
	SnapshotID          string          `json:"snapshot_id,omitempty"`
	CandidateFiles      int             `json:"candidate_files"`
	DeletedAssetFiles   int             `json:"deleted_asset_files"`
	DeletedAssetBytes   int64           `json:"deleted_asset_bytes"`
	DeletedMetaFiles    int             `json:"deleted_meta_files"`
	DeletedMetaBytes    int64           `json:"deleted_meta_bytes"`
	DeletedFiles        int             `json:"deleted_files"`
	FreedBytes          int64           `json:"freed_bytes"`
	FreedMB             float64         `json:"freed_mb"`
	DeletedDirs         int             `json:"deleted_dirs"`
	RemainingDirs       int             `json:"remaining_dirs"`
	AlreadyMissingCount int             `json:"already_missing_count"`
	SkippedChangedCount int             `json:"skipped_changed_count"`
	FailedCount         int             `json:"failed_count"`
	Failures            []DeleteFailure `json:"failures,omitempty"`
	Cancelled           bool            `json:"cancelled"`
	Done                bool            `json:"done"`
	Status              string          `json:"status"`
	Message             string          `json:"message,omitempty"`
}

// CleanupProgress reports progress during targeted deletion or category cleanup.
type CleanupProgress struct {
	TaskType            string `json:"task_type"`
	SnapshotID          string `json:"snapshot_id,omitempty"`
	ProcessedFiles      int    `json:"processed_files"`
	TotalFiles          int    `json:"total_files"`
	DeletedAssetFiles   int    `json:"deleted_asset_files"`
	DeletedAssetBytes   int64  `json:"deleted_asset_bytes"`
	DeletedMetaFiles    int    `json:"deleted_meta_files"`
	DeletedMetaBytes    int64  `json:"deleted_meta_bytes"`
	DeletedFiles        int    `json:"deleted_files"`
	FreedBytes          int64  `json:"freed_bytes"`
	AlreadyMissingCount int    `json:"already_missing_count"`
	SkippedChangedCount int    `json:"skipped_changed_count"`
	FailedCount         int    `json:"failed_count"`
	Cancelled           bool   `json:"cancelled"`
	Done                bool   `json:"done"`
}

func makeNSKey(ns, cleanKey string) string {
	return ns + "\x00" + cleanKey
}

func extractStrictNamespaceAndKey(base, path string) (ns string, cleanKey string, slashRel string, ok bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", "", "", false
	}
	slashRel = filepath.ToSlash(rel)
	if slashRel == "" || slashRel == "." || strings.HasPrefix(slashRel, "..") {
		return "", "", "", false
	}
	ns = "gbf"
	cleanKey = slashRel
	if slashRel == "hosts" {
		return "", "", "", false
	}
	if strings.HasPrefix(slashRel, "hosts/") {
		parts := strings.SplitN(slashRel, "/", 3)
		if len(parts) < 3 || parts[1] == "" || parts[2] == "" {
			return "", "", "", false
		}
		if strings.EqualFold(parts[1], "gbf") || strings.EqualFold(parts[1], "hosts") {
			return "", "", "", false
		}
		ns = parts[1]
		cleanKey = parts[2]
	}
	normNS, normKey, valid := validateManagedNamespaceAndKey(ns, strings.TrimSuffix(cleanKey, ".ext"))
	if !valid {
		return "", "", "", false
	}
	if strings.HasSuffix(cleanKey, ".ext") {
		return normNS, normKey + ".ext", slashRel, true
	}
	return normNS, normKey, slashRel, true
}

func buildEmptyCategoryStats() ([]CategoryStat, map[string]*CategoryStat) {
	stats := make([]CategoryStat, len(AllCategories))
	byKey := make(map[string]*CategoryStat, len(AllCategories))
	for i, cat := range AllCategories {
		meta := categoryMeta[cat]
		stats[i] = CategoryStat{
			Category:    cat,
			Label:       meta.Label,
			Description: meta.Description,
			Selectable:  meta.Selectable,
		}
		byKey[cat] = &stats[i]
	}
	return stats, byKey
}

// ComputeVersionRetentionPreview calculates the version pruning preview for a given keep count K.
func ComputeVersionRetentionPreview(groups []VersionGroupStat, keepCount int) VersionRetentionPreview {
	preview := VersionRetentionPreview{
		Keep:        keepCount,
		TotalGroups: len(groups),
		Groups:      make([]VersionGroupRetentionPreview, 0, len(groups)),
	}
	for _, g := range groups {
		gp := VersionGroupRetentionPreview{
			Namespace:      g.Namespace,
			ResourcePrefix: g.ResourcePrefix,
			GroupKey:       g.GroupKey,
			TotalDirs:      len(g.Versions),
			KeptVersions:   []string{},
			PruneVersions:  []string{},
		}
		preview.TotalVersionDirs += len(g.Versions)
		for idx, v := range g.Versions {
			if idx < keepCount {
				gp.KeptVersions = append(gp.KeptVersions, v.Version)
			} else {
				gp.PruneVersions = append(gp.PruneVersions, v.Version)
				gp.PruneDirs++
				gp.PruneAssetFiles += v.AssetFiles
				gp.PruneAssetBytes += v.AssetBytes
				gp.PruneMetadataFiles += v.MetadataFiles
				gp.PruneMetadataBytes += v.MetadataBytes
				gp.EstimatedDeleteBytes += v.EstimatedDeleteBytes
			}
		}
		preview.PruneDirs += gp.PruneDirs
		preview.PruneAssetFiles += gp.PruneAssetFiles
		preview.PruneAssetBytes += gp.PruneAssetBytes
		preview.PruneMetadataFiles += gp.PruneMetadataFiles
		preview.PruneMetadataBytes += gp.PruneMetadataBytes
		preview.EstimatedDeleteBytes += gp.EstimatedDeleteBytes
		preview.Groups = append(preview.Groups, gp)
	}
	return preview
}

// InvalidateSnapshots marks the latest completed breakdown snapshot as stale.
func (m *Manager) InvalidateSnapshots(reason string) {
	m.invalidateSnapshots(reason)
}

func (m *Manager) invalidateSnapshots(reason string) {
	m.snapshotMu.Lock()
	if m.lastSnapshot != nil && !m.lastSnapshot.Stale {
		cp := *m.lastSnapshot
		cp.Stale = true
		cp.Valid = false
		if reason != "" {
			cp.StaleReason = reason
		}
		m.lastSnapshot = &cp
	}
	m.snapshotMu.Unlock()
}

// GetBreakdownSnapshot returns a copy of the latest completed snapshot (or active scan metadata)
// with up-to-date validity flags. Lock order: persistMu.RLock -> mu.RLock -> snapshotMu.RLock.
func (m *Manager) GetBreakdownSnapshot(customKeep int) (*BreakdownSnapshot, *BreakdownSnapshot) {
	m.persistMu.RLock()
	curGen := m.generation
	curBase := m.GetCacheBase()

	m.snapshotMu.RLock()
	var rawLatest *BreakdownSnapshot
	if m.lastSnapshot != nil {
		cp := *m.lastSnapshot
		rawLatest = &cp
	}
	var active *BreakdownSnapshot
	if m.activeScanSnapshot != nil {
		cpActive := *m.activeScanSnapshot
		active = &cpActive
	}
	m.snapshotMu.RUnlock()
	m.persistMu.RUnlock()

	var latest *BreakdownSnapshot
	if rawLatest != nil {
		cp := *rawLatest
		if cp.Generation != curGen || cp.CacheBase != curBase {
			cp.Stale = true
			cp.Valid = false
			if cp.StaleReason == "" {
				cp.StaleReason = "缓存根目录或代数已变更，请重新扫描"
			}
		} else {
			cp.Valid = (cp.Status == "completed" && !cp.Stale)
		}
		if customKeep > 0 {
			revs := make(map[string]VersionRetentionPreview, len(cp.RetentionPreviews)+1)
			for k, v := range cp.RetentionPreviews {
				revs[k] = v
			}
			key := strconv.Itoa(customKeep)
			if _, exists := revs[key]; !exists {
				revs[key] = ComputeVersionRetentionPreview(cp.VersionGroups, customKeep)
			}
			cp.RetentionPreviews = revs
		}
		latest = &cp
	}

	return latest, active
}

// ScanBreakdownWithProgress performs a single-worker read-only directory walk to build a BreakdownSnapshot.
// It strictly reads directory entries and file attributes without reading file bodies, parsing .ext files,
// populating RAM/ResidentPool/DiskMeta, or issuing upstream requests.
func (m *Manager) ScanBreakdownWithProgress(progressCb func(p ScanProgress), cancelCh <-chan struct{}) *BreakdownSnapshot {
	startTime := time.Now()
	m.persistMu.RLock()
	startGen := m.generation
	base := m.GetCacheBase()
	snapID := fmt.Sprintf("snap-%d-%d", startGen, m.snapshotSeq.Add(1))

	activeSnap := &BreakdownSnapshot{
		SnapshotID:  snapID,
		Generation:  startGen,
		CacheBase:   base,
		Status:      "running",
		Valid:       false,
		StartedAtMS: startTime.UnixMilli(),
	}
	m.snapshotMu.Lock()
	m.activeScanSnapshot = activeSnap
	m.snapshotMu.Unlock()
	m.persistMu.RUnlock()

	defer func() {
		m.snapshotMu.Lock()
		if m.activeScanSnapshot != nil && m.activeScanSnapshot.SnapshotID == snapID {
			m.activeScanSnapshot = nil
		}
		m.snapshotMu.Unlock()
	}()

	absBase, realBase, rootInfo, baseExists, err := resolveContainedRealBaseWithInfo(base)
	if err != nil {
		now := time.Now()
		failed := &BreakdownSnapshot{
			SnapshotID:    snapID,
			Generation:    startGen,
			CacheBase:     base,
			Status:        "failed",
			Valid:         false,
			StartedAtMS:   startTime.UnixMilli(),
			CompletedAtMS: now.UnixMilli(),
			DurationMS:    now.Sub(startTime).Milliseconds(),
			ErrorMessage:  fmt.Sprintf("无法访问缓存根目录: %v", err),
		}
		if progressCb != nil {
			progressCb(ScanProgress{
				SnapshotID: snapID,
				Phase:      "failed",
				Done:       true,
				Status:     "failed",
				Message:    failed.ErrorMessage,
			})
		}
		return failed
	}

	type rawDataFile struct {
		ns        string
		key       string
		slashRel  string
		size      int64
		mtimeNano int64
		fileInfo  os.FileInfo
	}
	type rawExtFile struct {
		ns        string
		cleanKey  string
		slashRel  string
		size      int64
		mtimeNano int64
		fileInfo  os.FileInfo
		paired    bool
	}

	var rawFiles []rawDataFile
	extMap := make(map[string]*rawExtFile)
	dirInfos := make(map[string]os.FileInfo)
	if baseExists && rootInfo != nil {
		dirInfos[absBase] = rootInfo
	}
	var excludedFiles int
	var excludedBytes int64
	var skippedCount int
	var readErrors int
	var walkedEntries int
	var walkedBytes int64
	cancelled := false
	rootChanged := false
	lastReport := time.Now()

	if baseExists {
		_ = filepath.WalkDir(absBase, func(p string, d os.DirEntry, walkErr error) error {
			select {
			case <-cancelCh:
				cancelled = true
				return filepath.SkipAll
			default:
			}
			curGen, curBase := m.currentGenAndBase()
			if curGen != startGen || curBase != base {
				rootChanged = true
				return filepath.SkipAll
			}
			if walkErr != nil {
				readErrors++
				return nil
			}
			if p == absBase {
				return nil
			}

			// Symlink or irregular reparse point check
			if d.Type()&os.ModeSymlink != 0 || d.Type()&os.ModeIrregular != 0 {
				skippedCount++
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if d.IsDir() {
				if err := verifyStrictDirChainWithDirInfos(absBase, realBase, rootInfo, dirInfos, p); err != nil {
					skippedCount++
					return filepath.SkipDir
				}
				if _, exists := dirInfos[p]; !exists {
					if dInfo, dErr := d.Info(); dErr == nil && dInfo != nil {
						dirInfos[p] = dInfo
					}
				}
				return nil
			}

			info, infoErr := d.Info()
			if infoErr != nil {
				readErrors++
				return nil
			}
			if !info.Mode().IsRegular() {
				skippedCount++
				return nil
			}

			rel, relErr := filepath.Rel(absBase, p)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				skippedCount++
				return nil
			}
			slashRel := filepath.ToSlash(rel)

			// Exclude temporary and quarantine files from managed assets
			if strings.Contains(slashRel, ".tmp.") || strings.Contains(slashRel, ".quarantine") {
				excludedFiles++
				excludedBytes += info.Size()
				return nil
			}

			ns, cleanKey, _, ok := extractStrictNamespaceAndKey(absBase, p)
			if !ok {
				skippedCount++
				return nil
			}

			walkedEntries++
			walkedBytes += info.Size()

			if strings.HasSuffix(cleanKey, ".ext") {
				baseRel := strings.TrimSuffix(slashRel, ".ext")
				extMap[baseRel] = &rawExtFile{
					ns:        ns,
					cleanKey:  cleanKey,
					slashRel:  slashRel,
					size:      info.Size(),
					mtimeNano: info.ModTime().UnixNano(),
					fileInfo:  info,
				}
			} else {
				rawFiles = append(rawFiles, rawDataFile{
					ns:        ns,
					key:       cleanKey,
					slashRel:  slashRel,
					size:      info.Size(),
					mtimeNano: info.ModTime().UnixNano(),
					fileInfo:  info,
				})
			}

			now := time.Now()
			if progressCb != nil && (walkedEntries%500 == 0 || now.Sub(lastReport) >= 150*time.Millisecond) {
				lastReport = now
				progressCb(ScanProgress{
					SnapshotID:   snapID,
					Phase:        "scanning",
					ScannedFiles: len(rawFiles),
					ScannedBytes: walkedBytes,
					SkippedCount: skippedCount,
					ReadErrors:   readErrors,
					Done:         false,
					Status:       "running",
				})
			}
			return nil
		})
	}

	curGenAfterWalk, curBaseAfterWalk := m.currentGenAndBase()
	if !cancelled && !rootChanged {
		absAfter, realAfter, rootInfoAfter, existsAfter, afterErr := resolveContainedRealBaseWithInfo(base)
		if afterErr != nil || existsAfter != baseExists {
			rootChanged = true
		} else if baseExists && (absAfter != absBase || realAfter != realBase || (rootInfo != nil && rootInfoAfter != nil && !os.SameFile(rootInfo, rootInfoAfter))) {
			rootChanged = true
		}
	}
	if cancelled || rootChanged || curGenAfterWalk != startGen || curBaseAfterWalk != base {
		now := time.Now()
		status := "cancelled"
		msg := "扫描任务已取消"
		if rootChanged || curGenAfterWalk != startGen || curBaseAfterWalk != base {
			msg = "扫描期间缓存根目录或代数发生变化，扫描已中止"
		}
		incomplete := &BreakdownSnapshot{
			SnapshotID:    snapID,
			Generation:    startGen,
			CacheBase:     base,
			Status:        status,
			Valid:         false,
			StartedAtMS:   startTime.UnixMilli(),
			CompletedAtMS: now.UnixMilli(),
			DurationMS:    now.Sub(startTime).Milliseconds(),
			SkippedCount:  skippedCount,
			ReadErrors:    readErrors,
			ErrorMessage:  msg,
		}
		if progressCb != nil {
			progressCb(ScanProgress{
				SnapshotID:   snapID,
				Phase:        status,
				ScannedFiles: len(rawFiles),
				ScannedBytes: walkedBytes,
				SkippedCount: skippedCount,
				ReadErrors:   readErrors,
				Done:         true,
				Status:       status,
				Message:      msg,
			})
		}
		return incomplete
	}

	// Deterministic order by (NS, Key)
	sort.Slice(rawFiles, func(i, j int) bool {
		if rawFiles[i].ns != rawFiles[j].ns {
			return rawFiles[i].ns < rawFiles[j].ns
		}
		return rawFiles[i].key < rawFiles[j].key
	})

	catStats, catByKey := buildEmptyCategoryStats()
	nsMap := make(map[string]*NamespaceStat)

	type versionDirKey struct {
		ns      string
		prefix  string
		version string
		verNum  int
	}
	versionDirMap := make(map[versionDirKey]*VersionDirStat)

	// Seed versionDirMap from shared directory discovery so empty version dirs,
	// dirs with only .tmp.* files, or dirs with only orphan .ext files are included
	// consistently with live slimming discovery.
	if baseExists {
		discoveredDirs, dirFailures := discoverAllVersionDirsWithErrorsAndDirInfos(absBase, realBase, dirInfos)
		readErrors += len(dirFailures)
		for _, d := range discoveredDirs {
			vk := versionDirKey{ns: d.ns, prefix: d.prefix, version: d.version, verNum: d.verNum}
			if _, exists := versionDirMap[vk]; !exists {
				relDir := d.prefix + "/" + d.version
				if d.ns != "gbf" {
					relDir = "hosts/" + d.ns + "/" + relDir
				}
				versionDirMap[vk] = &VersionDirStat{
					Version:     d.version,
					VersionNum:  d.verNum,
					RelativeDir: relDir,
				}
			}
		}
	}

	entries := make([]SnapshotEntry, 0, len(rawFiles))
	entryByNSKey := make(map[string]int, len(rawFiles))

	var totalAssetFiles int
	var totalAssetBytes int64
	var totalMetaFiles int
	var totalMetaBytes int64

	for _, rf := range rawFiles {
		var metaBytes int64
		var extMTimeNano int64
		var extFileInfo os.FileInfo
		hasMeta := false
		if ext, ok := extMap[rf.slashRel]; ok {
			ext.paired = true
			hasMeta = true
			metaBytes = ext.size
			extMTimeNano = ext.mtimeNano
			extFileInfo = ext.fileInfo
			totalMetaFiles++
			totalMetaBytes += ext.size
		}

		cat := ClassifyCacheEntry(rf.ns, rf.key)
		entryIdx := len(entries)
		entries = append(entries, SnapshotEntry{
			NS:           rf.ns,
			Key:          rf.key,
			Category:     cat,
			Size:         rf.size,
			MetaBytes:    metaBytes,
			HasMeta:      hasMeta,
			MTimeNano:    rf.mtimeNano,
			MTimeMS:      rf.mtimeNano / 1e6,
			fileInfo:     rf.fileInfo,
			extMTimeNano: extMTimeNano,
			extFileInfo:  extFileInfo,
		})
		entryByNSKey[makeNSKey(rf.ns, rf.key)] = entryIdx

		totalAssetFiles++
		totalAssetBytes += rf.size

		if cs, ok := catByKey[cat]; ok {
			cs.AssetFiles++
			cs.AssetBytes += rf.size
			if hasMeta {
				cs.MetadataFiles++
				cs.MetadataBytes += metaBytes
			}
			cs.EstimatedDeleteBytes = cs.AssetBytes + cs.MetadataBytes
		}

		nsStat, ok := nsMap[rf.ns]
		if !ok {
			nsStat = &NamespaceStat{
				Namespace:  rf.ns,
				Categories: make(map[string]CategoryStat),
			}
			nsMap[rf.ns] = nsStat
		}
		nsStat.AssetFiles++
		nsStat.AssetBytes += rf.size
		if hasMeta {
			nsStat.MetadataFiles++
			nsStat.MetadataBytes += metaBytes
		}
		nsStat.EstimatedDeleteBytes = nsStat.AssetBytes + nsStat.MetadataBytes

		nsCat := nsStat.Categories[cat]
		nsCat.Category = cat
		nsCat.Label = categoryMeta[cat].Label
		nsCat.Selectable = categoryMeta[cat].Selectable
		nsCat.AssetFiles++
		nsCat.AssetBytes += rf.size
		if hasMeta {
			nsCat.MetadataFiles++
			nsCat.MetadataBytes += metaBytes
		}
		nsCat.EstimatedDeleteBytes = nsCat.AssetBytes + nsCat.MetadataBytes
		nsStat.Categories[cat] = nsCat

		if cat == CategoryVersioned {
			if prefix, verStr, verNum, ok := ParseVersionedEntry(rf.ns, rf.key); ok {
				vk := versionDirKey{ns: rf.ns, prefix: prefix, version: verStr, verNum: verNum}
				vd, exists := versionDirMap[vk]
				if !exists {
					relDir := prefix + "/" + verStr
					if rf.ns != "gbf" {
						relDir = "hosts/" + rf.ns + "/" + relDir
					}
					vd = &VersionDirStat{
						Version:     verStr,
						VersionNum:  verNum,
						RelativeDir: relDir,
					}
					versionDirMap[vk] = vd
				}
				vd.AssetFiles++
				vd.AssetBytes += rf.size
				if hasMeta {
					vd.MetadataFiles++
					vd.MetadataBytes += metaBytes
				}
				vd.EstimatedDeleteBytes = vd.AssetBytes + vd.MetadataBytes
			}
		}
	}

	// Calculate category percentages using asset_bytes as denominator (Section 3.2)
	for i := range catStats {
		if totalAssetBytes > 0 {
			catStats[i].Percentage = math.Round(float64(catStats[i].AssetBytes)/float64(totalAssetBytes)*1000) / 10
		}
	}

	// Collect orphan .ext files and attribute version-directory orphan .ext files to VersionDirStat
	var orphanExtFiles int
	var orphanExtBytes int64
	orphanExts := make(map[string]orphanExtCandidate)
	for _, ext := range extMap {
		if !ext.paired {
			orphanExtFiles++
			orphanExtBytes += ext.size
			orphanExts[ext.slashRel] = orphanExtCandidate{
				ns:        ext.ns,
				cleanKey:  ext.cleanKey,
				slashRel:  ext.slashRel,
				size:      ext.size,
				mtimeNano: ext.mtimeNano,
				fileInfo:  ext.fileInfo,
			}
			baseKey := strings.TrimSuffix(ext.cleanKey, ".ext")
			if prefix, verStr, verNum, ok := ParseVersionedEntry(ext.ns, baseKey); ok {
				vk := versionDirKey{ns: ext.ns, prefix: prefix, version: verStr, verNum: verNum}
				vd, exists := versionDirMap[vk]
				if !exists {
					relDir := prefix + "/" + verStr
					if ext.ns != "gbf" {
						relDir = "hosts/" + ext.ns + "/" + relDir
					}
					vd = &VersionDirStat{
						Version:     verStr,
						VersionNum:  verNum,
						RelativeDir: relDir,
					}
					versionDirMap[vk] = vd
				}
				vd.MetadataFiles++
				vd.MetadataBytes += ext.size
				vd.EstimatedDeleteBytes = vd.AssetBytes + vd.MetadataBytes
			}
		}
	}

	// Sort namespaces: "gbf" first, then alphabetical
	namespaces := make([]NamespaceStat, 0, len(nsMap))
	for _, nsStat := range nsMap {
		namespaces = append(namespaces, *nsStat)
	}
	sort.Slice(namespaces, func(i, j int) bool {
		if namespaces[i].Namespace == "gbf" {
			return true
		}
		if namespaces[j].Namespace == "gbf" {
			return false
		}
		return namespaces[i].Namespace < namespaces[j].Namespace
	})

	// Build VersionGroupStat sorted by (namespace, prefix) and descending version number
	type groupKey struct {
		ns     string
		prefix string
	}
	groupMap := make(map[groupKey]*VersionGroupStat)
	for vk, vd := range versionDirMap {
		gk := groupKey{ns: vk.ns, prefix: vk.prefix}
		grp, exists := groupMap[gk]
		if !exists {
			grp = &VersionGroupStat{
				Namespace:      vk.ns,
				ResourcePrefix: vk.prefix,
				GroupKey:       vk.ns + ":" + vk.prefix,
			}
			groupMap[gk] = grp
		}
		grp.Versions = append(grp.Versions, *vd)
		grp.AssetFiles += vd.AssetFiles
		grp.AssetBytes += vd.AssetBytes
		grp.MetadataFiles += vd.MetadataFiles
		grp.MetadataBytes += vd.MetadataBytes
		grp.EstimatedDeleteBytes += vd.EstimatedDeleteBytes
	}

	versionGroups := make([]VersionGroupStat, 0, len(groupMap))
	for _, grp := range groupMap {
		sort.Slice(grp.Versions, func(i, j int) bool {
			if grp.Versions[i].VersionNum != grp.Versions[j].VersionNum {
				return grp.Versions[i].VersionNum > grp.Versions[j].VersionNum
			}
			return grp.Versions[i].Version > grp.Versions[j].Version
		})
		grp.TotalDirs = len(grp.Versions)
		versionGroups = append(versionGroups, *grp)
	}
	sort.Slice(versionGroups, func(i, j int) bool {
		if versionGroups[i].Namespace != versionGroups[j].Namespace {
			if versionGroups[i].Namespace == "gbf" {
				return true
			}
			if versionGroups[j].Namespace == "gbf" {
				return false
			}
			return versionGroups[i].Namespace < versionGroups[j].Namespace
		}
		return versionGroups[i].ResourcePrefix < versionGroups[j].ResourcePrefix
	})

	retentionPreviews := map[string]VersionRetentionPreview{
		"2": ComputeVersionRetentionPreview(versionGroups, 2),
		"4": ComputeVersionRetentionPreview(versionGroups, 4),
		"8": ComputeVersionRetentionPreview(versionGroups, 8),
	}

	now := time.Now()
	completed := &BreakdownSnapshot{
		SnapshotID:          snapID,
		Generation:          startGen,
		CacheBase:           base,
		Status:              "completed",
		Valid:               true,
		Stale:               false,
		StartedAtMS:         startTime.UnixMilli(),
		CompletedAtMS:       now.UnixMilli(),
		DurationMS:          now.Sub(startTime).Milliseconds(),
		AssetFiles:          totalAssetFiles,
		AssetBytes:          totalAssetBytes,
		MetadataFiles:       totalMetaFiles,
		MetadataBytes:       totalMetaBytes,
		OrphanMetadataFiles: orphanExtFiles,
		OrphanMetadataBytes: orphanExtBytes,
		ExcludedFiles:       excludedFiles,
		ExcludedBytes:       excludedBytes,
		SkippedCount:        skippedCount,
		ReadErrors:          readErrors,
		Categories:          catStats,
		Namespaces:          namespaces,
		VersionGroups:       versionGroups,
		RetentionPreviews:   retentionPreviews,
		Entries:             entries,
		entryByNSKey:        entryByNSKey,
		orphanExts:          orphanExts,
		dirInfos:            dirInfos,
		absBase:             absBase,
		realBase:            realBase,
		rootInfo:            rootInfo,
		baseExists:          baseExists,
	}

	// Enforce strict lock hierarchy: persistMu.RLock -> mu.RLock -> snapshotMu.Lock
	m.persistMu.RLock()
	curGenFinal := m.generation
	curBaseFinal := m.GetCacheBase()
	rootStillValid := true
	if curGenFinal == startGen && curBaseFinal == base {
		absPub, realPub, rootInfoPub, existsPub, pubErr := resolveContainedRealBaseWithInfo(base)
		if pubErr != nil || existsPub != baseExists {
			rootStillValid = false
		} else if baseExists && (absPub != absBase || realPub != realBase || (rootInfo != nil && rootInfoPub != nil && !os.SameFile(rootInfo, rootInfoPub))) {
			rootStillValid = false
		}
	}
	m.snapshotMu.Lock()
	if curGenFinal == startGen && curBaseFinal == base && rootStillValid {
		stored := *completed
		m.lastSnapshot = &stored
	} else {
		completed.Stale = true
		completed.Valid = false
		completed.StaleReason = "扫描完成时缓存根目录或代数已变更"
	}
	m.snapshotMu.Unlock()
	m.persistMu.RUnlock()

	if progressCb != nil {
		progressCb(ScanProgress{
			SnapshotID:   snapID,
			Phase:        "completed",
			ScannedFiles: totalAssetFiles,
			ScannedBytes: totalAssetBytes + totalMetaBytes,
			SkippedCount: skippedCount,
			ReadErrors:   readErrors,
			Done:         true,
			Status:       "completed",
		})
	}

	return completed
}

func (m *Manager) getValidatedSnapshot(snapshotID string) (*BreakdownSnapshot, error) {
	m.persistMu.RLock()
	curGen := m.generation
	curBase := m.GetCacheBase()

	m.snapshotMu.RLock()
	snap := m.lastSnapshot
	if snap == nil || snap.Status != "completed" {
		m.snapshotMu.RUnlock()
		m.persistMu.RUnlock()
		return nil, ErrSnapshotNotFound
	}
	if snapshotID != "" && snap.SnapshotID != snapshotID {
		m.snapshotMu.RUnlock()
		m.persistMu.RUnlock()
		return nil, ErrSnapshotStale
	}
	if snap.Stale || !snap.Valid || snap.Generation != curGen || snap.CacheBase != curBase {
		m.snapshotMu.RUnlock()
		m.persistMu.RUnlock()
		return nil, ErrSnapshotStale
	}
	cp := *snap
	m.snapshotMu.RUnlock()
	m.persistMu.RUnlock()
	return &cp, nil
}

// --- Search over Snapshot Index (Section 4.3) ---

// SearchSnapshot searches the latest valid breakdown snapshot by full HTTP/HTTPS URL,
// relative resource path, or filename/path fragment (plain substring match, no regex).
func (m *Manager) SearchSnapshot(snapshotID, query string, limit int, hostNormalizer func(host string) (string, bool)) (*SearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, ErrEmptySearchQuery
	}
	trimmed := trimTrailingASCIISpaceOrControl(trimLeadingASCIIControl(query))
	trimmed = strings.Trim(trimmed, " ")
	if trimmed == "" {
		return nil, ErrEmptySearchQuery
	}
	if len(trimmed) > MaxSearchQueryLen {
		return nil, ErrSearchQueryTooLong
	}
	if limit == 0 {
		limit = DefaultSearchLimit
	}
	if limit < 1 || limit > MaxSearchLimit {
		return nil, ErrInvalidSearchLimit
	}

	snap, err := m.getValidatedSnapshot(snapshotID)
	if err != nil {
		return nil, err
	}

	var filterNS string
	var searchPathLower string
	isURLQuery := false

	lowerTrimmed := strings.ToLower(trimmed)
	if strings.HasPrefix(lowerTrimmed, "http://") || strings.HasPrefix(lowerTrimmed, "https://") {
		if u, parseErr := url.Parse(trimmed); parseErr == nil && u.Host != "" {
			isURLQuery = true
			host := u.Hostname()
			if hostNormalizer != nil {
				filterNS, _ = hostNormalizer(host)
			} else {
				filterNS = sanitizeNamespace(host)
			}
			cleanP := strings.TrimPrefix(strings.ReplaceAll(u.Path, "\\", "/"), "/")
			segs := splitCleanSegments(cleanP)
			searchPathLower = strings.ToLower(strings.Join(segs, "/"))
			if filterNS == "gbf" && len(segs) >= 3 && strings.EqualFold(segs[0], "hosts") {
				filterNS = sanitizeNamespace(segs[1])
				searchPathLower = strings.ToLower(strings.Join(segs[2:], "/"))
			}
		}
	}

	if !isURLQuery {
		noQuery, _, _ := strings.Cut(trimmed, "?")
		norm := strings.TrimPrefix(strings.ReplaceAll(strings.Trim(noQuery, " \t\r\n"), "\\", "/"), "/")
		if strings.HasPrefix(strings.ToLower(norm), "hosts/") {
			parts := strings.SplitN(norm, "/", 3)
			if len(parts) == 3 && parts[1] != "" && parts[2] != "" {
				filterNS = sanitizeNamespace(parts[1])
				norm = parts[2]
			}
		}
		searchPathLower = strings.ToLower(norm)
	}

	if searchPathLower == "" {
		return nil, ErrEmptySearchQuery
	}

	var altSearchLower string
	if filterNS == "" || filterNS == "gbf" {
		if alt, ok := gbfFallbackCleanKey(searchPathLower); ok {
			altSearchLower = strings.ToLower(alt)
		}
	}

	type matchedCandidate struct {
		entry      SnapshotEntry
		exactMatch bool
	}

	var matches []matchedCandidate
	for _, entry := range snap.Entries {
		if filterNS != "" && entry.NS != filterNS {
			continue
		}
		keyLower := strings.ToLower(entry.Key)
		exact := (keyLower == searchPathLower) || (entry.NS == "gbf" && altSearchLower != "" && keyLower == altSearchLower)
		matched := exact
		if !matched {
			if strings.Contains(keyLower, searchPathLower) {
				matched = true
			} else if entry.NS == "gbf" && altSearchLower != "" && strings.Contains(keyLower, altSearchLower) {
				matched = true
			} else if filterNS == "" && strings.Contains(strings.ToLower(entry.NS+"/"+entry.Key), searchPathLower) {
				matched = true
			}
		}
		if matched {
			matches = append(matches, matchedCandidate{
				entry:      entry,
				exactMatch: exact,
			})
		}
	}

	// Deterministic sort: exact matches first, then (NS, Key)
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].exactMatch != matches[j].exactMatch {
			return matches[i].exactMatch
		}
		if matches[i].entry.NS != matches[j].entry.NS {
			return matches[i].entry.NS < matches[j].entry.NS
		}
		return matches[i].entry.Key < matches[j].entry.Key
	})

	totalMatches := len(matches)
	truncated := totalMatches > limit
	if truncated {
		matches = matches[:limit]
	}

	items := make([]SearchItem, 0, len(matches))
	for _, mc := range matches {
		e := mc.entry
		estBytes := e.Size + e.MetaBytes
		hasAlias := false
		aliasKey := ""
		if e.NS == "gbf" {
			if altKey, ok := gbfFallbackCleanKey(e.Key); ok {
				if altIdx, exists := snap.entryByNSKey[makeNSKey("gbf", altKey)]; exists {
					altEntry := snap.Entries[altIdx]
					hasAlias = true
					aliasKey = altEntry.Key
					estBytes += altEntry.Size + altEntry.MetaBytes
				}
			}
		}
		items = append(items, SearchItem{
			NS:                   e.NS,
			Key:                  e.Key,
			Category:             e.Category,
			CategoryLabel:        categoryMeta[e.Category].Label,
			AssetBytes:           e.Size,
			MetadataBytes:        e.MetaBytes,
			EstimatedDeleteBytes: estBytes,
			HasMetadata:          e.HasMeta,
			FileModifiedMS:       e.MTimeMS,
			FileModifiedISO:      time.Unix(0, e.MTimeNano).UTC().Format(time.RFC3339),
			HasAlias:             hasAlias,
			AliasKey:             aliasKey,
		})
	}

	return &SearchResponse{
		OK:            true,
		SnapshotID:    snap.SnapshotID,
		CompletedAtMS: snap.CompletedAtMS,
		Query:         trimmed,
		Limit:         limit,
		TotalMatches:  totalMatches,
		Truncated:     truncated,
		Items:         items,
	}, nil
}

// --- Controlled Deletion & Selective Cleanup (Section 5 & Section 6) ---

// DeleteTargetEntry identifies a target cache item in a deletion request.
type DeleteTargetEntry struct {
	NS  string `json:"ns"`
	Key string `json:"key"`
}

// ExpandSnapshotTargets validates requested (NS, Key) pairs against the snapshot,
// expands GBF fallback aliases if both exist in the snapshot, and deduplicates by physical identity.
func ExpandSnapshotTargets(snap *BreakdownSnapshot, targets []DeleteTargetEntry) ([]SnapshotEntry, []DeleteFailure) {
	seen := make(map[string]struct{}, len(targets)*2)
	var candidates []SnapshotEntry
	var failures []DeleteFailure

	for _, t := range targets {
		normNS, normKey, ok := validateManagedNamespaceAndKey(t.NS, t.Key)
		if !ok {
			failures = append(failures, DeleteFailure{
				NS:     t.NS,
				Key:    t.Key,
				Reason: "非法的缓存命名空间或资源路径",
			})
			continue
		}

		nsKey := makeNSKey(normNS, normKey)
		idx, exists := snap.entryByNSKey[nsKey]
		var matchedAny bool
		if exists {
			matchedAny = true
			if _, dup := seen[nsKey]; !dup {
				seen[nsKey] = struct{}{}
				candidates = append(candidates, snap.Entries[idx])
			}
		}

		if normNS == "gbf" {
			if altKey, hasAlt := gbfFallbackCleanKey(normKey); hasAlt {
				altNSKey := makeNSKey("gbf", altKey)
				if altIdx, altExists := snap.entryByNSKey[altNSKey]; altExists {
					matchedAny = true
					if _, dup := seen[altNSKey]; !dup {
						seen[altNSKey] = struct{}{}
						candidates = append(candidates, snap.Entries[altIdx])
					}
				}
			}
		}

		if !matchedAny {
			failures = append(failures, DeleteFailure{
				NS:     normNS,
				Key:    normKey,
				Reason: "目标条目不在当前有效扫描快照中",
			})
		}
	}

	return candidates, failures
}

// DeleteSnapshotEntriesWithProgress deletes a verified batch of snapshot entries using
// per-key invalidation tokens and identity verification.
func (m *Manager) DeleteSnapshotEntriesWithProgress(
	snapshotID string,
	targets []DeleteTargetEntry,
	progressCb func(p CleanupProgress),
	cancelCh <-chan struct{},
) (*DeleteResult, error) {
	if len(targets) == 0 {
		return nil, ErrEmptyCleanupSelection
	}
	if len(targets) > MaxInvalidateBatchSize {
		return nil, ErrBatchLimitExceeded
	}
	snap, err := m.getValidatedSnapshot(snapshotID)
	if err != nil {
		return nil, err
	}

	candidates, initialFailures := ExpandSnapshotTargets(snap, targets)
	res := m.executeControlledDeletion("invalidate", snap, candidates, initialFailures, progressCb, cancelCh)
	return &res, nil
}

// ClearCategoriesWithProgress deletes all entries matching the selected non-versioned categories
// and namespaces from the specified valid snapshot.
func (m *Manager) ClearCategoriesWithProgress(
	snapshotID string,
	categories []string,
	namespaces []string,
	progressCb func(p CleanupProgress),
	cancelCh <-chan struct{},
) (*DeleteResult, error) {
	if len(categories) == 0 {
		return nil, ErrEmptyCleanupSelection
	}
	catSet := make(map[string]struct{}, len(categories))
	for _, c := range categories {
		trimmed := strings.TrimSpace(c)
		if trimmed == CategoryVersioned {
			return nil, ErrVersionedCatDisallowed
		}
		if !IsSelectableCleanupCategory(trimmed) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidCategory, trimmed)
		}
		catSet[trimmed] = struct{}{}
	}

	nsSet := make(map[string]struct{})
	if len(namespaces) == 0 {
		// Section 3.1: Category cleanup defaults to "gbf" namespace unless others are explicitly selected
		nsSet["gbf"] = struct{}{}
	} else {
		for _, ns := range namespaces {
			raw := strings.TrimSpace(ns)
			if raw == "" {
				continue
			}
			if strings.Contains(raw, "..") || strings.ContainsAny(raw, `/\:`) || raw == "hosts" {
				return nil, fmt.Errorf("无效的缓存命名空间: %s", raw)
			}
			nsSet[sanitizeNamespace(raw)] = struct{}{}
		}
		if len(nsSet) == 0 {
			nsSet["gbf"] = struct{}{}
		}
	}

	snap, err := m.getValidatedSnapshot(snapshotID)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var candidates []SnapshotEntry
	for _, entry := range snap.Entries {
		if _, ok := nsSet[entry.NS]; !ok {
			continue
		}
		if entry.Category == CategoryVersioned {
			continue
		}
		if _, ok := catSet[entry.Category]; !ok {
			continue
		}
		nsKey := makeNSKey(entry.NS, entry.Key)
		if _, dup := seen[nsKey]; !dup {
			seen[nsKey] = struct{}{}
			candidates = append(candidates, entry)
		}
		// Include GBF fallback alias only if it exists in snapshot AND belongs to a selected non-versioned category
		if entry.NS == "gbf" {
			if altKey, hasAlt := gbfFallbackCleanKey(entry.Key); hasAlt {
				altNSKey := makeNSKey("gbf", altKey)
				if altIdx, altExists := snap.entryByNSKey[altNSKey]; altExists {
					altEntry := snap.Entries[altIdx]
					if altEntry.Category != CategoryVersioned {
						if _, catOK := catSet[altEntry.Category]; catOK {
							if _, dup := seen[altNSKey]; !dup {
								seen[altNSKey] = struct{}{}
								candidates = append(candidates, altEntry)
							}
						}
					}
				}
			}
		}
	}

	res := m.executeControlledDeletion("clear_category", snap, candidates, nil, progressCb, cancelCh)
	return &res, nil
}

func (m *Manager) executeControlledDeletion(
	taskType string,
	snap *BreakdownSnapshot,
	candidates []SnapshotEntry,
	initialFailures []DeleteFailure,
	progressCb func(p CleanupProgress),
	cancelCh <-chan struct{},
) DeleteResult {
	res := DeleteResult{
		TaskType:       taskType,
		SnapshotID:     snap.SnapshotID,
		CandidateFiles: len(candidates),
		FailedCount:    len(initialFailures),
	}
	if len(initialFailures) > 0 {
		if len(initialFailures) > 50 {
			res.Failures = append(res.Failures, initialFailures[:50]...)
		} else {
			res.Failures = append(res.Failures, initialFailures...)
		}
	}

	absBase, realBase, rootInfo, baseExists, baseErr := resolveContainedRealBaseWithInfo(snap.CacheBase)
	if baseErr == nil && baseExists && snap.baseExists && snap.rootInfo != nil {
		if !os.SameFile(snap.rootInfo, rootInfo) || (snap.realBase != "" && snap.realBase != realBase) {
			baseErr = fmt.Errorf("缓存根目录物理身份已变更: %s", snap.CacheBase)
		}
	}
	if baseErr != nil || !baseExists {
		if baseErr != nil {
			if len(candidates) > 0 {
				res.FailedCount += len(candidates)
			} else {
				res.FailedCount++
			}
			res.Status = "failed"
			res.Done = true
			res.Message = fmt.Sprintf("缓存根目录校验失败: %v", baseErr)
			appendBoundedFailure(&res, "", snap.CacheBase, res.Message)
			if progressCb != nil {
				progressCb(CleanupProgress{
					TaskType:    taskType,
					SnapshotID:  snap.SnapshotID,
					TotalFiles:  len(candidates),
					FailedCount: res.FailedCount,
					Done:        true,
				})
			}
			return res
		}
	}

	touchedDirs := make(map[string]struct{})
	lastReport := time.Now()
	total := len(candidates)

	for idx, cand := range candidates {
		select {
		case <-cancelCh:
			res.Cancelled = true
		default:
		}
		if res.Cancelled {
			break
		}
		curGen, curBase := m.currentGenAndBase()
		if curGen != snap.Generation || curBase != snap.CacheBase {
			res.Cancelled = true
			res.Message = "清理期间缓存根目录已切换，操作已中止"
			break
		}

		m.deleteSingleVerifiedEntryWithDirInfos(absBase, realBase, rootInfo, snap.dirInfos, snap.Generation, snap.CacheBase, cand, &res, touchedDirs)

		now := time.Now()
		if progressCb != nil && ((idx+1)%100 == 0 || now.Sub(lastReport) >= 150*time.Millisecond || idx+1 == total) {
			lastReport = now
			progressCb(CleanupProgress{
				TaskType:            taskType,
				SnapshotID:          snap.SnapshotID,
				ProcessedFiles:      idx + 1,
				TotalFiles:          total,
				DeletedAssetFiles:   res.DeletedAssetFiles,
				DeletedAssetBytes:   res.DeletedAssetBytes,
				DeletedMetaFiles:    res.DeletedMetaFiles,
				DeletedMetaBytes:    res.DeletedMetaBytes,
				DeletedFiles:        res.DeletedAssetFiles + res.DeletedMetaFiles,
				FreedBytes:          res.DeletedAssetBytes + res.DeletedMetaBytes,
				AlreadyMissingCount: res.AlreadyMissingCount,
				SkippedChangedCount: res.SkippedChangedCount,
				FailedCount:         res.FailedCount,
				Cancelled:           false,
				Done:                false,
			})
		}
	}

	if m.ramBudget != nil && m.residentPool != nil && m.residentPool.Enabled() {
		m.ramBudget.SyncSLRU(m.ramCache)
	}

	// Best-effort removal of empty parent directories up to cache root
	if !res.Cancelled && len(touchedDirs) > 0 && absBase != "" {
		removedDirs, dirCancelled := m.removeEmptyParentDirsControlledWithDirInfos(absBase, realBase, rootInfo, snap.dirInfos, snap.Generation, snap.CacheBase, touchedDirs, cancelCh)
		res.DeletedDirs += removedDirs
		if dirCancelled {
			res.Cancelled = true
		}
	}

	res.DeletedFiles = res.DeletedAssetFiles + res.DeletedMetaFiles
	res.FreedBytes = res.DeletedAssetBytes + res.DeletedMetaBytes
	res.FreedMB = math.Round(float64(res.FreedBytes)/(1024*1024)*100) / 100
	res.Done = true

	if res.DeletedFiles > 0 || res.AlreadyMissingCount > 0 || res.SkippedChangedCount > 0 {
		m.invalidateSnapshots("已执行缓存清理，请重新扫描以刷新统计")
	}

	switch {
	case res.Cancelled:
		res.Status = "cancelled"
		if res.Message == "" {
			res.Message = fmt.Sprintf("操作已取消（已删除 %d 个素材文件）", res.DeletedAssetFiles)
		}
	case res.FailedCount > 0 && res.DeletedFiles == 0:
		res.Status = "failed"
		res.Message = fmt.Sprintf("删除本地缓存失败（%d 项失败，%d 项变化跳过）", res.FailedCount, res.SkippedChangedCount)
	case res.FailedCount > 0:
		res.Status = "partial_failed"
		res.Message = fmt.Sprintf("完成删除 %d 个素材文件，%d 项失败，%d 项变化跳过", res.DeletedAssetFiles, res.FailedCount, res.SkippedChangedCount)
	default:
		res.Status = "completed"
		res.Message = fmt.Sprintf("已删除 %d 个素材文件（共释放 %.2f MiB）", res.DeletedAssetFiles, res.FreedMB)
	}

	if progressCb != nil {
		progressCb(CleanupProgress{
			TaskType:            taskType,
			SnapshotID:          snap.SnapshotID,
			ProcessedFiles:      total,
			TotalFiles:          total,
			DeletedAssetFiles:   res.DeletedAssetFiles,
			DeletedAssetBytes:   res.DeletedAssetBytes,
			DeletedMetaFiles:    res.DeletedMetaFiles,
			DeletedMetaBytes:    res.DeletedMetaBytes,
			DeletedFiles:        res.DeletedFiles,
			FreedBytes:          res.FreedBytes,
			AlreadyMissingCount: res.AlreadyMissingCount,
			SkippedChangedCount: res.SkippedChangedCount,
			FailedCount:         res.FailedCount,
			Cancelled:           res.Cancelled,
			Done:                true,
		})
	}

	return res
}

func appendBoundedFailure(res *DeleteResult, ns, key, reason string) {
	if len(res.Failures) < 50 {
		res.Failures = append(res.Failures, DeleteFailure{
			NS:     ns,
			Key:    key,
			Reason: reason,
		})
	}
}

func (m *Manager) deleteSingleVerifiedEntry(
	absBase string,
	realBase string,
	expectedGen uint64,
	expectedBase string,
	cand SnapshotEntry,
	res *DeleteResult,
	touchedDirs map[string]struct{},
) {
	m.deleteSingleVerifiedEntryWithDirInfos(absBase, realBase, nil, nil, expectedGen, expectedBase, cand, res, touchedDirs)
}

func (m *Manager) deleteSingleVerifiedEntryWithRootInfo(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	expectedGen uint64,
	expectedBase string,
	cand SnapshotEntry,
	res *DeleteResult,
	touchedDirs map[string]struct{},
) {
	m.deleteSingleVerifiedEntryWithDirInfos(absBase, realBase, expectedRootFi, nil, expectedGen, expectedBase, cand, res, touchedDirs)
}

func (m *Manager) deleteSingleVerifiedEntryWithDirInfos(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	dirInfos map[string]os.FileInfo,
	expectedGen uint64,
	expectedBase string,
	cand SnapshotEntry,
	res *DeleteResult,
	touchedDirs map[string]struct{},
) {
	normNS, normKey, valid := validateManagedNamespaceAndKey(cand.NS, cand.Key)
	if !valid {
		res.FailedCount++
		appendBoundedFailure(res, cand.NS, cand.Key, "无效的命名空间或相对路径")
		return
	}

	m.persistMu.RLock()
	if m.generation != expectedGen || (expectedBase != "" && m.GetCacheBase() != expectedBase) {
		m.persistMu.RUnlock()
		res.Cancelled = true
		return
	}

	filePath, ok := m.resolvePathWithNamespace(normNS, normKey)
	if !ok {
		m.persistMu.RUnlock()
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey, "无法解析缓存文件路径")
		return
	}
	extPath := filePath + ".ext"

	shard, canonKey := m.tokenShardFor(normNS, normKey)
	shard.mu.Lock()

	// 1. Advance per-key invalidation token and clear all in-memory representations
	bumpInvalidateTokenLocked(shard, canonKey)
	m.clearKeyMemoryState(normNS, normKey)

	// 2. Open pinned parent directory handle via step-by-step os.Root traversal inside the synchronized boundary
	parentRoot, leafName, closeParent, chainErr := openPinnedParentRoot(absBase, realBase, expectedRootFi, dirInfos, filePath)
	if chainErr != nil {
		if os.IsNotExist(chainErr) && verifyStrictRootIdentity(absBase, realBase, expectedRootFi) == nil {
			res.AlreadyMissingCount++
		} else {
			res.FailedCount++
			appendBoundedFailure(res, normNS, normKey, fmt.Sprintf("路径边界校验未通过: %v", chainErr))
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}
	defer closeParent()

	extLeaf := leafName + ".ext"

	// 3. Verify physical file identity (regular file + size + mtime + os.SameFile) on the pinned parent handle
	lfi, statErr := parentRoot.Lstat(leafName)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			res.AlreadyMissingCount++
			// Clean up paired .ext if it still lingers after data file disappeared and matches recorded metadata identity
			if extFi, extErr := parentRoot.Lstat(extLeaf); extErr == nil {
				if extFi.IsDir() || extFi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !extFi.Mode().IsRegular() {
					res.FailedCount++
					appendBoundedFailure(res, normNS, normKey+".ext", "残留元数据路径不是普通文件")
				} else if (cand.extFileInfo != nil && (extFi.Size() != cand.MetaBytes || extFi.ModTime().UnixNano() != cand.extMTimeNano || !os.SameFile(cand.extFileInfo, extFi))) || (!cand.HasMeta && cand.fileInfo != nil) {
					res.SkippedChangedCount++
				} else {
					extSize := extFi.Size()
					if testBeforeUnlinkHook != nil {
						testBeforeUnlinkHook(extPath)
					}
					if rmErr := parentRoot.Remove(extLeaf); rmErr == nil {
						res.DeletedMetaFiles++
						res.DeletedMetaBytes += extSize
						touchedDirs[filepath.Dir(filePath)] = struct{}{}
					} else if !os.IsNotExist(rmErr) {
						res.FailedCount++
						appendBoundedFailure(res, normNS, normKey+".ext", rmErr.Error())
					}
				}
			} else if !os.IsNotExist(extErr) {
				res.FailedCount++
				appendBoundedFailure(res, normNS, normKey+".ext", extErr.Error())
			}
		} else {
			res.FailedCount++
			appendBoundedFailure(res, normNS, normKey, statErr.Error())
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	if lfi.IsDir() || lfi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !lfi.Mode().IsRegular() {
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey, "目标不是普通缓存文件")
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	if lfi.Size() != cand.Size || lfi.ModTime().UnixNano() != cand.MTimeNano || (cand.fileInfo != nil && !os.SameFile(cand.fileInfo, lfi)) {
		// File was modified or replaced after the snapshot was taken: skip deleting the disk file
		res.SkippedChangedCount++
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	if testBeforeUnlinkHook != nil {
		testBeforeUnlinkHook(filePath)
	}

	// 4. Delete verified data file via handle-relative unlinkat/Deleteat on parentRoot
	if rmErr := parentRoot.Remove(leafName); rmErr != nil {
		if os.IsNotExist(rmErr) {
			res.AlreadyMissingCount++
		} else {
			res.FailedCount++
			appendBoundedFailure(res, normNS, normKey, rmErr.Error())
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	res.DeletedAssetFiles++
	res.DeletedAssetBytes += cand.Size
	touchedDirs[filepath.Dir(filePath)] = struct{}{}

	// 5. Delete paired .ext metadata file if present on the same pinned parentRoot
	if extFi, extErr := parentRoot.Lstat(extLeaf); extErr == nil {
		if extFi.IsDir() || extFi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !extFi.Mode().IsRegular() {
			res.FailedCount++
			appendBoundedFailure(res, normNS, normKey+".ext", "配对元数据路径不是普通文件")
		} else {
			extSize := extFi.Size()
			if testBeforeUnlinkHook != nil {
				testBeforeUnlinkHook(extPath)
			}
			if rmExtErr := parentRoot.Remove(extLeaf); rmExtErr == nil {
				res.DeletedMetaFiles++
				res.DeletedMetaBytes += extSize
			} else if !os.IsNotExist(rmExtErr) {
				res.FailedCount++
				appendBoundedFailure(res, normNS, normKey+".ext", rmExtErr.Error())
			}
		}
	} else if !os.IsNotExist(extErr) {
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey+".ext", extErr.Error())
	}

	shard.mu.Unlock()
	m.persistMu.RUnlock()
}

func (m *Manager) removeEmptyParentDirsControlled(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	expectedGen uint64,
	expectedBase string,
	touchedDirs map[string]struct{},
	cancelCh <-chan struct{},
) (int, bool) {
	return m.removeEmptyParentDirsControlledWithDirInfos(absBase, realBase, expectedRootFi, nil, expectedGen, expectedBase, touchedDirs, cancelCh)
}

func (m *Manager) removeEmptyParentDirsControlledWithDirInfos(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	dirInfos map[string]os.FileInfo,
	expectedGen uint64,
	expectedBase string,
	touchedDirs map[string]struct{},
	cancelCh <-chan struct{},
) (int, bool) {
	hostsRoot := filepath.Join(absBase, "hosts")
	allDirs := make(map[string]struct{}, len(touchedDirs)*2)
	for d := range touchedDirs {
		cur := d
		for {
			rel, err := filepath.Rel(absBase, cur)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "..") {
				break
			}
			if cur == hostsRoot || filepath.Dir(cur) == hostsRoot {
				break
			}
			allDirs[cur] = struct{}{}
			cur = filepath.Dir(cur)
		}
	}
	sortedDirs := make([]string, 0, len(allDirs))
	for d := range allDirs {
		sortedDirs = append(sortedDirs, d)
	}
	// Deepest directories first
	sort.Slice(sortedDirs, func(i, j int) bool {
		return len(sortedDirs[i]) > len(sortedDirs[j])
	})
	removed := 0
	for _, d := range sortedDirs {
		select {
		case <-cancelCh:
			return removed, true
		default:
		}
		if m != nil {
			m.persistMu.Lock()
			if m.generation != expectedGen || (expectedBase != "" && m.GetCacheBase() != expectedBase) {
				m.persistMu.Unlock()
				return removed, true
			}
			removedDir, _, _ := removeVerifiedEmptyDir(absBase, realBase, expectedRootFi, dirInfos, d)
			m.persistMu.Unlock()
			if removedDir {
				removed++
			}
		} else {
			if removedDir, _, _ := removeVerifiedEmptyDir(absBase, realBase, expectedRootFi, dirInfos, d); removedDir {
				removed++
			}
		}
	}
	return removed, false
}

// pruneEmptyDirTreeControlled walks dirRoot bottom-up and removes only genuinely empty directories
// using handle-relative parentRoot.Remove (never os.RemoveAll), verifying cancelCh, generation/root binding,
// and directory identity on every directory before ReadDir and Remove.
func (m *Manager) pruneEmptyDirTreeControlled(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	expectedGen uint64,
	expectedBase string,
	dirRoot string,
	cancelCh <-chan struct{},
) (removedRoot bool, cancelled bool, firstErr error) {
	return m.pruneEmptyDirTreeControlledWithDirInfos(absBase, realBase, expectedRootFi, nil, expectedGen, expectedBase, dirRoot, cancelCh)
}

func (m *Manager) pruneEmptyDirTreeControlledWithDirInfos(
	absBase string,
	realBase string,
	expectedRootFi os.FileInfo,
	baseDirInfos map[string]os.FileInfo,
	expectedGen uint64,
	expectedBase string,
	dirRoot string,
	cancelCh <-chan struct{},
) (removedRoot bool, cancelled bool, firstErr error) {
	dirInfos := make(map[string]os.FileInfo)
	for k, v := range baseDirInfos {
		dirInfos[k] = v
	}
	if expectedRootFi != nil {
		dirInfos[absBase] = expectedRootFi
	}

	if err := verifyStrictDirChainWithDirInfos(absBase, realBase, expectedRootFi, dirInfos, dirRoot); err != nil {
		if os.IsNotExist(err) && verifyStrictRootIdentity(absBase, realBase, expectedRootFi) == nil {
			return true, false, nil
		}
		return false, false, err
	}

	var subDirs []string
	_ = filepath.WalkDir(dirRoot, func(p string, d os.DirEntry, err error) error {
		select {
		case <-cancelCh:
			cancelled = true
			return filepath.SkipAll
		default:
		}
		if m != nil {
			curGen, curBase := m.currentGenAndBase()
			if curGen != expectedGen || (expectedBase != "" && curBase != expectedBase) {
				cancelled = true
				return filepath.SkipAll
			}
		}
		if err != nil {
			if !os.IsNotExist(err) && firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if d.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			if firstErr == nil {
				firstErr = fmt.Errorf("拒绝处理符号链接或重解析点: %s", p)
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if chainErr := verifyStrictDirChainWithDirInfos(absBase, realBase, expectedRootFi, dirInfos, p); chainErr != nil {
				if firstErr == nil {
					firstErr = chainErr
				}
				return filepath.SkipDir
			}
			if _, exists := dirInfos[p]; !exists {
				if dInfo, dErr := d.Info(); dErr == nil && dInfo != nil {
					dirInfos[p] = dInfo
				}
			}
			subDirs = append(subDirs, p)
		}
		return nil
	})
	if cancelled {
		return false, true, firstErr
	}

	sort.Slice(subDirs, func(i, j int) bool {
		return len(subDirs[i]) > len(subDirs[j])
	})
	for _, d := range subDirs {
		select {
		case <-cancelCh:
			return false, true, firstErr
		default:
		}
		var removedDir bool
		var rmErr error
		if m != nil {
			m.persistMu.Lock()
			if m.generation != expectedGen || (expectedBase != "" && m.GetCacheBase() != expectedBase) {
				m.persistMu.Unlock()
				return false, true, firstErr
			}
			removedDir, _, rmErr = removeVerifiedEmptyDir(absBase, realBase, expectedRootFi, dirInfos, d)
			m.persistMu.Unlock()
		} else {
			removedDir, _, rmErr = removeVerifiedEmptyDir(absBase, realBase, expectedRootFi, dirInfos, d)
		}
		if rmErr != nil && !os.IsNotExist(rmErr) {
			if firstErr == nil {
				firstErr = rmErr
			}
			continue
		}
		if d == dirRoot && removedDir {
			removedRoot = true
		}
	}
	return removedRoot, false, firstErr
}

type discoveredStaleVersionDir struct {
	ns       string
	prefix   string
	version  string
	verNum   int
	fullPath string
	display  string
}

// discoverAllVersionDirs discovers all valid positive numeric version directories across
// "gbf" and "hosts/<ns>" for SupportedVersionPrefixes, enforcing containment within realBase.
func discoverAllVersionDirs(absBase, realBase string) []discoveredStaleVersionDir {
	dirs, _ := discoverAllVersionDirsWithErrors(absBase, realBase)
	return dirs
}

func discoverAllVersionDirsWithErrors(absBase, realBase string) ([]discoveredStaleVersionDir, []DeleteFailure) {
	return discoverAllVersionDirsWithErrorsAndDirInfos(absBase, realBase, nil)
}

func discoverAllVersionDirsWithErrorsAndDirInfos(absBase, realBase string, dirInfos map[string]os.FileInfo) ([]discoveredStaleVersionDir, []DeleteFailure) {
	var allDirs []discoveredStaleVersionDir
	var failures []DeleteFailure

	collectForRoot := func(ns, root string) {
		for _, prefix := range SupportedVersionPrefixes {
			prefixDir := filepath.Join(root, prefix)
			lfi, statErr := os.Lstat(prefixDir)
			if statErr != nil {
				if !os.IsNotExist(statErr) {
					failures = append(failures, DeleteFailure{
						NS:     ns,
						Key:    prefix,
						Reason: fmt.Sprintf("读取资源前缀目录失败: %v", statErr),
					})
				}
				continue
			}
			if lfi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !lfi.IsDir() {
				failures = append(failures, DeleteFailure{
					NS:     ns,
					Key:    prefix,
					Reason: fmt.Sprintf("资源前缀路径不是普通目录: %s", prefixDir),
				})
				continue
			}
			if realBase != "" {
				realPrefixDir, evalErr := filepath.EvalSymlinks(prefixDir)
				if evalErr != nil || !isRealPathWithinBase(realBase, realPrefixDir) {
					failures = append(failures, DeleteFailure{
						NS:     ns,
						Key:    prefix,
						Reason: fmt.Sprintf("资源前缀目录安全边界校验失败: %s", prefixDir),
					})
					continue
				}
			}
			if dirInfos != nil {
				if _, exists := dirInfos[prefixDir]; !exists {
					dirInfos[prefixDir] = lfi
				}
			}
			entries, err := os.ReadDir(prefixDir)
			if err != nil {
				if !os.IsNotExist(err) {
					failures = append(failures, DeleteFailure{
						NS:     ns,
						Key:    prefix,
						Reason: fmt.Sprintf("遍历资源前缀目录失败: %v", err),
					})
				}
				continue
			}
			var groupDirs []discoveredStaleVersionDir
			for _, e := range entries {
				vNum, isVerName := parsePositiveVersionSegment(e.Name())
				if !isVerName {
					continue
				}
				fullP := filepath.Join(prefixDir, e.Name())
				disp := filepath.Join(prefix, e.Name())
				if ns != "gbf" {
					disp = filepath.Join("hosts", ns, prefix, e.Name())
				}
				if e.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
					failures = append(failures, DeleteFailure{
						NS:     ns,
						Key:    disp,
						Reason: "版本目录为符号链接或重解析点，已拒绝处理",
					})
					continue
				}
				if !e.IsDir() {
					continue
				}
				if realBase != "" {
					realDir, evalErr := filepath.EvalSymlinks(fullP)
					if evalErr != nil || !isRealPathWithinBase(realBase, realDir) {
						failures = append(failures, DeleteFailure{
							NS:     ns,
							Key:    disp,
							Reason: "版本目录超出缓存根目录安全边界",
						})
						continue
					}
				}
				if dirInfos != nil {
					if _, exists := dirInfos[fullP]; !exists {
						if vFi, vErr := e.Info(); vErr == nil && vFi != nil {
							dirInfos[fullP] = vFi
						}
					}
				}
				groupDirs = append(groupDirs, discoveredStaleVersionDir{
					ns:       ns,
					prefix:   prefix,
					version:  e.Name(),
					verNum:   vNum,
					fullPath: fullP,
					display:  disp,
				})
			}
			sort.Slice(groupDirs, func(i, j int) bool {
				if groupDirs[i].verNum != groupDirs[j].verNum {
					return groupDirs[i].verNum > groupDirs[j].verNum
				}
				return groupDirs[i].version > groupDirs[j].version
			})
			allDirs = append(allDirs, groupDirs...)
		}
	}

	collectForRoot("gbf", absBase)

	hostsDir := filepath.Join(absBase, "hosts")
	if lfi, statErr := os.Lstat(hostsDir); statErr == nil {
		if lfi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !lfi.IsDir() {
			failures = append(failures, DeleteFailure{
				NS:     "hosts",
				Key:    "hosts",
				Reason: fmt.Sprintf("hosts 路径不是普通目录: %s", hostsDir),
			})
		} else if hostEntries, err := os.ReadDir(hostsDir); err != nil {
			if !os.IsNotExist(err) {
				failures = append(failures, DeleteFailure{
					NS:     "hosts",
					Key:    "hosts",
					Reason: fmt.Sprintf("读取 hosts 目录失败: %v", err),
				})
			}
		} else {
			if dirInfos != nil {
				if _, exists := dirInfos[hostsDir]; !exists {
					dirInfos[hostsDir] = lfi
				}
			}
			var hostNames []string
			for _, he := range hostEntries {
				if he.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
					failures = append(failures, DeleteFailure{
						NS:     he.Name(),
						Key:    filepath.Join("hosts", he.Name()),
						Reason: "hosts 子目录为符号链接或重解析点，已拒绝处理",
					})
					continue
				}
				if !he.IsDir() {
					continue
				}
				hostDirPath := filepath.Join(hostsDir, he.Name())
				if realBase != "" {
					realHostDir, evalErr := filepath.EvalSymlinks(hostDirPath)
					if evalErr != nil || !isRealPathWithinBase(realBase, realHostDir) {
						failures = append(failures, DeleteFailure{
							NS:     he.Name(),
							Key:    filepath.Join("hosts", he.Name()),
							Reason: "hosts 子目录超出缓存根目录安全边界",
						})
						continue
					}
				}
				if dirInfos != nil {
					if _, exists := dirInfos[hostDirPath]; !exists {
						if hFi, hErr := he.Info(); hErr == nil && hFi != nil {
							dirInfos[hostDirPath] = hFi
						}
					}
				}
				hostNames = append(hostNames, he.Name())
			}
			sort.Strings(hostNames)
			for _, hn := range hostNames {
				collectForRoot(sanitizeNamespace(hn), filepath.Join(hostsDir, hn))
			}
		}
	} else if !os.IsNotExist(statErr) {
		failures = append(failures, DeleteFailure{
			NS:     "hosts",
			Key:    "hosts",
			Reason: fmt.Sprintf("访问 hosts 目录失败: %v", statErr),
		})
	}

	return allDirs, failures
}

func discoverStaleVersionDirs(absBase, realBase string, keepCount int) []discoveredStaleVersionDir {
	stales, _ := discoverStaleVersionDirsWithErrors(absBase, realBase, keepCount)
	return stales
}

func discoverStaleVersionDirsWithErrors(absBase, realBase string, keepCount int) ([]discoveredStaleVersionDir, []DeleteFailure) {
	return discoverStaleVersionDirsWithErrorsAndDirInfos(absBase, realBase, keepCount, nil)
}

func discoverStaleVersionDirsWithErrorsAndDirInfos(absBase, realBase string, keepCount int, dirInfos map[string]os.FileInfo) ([]discoveredStaleVersionDir, []DeleteFailure) {
	allDirs, failures := discoverAllVersionDirsWithErrorsAndDirInfos(absBase, realBase, dirInfos)
	type groupKey struct {
		ns     string
		prefix string
	}
	var groupOrder []groupKey
	grouped := make(map[groupKey][]discoveredStaleVersionDir)
	for _, d := range allDirs {
		gk := groupKey{ns: d.ns, prefix: d.prefix}
		if _, exists := grouped[gk]; !exists {
			groupOrder = append(groupOrder, gk)
		}
		grouped[gk] = append(grouped[gk], d)
	}
	var allStales []discoveredStaleVersionDir
	for _, gk := range groupOrder {
		vers := grouped[gk]
		if len(vers) > keepCount {
			allStales = append(allStales, vers[keepCount:]...)
		}
	}
	return allStales, failures
}

// PruneStaleVersionsDetailed prunes stale numeric version directories keeping the newest keepCount
// per (namespace, resource_prefix) group. If snapshotID is non-empty, it validates and binds
// the candidate set, generation, and cache root to that snapshot; otherwise it performs live directory discovery.
// In both modes it uses controlled per-file deletion (never blind os.RemoveAll), preserving
// .tmp.*, .quarantine, and concurrently modified files while removing only confirmed empty directories.
func (m *Manager) PruneStaleVersionsDetailed(
	keepCount int,
	snapshotID string,
	progressCb func(p SlimProgress),
	cancelCh <-chan struct{},
) (*DeleteResult, error) {
	if keepCount <= 0 {
		return nil, ErrInvalidKeepCount
	}

	var snap *BreakdownSnapshot
	var startGen uint64
	var base string
	if snapshotID != "" {
		var err error
		snap, err = m.getValidatedSnapshot(snapshotID)
		if err != nil {
			return nil, err
		}
		startGen = snap.Generation
		base = snap.CacheBase
		curGen, curBase := m.currentGenAndBase()
		if curGen != startGen || curBase != base {
			return nil, ErrSnapshotStale
		}
	} else {
		startGen, base = m.currentGenAndBase()
	}

	absBase, realBase, rootInfo, baseExists, baseErr := resolveContainedRealBaseWithInfo(base)
	if snap != nil && baseErr == nil && baseExists && snap.baseExists && snap.rootInfo != nil {
		if !os.SameFile(snap.rootInfo, rootInfo) || (snap.realBase != "" && snap.realBase != realBase) {
			baseErr = fmt.Errorf("缓存根目录物理身份已变更: %s", base)
		}
	}
	if baseErr != nil {
		msg := fmt.Sprintf("缓存根目录校验失败: %v", baseErr)
		res := &DeleteResult{
			TaskType:    "slim",
			SnapshotID:  snapshotID,
			FailedCount: 1,
			Done:        true,
			Status:      "failed",
			Message:     msg,
		}
		appendBoundedFailure(res, "", base, msg)
		if progressCb != nil {
			progressCb(SlimProgress{Done: true})
		}
		return res, nil
	}
	if !baseExists {
		res := &DeleteResult{
			TaskType:   "slim",
			SnapshotID: snapshotID,
			Done:       true,
			Status:     "completed",
			Message:    "缓存目录尚不存在，无需清理",
		}
		if progressCb != nil {
			progressCb(SlimProgress{Done: true})
		}
		return res, nil
	}

	var stales []discoveredStaleVersionDir
	var discoveryFailures []DeleteFailure
	var snapCandidatesByDir map[string][]SnapshotEntry
	var snapOrphanExtsByDir map[string][]orphanExtCandidate
	var dirInfos map[string]os.FileInfo

	if snap != nil {
		dirInfos = snap.dirInfos
		_, discoveryFailures = discoverAllVersionDirsWithErrorsAndDirInfos(absBase, realBase, dirInfos)
		snapCandidatesByDir = make(map[string][]SnapshotEntry)
		snapOrphanExtsByDir = make(map[string][]orphanExtCandidate)
		pruneVerSet := make(map[string]discoveredStaleVersionDir)
		for _, grp := range snap.VersionGroups {
			if len(grp.Versions) <= keepCount {
				continue
			}
			for _, vd := range grp.Versions[keepCount:] {
				var fullP string
				if grp.Namespace == "gbf" {
					fullP = filepath.Join(absBase, grp.ResourcePrefix, vd.Version)
				} else {
					fullP = filepath.Join(absBase, "hosts", grp.Namespace, grp.ResourcePrefix, vd.Version)
				}
				disp := filepath.Join(grp.ResourcePrefix, vd.Version)
				if grp.Namespace != "gbf" {
					disp = filepath.Join("hosts", grp.Namespace, grp.ResourcePrefix, vd.Version)
				}
				d := discoveredStaleVersionDir{
					ns:       grp.Namespace,
					prefix:   grp.ResourcePrefix,
					version:  vd.Version,
					verNum:   vd.VersionNum,
					fullPath: fullP,
					display:  disp,
				}
				stales = append(stales, d)
				pruneVerSet[grp.Namespace+"\x00"+grp.ResourcePrefix+"\x00"+vd.Version] = d
			}
		}
		for _, entry := range snap.Entries {
			if entry.Category != CategoryVersioned {
				continue
			}
			if prefix, verStr, _, ok := ParseVersionedEntry(entry.NS, entry.Key); ok {
				k := entry.NS + "\x00" + prefix + "\x00" + verStr
				if d, shouldPrune := pruneVerSet[k]; shouldPrune {
					snapCandidatesByDir[d.fullPath] = append(snapCandidatesByDir[d.fullPath], entry)
				}
			}
		}
		for slashRel, oext := range snap.orphanExts {
			fullExtPath := filepath.Join(absBase, filepath.FromSlash(slashRel))
			ns := oext.ns
			cleanKey := oext.cleanKey
			if ns == "" || cleanKey == "" {
				var ok bool
				ns, cleanKey, _, ok = extractStrictNamespaceAndKey(absBase, fullExtPath)
				if !ok {
					continue
				}
			}
			if strings.HasSuffix(cleanKey, ".ext") {
				baseKey := strings.TrimSuffix(cleanKey, ".ext")
				if prefix, verStr, _, okVer := ParseVersionedEntry(ns, baseKey); okVer {
					k := ns + "\x00" + prefix + "\x00" + verStr
					if d, shouldPrune := pruneVerSet[k]; shouldPrune {
						cand := oext
						cand.ns = ns
						cand.cleanKey = cleanKey
						cand.fullPath = fullExtPath
						snapOrphanExtsByDir[d.fullPath] = append(snapOrphanExtsByDir[d.fullPath], cand)
					}
				}
			}
		}
	} else {
		dirInfos = make(map[string]os.FileInfo)
		if rootInfo != nil {
			dirInfos[absBase] = rootInfo
		}
		stales, discoveryFailures = discoverStaleVersionDirsWithErrorsAndDirInfos(absBase, realBase, keepCount, dirInfos)
	}

	res := &DeleteResult{
		TaskType:    "slim",
		SnapshotID:  snapshotID,
		FailedCount: len(discoveryFailures),
	}
	for _, df := range discoveryFailures {
		appendBoundedFailure(res, df.NS, df.Key, df.Reason)
	}

	totalDirs := len(stales)
	touchedDirs := make(map[string]struct{})

	for idx, s := range stales {
		select {
		case <-cancelCh:
			res.Cancelled = true
		default:
		}
		if res.Cancelled {
			break
		}
		curGen, curBase := m.currentGenAndBase()
		if curGen != startGen || curBase != base {
			res.Cancelled = true
			res.Message = "版本清理期间缓存根目录已切换，操作已中止"
			break
		}

		if progressCb != nil {
			progressCb(SlimProgress{
				CurrentDir:   s.display,
				CurrentIdx:   idx + 1,
				TotalDirs:    totalDirs,
				DeletedFiles: res.DeletedAssetFiles + res.DeletedMetaFiles,
				FreedBytes:   res.DeletedAssetBytes + res.DeletedMetaBytes,
				Done:         false,
			})
		}

		// Verify version directory and its entire ancestor chain from absBase before walking/deleting
		if chainErr := verifyStrictDirChainWithDirInfos(absBase, realBase, rootInfo, dirInfos, s.fullPath); chainErr != nil {
			if os.IsNotExist(chainErr) && verifyStrictRootIdentity(absBase, realBase, rootInfo) == nil {
				if snap != nil {
					res.AlreadyMissingCount += len(snapCandidatesByDir[s.fullPath])
				}
				continue
			}
			res.FailedCount++
			appendBoundedFailure(res, s.ns, s.display, chainErr.Error())
			res.RemainingDirs++
			continue
		}

		var dirCandidates []SnapshotEntry
		var dirOrphanExts []orphanExtCandidate

		if snap != nil {
			dirCandidates = snapCandidatesByDir[s.fullPath]
			dirOrphanExts = snapOrphanExtsByDir[s.fullPath]
		} else {
			// Record verified ancestor dir FileInfos from absBase to s.fullPath
			if relToVer, rErr := filepath.Rel(absBase, s.fullPath); rErr == nil {
				curAnc := absBase
				for _, seg := range strings.Split(relToVer, string(filepath.Separator)) {
					if seg == "" || seg == "." {
						continue
					}
					curAnc = filepath.Join(curAnc, seg)
					if _, exists := dirInfos[curAnc]; !exists {
						if afi, aErr := os.Lstat(curAnc); aErr == nil && afi != nil && afi.IsDir() {
							dirInfos[curAnc] = afi
						}
					}
				}
			}
			// Live discovery of managed files within s.fullPath without holding locks
			pairedExts := make(map[string]struct{})
			foundExtsByPath := make(map[string]orphanExtCandidate)
			var foundExts []orphanExtCandidate
			var candPaths []string
			_ = filepath.WalkDir(s.fullPath, func(p string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					res.FailedCount++
					appendBoundedFailure(res, s.ns, s.display, walkErr.Error())
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 || d.Type()&os.ModeIrregular != 0 {
					res.FailedCount++
					appendBoundedFailure(res, s.ns, p, "拒绝处理符号链接或重解析点")
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					if chainErr := verifyStrictDirChainWithDirInfos(absBase, realBase, rootInfo, dirInfos, p); chainErr != nil {
						res.FailedCount++
						appendBoundedFailure(res, s.ns, p, chainErr.Error())
						return filepath.SkipDir
					}
					if _, exists := dirInfos[p]; !exists {
						if dInfo, dErr := d.Info(); dErr == nil && dInfo != nil {
							dirInfos[p] = dInfo
						}
					}
					return nil
				}
				if strings.Contains(p, ".tmp.") || strings.Contains(p, ".quarantine") {
					return nil
				}
				info, infoErr := d.Info()
				if infoErr != nil {
					res.FailedCount++
					appendBoundedFailure(res, s.ns, p, infoErr.Error())
					return nil
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				ns, cleanKey, slashRel, ok := extractStrictNamespaceAndKey(absBase, p)
				if !ok {
					return nil
				}
				if strings.HasSuffix(cleanKey, ".ext") {
					extCand := orphanExtCandidate{
						ns:        ns,
						cleanKey:  cleanKey,
						slashRel:  slashRel,
						fullPath:  p,
						size:      info.Size(),
						mtimeNano: info.ModTime().UnixNano(),
						fileInfo:  info,
					}
					foundExts = append(foundExts, extCand)
					foundExtsByPath[p] = extCand
					return nil
				}
				pairedExts[p+".ext"] = struct{}{}
				candPaths = append(candPaths, p)
				dirCandidates = append(dirCandidates, SnapshotEntry{
					NS:        ns,
					Key:       cleanKey,
					Category:  CategoryVersioned,
					Size:      info.Size(),
					MTimeNano: info.ModTime().UnixNano(),
					fileInfo:  info,
				})
				return nil
			})
			for i := range dirCandidates {
				if extCand, ok := foundExtsByPath[candPaths[i]+".ext"]; ok {
					dirCandidates[i].HasMeta = true
					dirCandidates[i].MetaBytes = extCand.size
					dirCandidates[i].extMTimeNano = extCand.mtimeNano
					dirCandidates[i].extFileInfo = extCand.fileInfo
				}
			}
			for _, ext := range foundExts {
				if _, paired := pairedExts[ext.fullPath]; !paired {
					dirOrphanExts = append(dirOrphanExts, ext)
				}
			}
		}

		res.CandidateFiles += len(dirCandidates)
		for _, cand := range dirCandidates {
			select {
			case <-cancelCh:
				res.Cancelled = true
			default:
			}
			if res.Cancelled {
				break
			}
			m.deleteSingleVerifiedEntryWithDirInfos(absBase, realBase, rootInfo, dirInfos, startGen, base, cand, res, touchedDirs)
		}

		for _, oext := range dirOrphanExts {
			select {
			case <-cancelCh:
				res.Cancelled = true
			default:
			}
			if res.Cancelled {
				break
			}

			m.persistMu.RLock()
			if m.generation != startGen || m.GetCacheBase() != base {
				m.persistMu.RUnlock()
				res.Cancelled = true
				res.Message = "版本清理期间缓存根目录已切换，操作已中止"
				break
			}

			ns := oext.ns
			cleanKey := oext.cleanKey
			if ns == "" || cleanKey == "" {
				var ok bool
				ns, cleanKey, _, ok = extractStrictNamespaceAndKey(absBase, oext.fullPath)
				if !ok {
					m.persistMu.RUnlock()
					res.FailedCount++
					appendBoundedFailure(res, s.ns, oext.fullPath, "无效的孤立元数据路径")
					continue
				}
			}
			baseKey := strings.TrimSuffix(cleanKey, ".ext")

			// Synchronize with Save / CommitDiskTempFile via per-key tokenShard mutex
			shard, _ := m.tokenShardFor(ns, baseKey)
			shard.mu.Lock()

			parentRoot, extLeaf, closeParent, chainErr := openPinnedParentRoot(absBase, realBase, rootInfo, dirInfos, oext.fullPath)
			if chainErr != nil {
				shard.mu.Unlock()
				m.persistMu.RUnlock()
				if os.IsNotExist(chainErr) && verifyStrictRootIdentity(absBase, realBase, rootInfo) == nil {
					res.AlreadyMissingCount++
				} else {
					res.FailedCount++
					appendBoundedFailure(res, s.ns, oext.fullPath, chainErr.Error())
				}
				continue
			}

			// Verify that a paired data file did not appear after scan/discovery on the pinned parent handle
			dataLeaf := strings.TrimSuffix(extLeaf, ".ext")
			if _, dataErr := parentRoot.Lstat(dataLeaf); dataErr == nil || !os.IsNotExist(dataErr) {
				closeParent()
				shard.mu.Unlock()
				m.persistMu.RUnlock()
				res.SkippedChangedCount++
				continue
			}

			// Verify orphan .ext file identity (regular file + size + mtimeNano + os.SameFile)
			extFi, extStatErr := parentRoot.Lstat(extLeaf)
			if extStatErr != nil {
				closeParent()
				shard.mu.Unlock()
				m.persistMu.RUnlock()
				if os.IsNotExist(extStatErr) {
					res.AlreadyMissingCount++
				} else {
					res.FailedCount++
					appendBoundedFailure(res, s.ns, oext.fullPath, extStatErr.Error())
				}
				continue
			}
			if extFi.IsDir() || extFi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || !extFi.Mode().IsRegular() {
				closeParent()
				shard.mu.Unlock()
				m.persistMu.RUnlock()
				res.FailedCount++
				appendBoundedFailure(res, s.ns, oext.fullPath, "目标 .ext 不是普通文件")
				continue
			}
			if extFi.Size() != oext.size || extFi.ModTime().UnixNano() != oext.mtimeNano || (oext.fileInfo != nil && !os.SameFile(oext.fileInfo, extFi)) {
				closeParent()
				shard.mu.Unlock()
				m.persistMu.RUnlock()
				res.SkippedChangedCount++
				continue
			}

			if testBeforeUnlinkHook != nil {
				testBeforeUnlinkHook(oext.fullPath)
			}

			if rmErr := parentRoot.Remove(extLeaf); rmErr == nil {
				res.DeletedMetaFiles++
				res.DeletedMetaBytes += oext.size
			} else if os.IsNotExist(rmErr) {
				res.AlreadyMissingCount++
			} else {
				res.FailedCount++
				appendBoundedFailure(res, s.ns, oext.fullPath, rmErr.Error())
			}
			closeParent()
			shard.mu.Unlock()
			m.persistMu.RUnlock()
		}

		if !res.Cancelled {
			removedRoot, dirCancelled, pruneDirErr := m.pruneEmptyDirTreeControlledWithDirInfos(absBase, realBase, rootInfo, dirInfos, startGen, base, s.fullPath, cancelCh)
			if dirCancelled {
				res.Cancelled = true
				break
			}
			if pruneDirErr != nil {
				res.FailedCount++
				appendBoundedFailure(res, s.ns, s.display, fmt.Sprintf("移除空版本目录失败: %v", pruneDirErr))
			}
			if removedRoot {
				res.DeletedDirs++
			} else if _, err := os.Lstat(s.fullPath); err == nil {
				res.RemainingDirs++
			}
		}
	}

	if m.ramBudget != nil && m.residentPool != nil && m.residentPool.Enabled() {
		m.ramBudget.SyncSLRU(m.ramCache)
	}

	res.DeletedFiles = res.DeletedAssetFiles + res.DeletedMetaFiles
	res.FreedBytes = res.DeletedAssetBytes + res.DeletedMetaBytes
	res.FreedMB = math.Round(float64(res.FreedBytes)/(1024*1024)*100) / 100
	res.Done = true

	if res.DeletedFiles > 0 || res.DeletedDirs > 0 || res.AlreadyMissingCount > 0 || res.SkippedChangedCount > 0 {
		m.invalidateSnapshots("已执行历史版本清理，请重新扫描以刷新统计")
	}

	switch {
	case res.Cancelled:
		res.Status = "cancelled"
		if res.Message == "" {
			res.Message = fmt.Sprintf("版本清理已取消（已清理 %d 个目录，%d 个文件）", res.DeletedDirs, res.DeletedFiles)
		}
	case res.FailedCount > 0 && res.DeletedFiles == 0 && res.DeletedDirs == 0:
		res.Status = "failed"
		res.Message = fmt.Sprintf("版本清理失败（%d 项失败，%d 个目录保留）", res.FailedCount, res.RemainingDirs)
	case res.FailedCount > 0:
		res.Status = "partial_failed"
		res.Message = fmt.Sprintf("部分完成：已清理 %d 个版本目录，%d 项删除失败（%d 个目录保留），释放 %.2f MiB", res.DeletedDirs, res.FailedCount, res.RemainingDirs, res.FreedMB)
	case res.RemainingDirs > 0:
		res.Status = "completed"
		res.Message = fmt.Sprintf("已清理 %d 个版本目录（%d 个目录因包含未删/在途文件保留），释放 %.2f MiB", res.DeletedDirs, res.RemainingDirs, res.FreedMB)
	default:
		res.Status = "completed"
		res.Message = fmt.Sprintf("已清理 %d 个历史版本目录，释放 %.2f MiB", res.DeletedDirs, res.FreedMB)
	}

	if progressCb != nil {
		progressCb(SlimProgress{
			CurrentDir:   "",
			CurrentIdx:   totalDirs,
			TotalDirs:    totalDirs,
			DeletedFiles: res.DeletedFiles,
			FreedBytes:   res.FreedBytes,
			Done:         true,
		})
	}

	return res, nil
}
