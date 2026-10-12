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

func splitCleanSegments(cleanKey string) []string {
	norm := strings.ReplaceAll(strings.TrimSpace(cleanKey), "\\", "/")
	norm, _, _ = strings.Cut(norm, "?")
	norm = strings.Trim(norm, "/")
	if norm == "" {
		return nil
	}
	raw := strings.Split(norm, "/")
	segs := make([]string, 0, len(raw))
	for _, s := range raw {
		if s == "" || s == "." {
			continue
		}
		if s == ".." {
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

func canonicalTokenKey(ns, cleanKey string) string {
	segs := splitCleanSegments(cleanKey)
	normKey := strings.Join(segs, "/")
	normNS := sanitizeNamespace(ns)
	if normNS == "gbf" {
		if strings.HasPrefix(normKey, "assets/") {
			rem := strings.TrimPrefix(normKey, "assets/")
			if rem != "" && !strings.HasPrefix(rem, "assets") {
				normKey = rem
			}
		}
		return "gbf:" + normKey
	}
	return normNS + ":" + normKey
}

func gbfFallbackCleanKey(cleanKey string) (string, bool) {
	segs := splitCleanSegments(cleanKey)
	if len(segs) == 0 {
		return "", false
	}
	normKey := strings.Join(segs, "/")
	if !strings.HasPrefix(normKey, "assets") {
		return "assets/" + normKey, true
	}
	if strings.HasPrefix(normKey, "assets/") {
		rem := strings.TrimPrefix(normKey, "assets/")
		if rem != "" && !strings.HasPrefix(rem, "assets") {
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

func (m *Manager) clearKeyMemoryState(ns, cleanKey string) {
	normNS := sanitizeNamespace(ns)
	ramKey := makeRAMKey(normNS, cleanKey)
	m.deleteDiskMeta(ramKey)
	if m.residentPool != nil {
		m.residentPool.Delete(ramKey)
	}
	if m.ramCache != nil {
		m.ramCache.Delete(ramKey)
	}
	m.deleteMissingKey(ramKey)

	if normNS == "gbf" {
		if altKey, ok := gbfFallbackCleanKey(cleanKey); ok {
			altRamKey := makeRAMKey("gbf", altKey)
			m.deleteDiskMeta(altRamKey)
			if m.residentPool != nil {
				m.residentPool.Delete(altRamKey)
			}
			if m.ramCache != nil {
				m.ramCache.Delete(altRamKey)
			}
			m.deleteMissingKey(altRamKey)
		}
	}
}

func (m *Manager) deleteMissingKey(ramKey string) {
	shard := m.missingShard(ramKey)
	shard.mu.Lock()
	delete(shard.items, ramKey)
	shard.mu.Unlock()
}

func (m *Manager) registerTempFileToken(tmpPath string, tok opToken) {
	m.tempTokensMu.Lock()
	if m.tempTokens == nil {
		m.tempTokens = make(map[string]opToken)
	}
	if len(m.tempTokens) >= 1024 {
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
	m.tempTokensMu.Unlock()
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

func (m *Manager) clearTempTokens() {
	m.tempTokensMu.Lock()
	m.tempTokens = make(map[string]opToken)
	m.tempTokensMu.Unlock()
}

// --- Symlink / Junction Containment Verification (Section 5.4) ---

func resolveContainedRealBase(baseDir string) (absBase string, realBase string, exists bool, err error) {
	absBase, err = filepath.Abs(baseDir)
	if err != nil {
		return "", "", false, err
	}
	fi, statErr := os.Stat(absBase)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return absBase, absBase, false, nil
		}
		return absBase, "", false, statErr
	}
	if !fi.IsDir() {
		return absBase, "", false, fmt.Errorf("cache base is not a directory: %s", absBase)
	}
	realBase, err = filepath.EvalSymlinks(absBase)
	if err != nil {
		return absBase, "", true, err
	}
	return absBase, realBase, true, nil
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

func verifyTargetContainment(absBase, realBase, targetPath string) error {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return err
	}
	lexRel, err := filepath.Rel(absBase, absTarget)
	if err != nil || lexRel == "." || lexRel == ".." || strings.HasPrefix(lexRel, ".."+string(filepath.Separator)) || filepath.IsAbs(lexRel) {
		return fmt.Errorf("path escapes cache base: %s", targetPath)
	}

	lfi, err := os.Lstat(absTarget)
	if err != nil {
		if os.IsNotExist(err) {
			// Verify parent directory containment if file is already absent
			parentDir := filepath.Dir(absTarget)
			if realParent, pErr := filepath.EvalSymlinks(parentDir); pErr == nil {
				if !isRealPathWithinBase(realBase, realParent) {
					return fmt.Errorf("parent directory redirects outside cache base: %s", targetPath)
				}
			}
			return nil
		}
		return err
	}
	if lfi.Mode()&os.ModeSymlink != 0 || lfi.Mode()&os.ModeIrregular != 0 {
		return fmt.Errorf("refusing symlink or reparse target: %s", targetPath)
	}
	realTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return err
	}
	if !isRealPathWithinBase(realBase, realTarget) {
		return fmt.Errorf("resolved path escapes cache base: %s", targetPath)
	}
	if realTarget == realBase {
		return fmt.Errorf("resolved path equals cache root: %s", targetPath)
	}
	return nil
}

func validateManagedNamespaceAndKey(ns, cleanKey string) (string, string, bool) {
	rawNS := strings.TrimSpace(ns)
	if rawNS == "" {
		rawNS = "gbf"
	}
	if strings.Contains(rawNS, "..") || strings.ContainsAny(rawNS, `/\:`) || rawNS == "hosts" {
		return "", "", false
	}
	normNS := sanitizeNamespace(rawNS)
	if normNS != strings.ToLower(rawNS) {
		return "", "", false
	}

	if strings.TrimSpace(cleanKey) == "" {
		return "", "", false
	}
	if strings.Contains(cleanKey, "?") {
		return "", "", false
	}
	trimmed := strings.TrimSpace(cleanKey)
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
	NS        string `json:"ns"`
	Key       string `json:"key"`
	Category  string `json:"category"`
	Size      int64  `json:"asset_bytes"`
	MetaBytes int64  `json:"metadata_bytes"`
	HasMeta   bool   `json:"has_metadata"`
	MTimeNano int64  `json:"mtime_nano"`
	MTimeMS   int64  `json:"mtime_ms"`
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
	orphanExtPaths       map[string]int64
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
		m.lastSnapshot.Stale = true
		m.lastSnapshot.Valid = false
		if reason != "" {
			m.lastSnapshot.StaleReason = reason
		}
	}
	m.snapshotMu.Unlock()
}

// GetBreakdownSnapshot returns a copy of the latest completed snapshot (or active scan metadata)
// with up-to-date validity flags.
func (m *Manager) GetBreakdownSnapshot(customKeep int) (*BreakdownSnapshot, *BreakdownSnapshot) {
	curGen := m.currentGeneration()
	curBase := m.GetCacheBase()

	m.snapshotMu.RLock()
	defer m.snapshotMu.RUnlock()

	var latest *BreakdownSnapshot
	if m.lastSnapshot != nil {
		cp := *m.lastSnapshot
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

	var active *BreakdownSnapshot
	if m.activeScanSnapshot != nil {
		cpActive := *m.activeScanSnapshot
		active = &cpActive
	}
	return latest, active
}

// ScanBreakdownWithProgress performs a single-worker read-only directory walk to build a BreakdownSnapshot.
// It strictly reads directory entries and file attributes without reading file bodies, parsing .ext files,
// populating RAM/ResidentPool/DiskMeta, or issuing upstream requests.
func (m *Manager) ScanBreakdownWithProgress(progressCb func(p ScanProgress), cancelCh <-chan struct{}) *BreakdownSnapshot {
	startGen := m.currentGeneration()
	base := m.GetCacheBase()
	startTime := time.Now()
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

	defer func() {
		m.snapshotMu.Lock()
		if m.activeScanSnapshot != nil && m.activeScanSnapshot.SnapshotID == snapID {
			m.activeScanSnapshot = nil
		}
		m.snapshotMu.Unlock()
	}()

	absBase, realBase, baseExists, err := resolveContainedRealBase(base)
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
	}
	type rawExtFile struct {
		slashRel string
		size     int64
		paired   bool
	}

	var rawFiles []rawDataFile
	extMap := make(map[string]*rawExtFile)
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
			if m.currentGeneration() != startGen || m.GetCacheBase() != base {
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
				realDir, evalErr := filepath.EvalSymlinks(p)
				if evalErr != nil || !isRealPathWithinBase(realBase, realDir) {
					skippedCount++
					return filepath.SkipDir
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
					slashRel: slashRel,
					size:     info.Size(),
				}
			} else {
				rawFiles = append(rawFiles, rawDataFile{
					ns:        ns,
					key:       cleanKey,
					slashRel:  slashRel,
					size:      info.Size(),
					mtimeNano: info.ModTime().UnixNano(),
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

	if cancelled || rootChanged || m.currentGeneration() != startGen || m.GetCacheBase() != base {
		now := time.Now()
		status := "cancelled"
		msg := "扫描任务已取消"
		if rootChanged || m.currentGeneration() != startGen || m.GetCacheBase() != base {
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

	entries := make([]SnapshotEntry, 0, len(rawFiles))
	entryByNSKey := make(map[string]int, len(rawFiles))

	var totalAssetFiles int
	var totalAssetBytes int64
	var totalMetaFiles int
	var totalMetaBytes int64

	for _, rf := range rawFiles {
		var metaBytes int64
		hasMeta := false
		if ext, ok := extMap[rf.slashRel]; ok {
			ext.paired = true
			hasMeta = true
			metaBytes = ext.size
			totalMetaFiles++
			totalMetaBytes += ext.size
		}

		cat := ClassifyCacheEntry(rf.ns, rf.key)
		entryIdx := len(entries)
		entries = append(entries, SnapshotEntry{
			NS:        rf.ns,
			Key:       rf.key,
			Category:  cat,
			Size:      rf.size,
			MetaBytes: metaBytes,
			HasMeta:   hasMeta,
			MTimeNano: rf.mtimeNano,
			MTimeMS:   rf.mtimeNano / 1e6,
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

	// Collect orphan .ext files
	var orphanExtFiles int
	var orphanExtBytes int64
	orphanExtPaths := make(map[string]int64)
	for _, ext := range extMap {
		if !ext.paired {
			orphanExtFiles++
			orphanExtBytes += ext.size
			orphanExtPaths[ext.slashRel] = ext.size
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
			return grp.Versions[i].VersionNum > grp.Versions[j].VersionNum
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
		SnapshotID:           snapID,
		Generation:           startGen,
		CacheBase:            base,
		Status:               "completed",
		Valid:                true,
		Stale:                false,
		StartedAtMS:          startTime.UnixMilli(),
		CompletedAtMS:        now.UnixMilli(),
		DurationMS:           now.Sub(startTime).Milliseconds(),
		AssetFiles:           totalAssetFiles,
		AssetBytes:           totalAssetBytes,
		MetadataFiles:        totalMetaFiles,
		MetadataBytes:        totalMetaBytes,
		OrphanMetadataFiles:  orphanExtFiles,
		OrphanMetadataBytes:  orphanExtBytes,
		ExcludedFiles:        excludedFiles,
		ExcludedBytes:        excludedBytes,
		SkippedCount:         skippedCount,
		ReadErrors:           readErrors,
		Categories:           catStats,
		Namespaces:           namespaces,
		VersionGroups:        versionGroups,
		RetentionPreviews:    retentionPreviews,
		Entries:              entries,
		entryByNSKey:         entryByNSKey,
		orphanExtPaths:       orphanExtPaths,
	}

	m.snapshotMu.Lock()
	if m.currentGeneration() == startGen && m.GetCacheBase() == base {
		m.lastSnapshot = completed
	} else {
		completed.Stale = true
		completed.Valid = false
		completed.StaleReason = "扫描完成时缓存根目录或代数已变更"
	}
	m.snapshotMu.Unlock()

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
	curGen := m.currentGeneration()
	curBase := m.GetCacheBase()

	m.snapshotMu.RLock()
	snap := m.lastSnapshot
	m.snapshotMu.RUnlock()

	if snap == nil || snap.Status != "completed" {
		return nil, ErrSnapshotNotFound
	}
	if snapshotID != "" && snap.SnapshotID != snapshotID {
		return nil, ErrSnapshotStale
	}
	if snap.Stale || snap.Generation != curGen || snap.CacheBase != curBase {
		return nil, ErrSnapshotStale
	}
	return snap, nil
}

// --- Search over Snapshot Index (Section 4.3) ---

// SearchSnapshot searches the latest valid breakdown snapshot by full HTTP/HTTPS URL,
// relative resource path, or filename/path fragment (plain substring match, no regex).
func (m *Manager) SearchSnapshot(snapshotID, query string, limit int, hostNormalizer func(host string) (string, bool)) (*SearchResponse, error) {
	trimmed := strings.TrimSpace(query)
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
		}
	}

	if !isURLQuery {
		noQuery, _, _ := strings.Cut(trimmed, "?")
		norm := strings.TrimPrefix(strings.ReplaceAll(strings.TrimSpace(noQuery), "\\", "/"), "/")
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
		if _, ok := catSet[entry.Category]; !ok {
			continue
		}
		nsKey := makeNSKey(entry.NS, entry.Key)
		if _, dup := seen[nsKey]; !dup {
			seen[nsKey] = struct{}{}
			candidates = append(candidates, entry)
		}
		// Include GBF fallback alias if present in snapshot
		if entry.NS == "gbf" {
			if altKey, hasAlt := gbfFallbackCleanKey(entry.Key); hasAlt {
				altNSKey := makeNSKey("gbf", altKey)
				if altIdx, altExists := snap.entryByNSKey[altNSKey]; altExists {
					if _, dup := seen[altNSKey]; !dup {
						seen[altNSKey] = struct{}{}
						candidates = append(candidates, snap.Entries[altIdx])
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

	absBase, realBase, baseExists, baseErr := resolveContainedRealBase(snap.CacheBase)
	if baseErr != nil || !baseExists {
		if baseErr != nil {
			res.FailedCount += len(candidates)
			res.Status = "failed"
			res.Done = true
			res.Message = fmt.Sprintf("缓存根目录校验失败: %v", baseErr)
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
		if m.currentGeneration() != snap.Generation || m.GetCacheBase() != snap.CacheBase {
			res.Cancelled = true
			res.Message = "清理期间缓存根目录已切换，操作已中止"
			break
		}

		m.deleteSingleVerifiedEntry(absBase, realBase, snap.Generation, cand, &res, touchedDirs)

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
	if len(touchedDirs) > 0 && absBase != "" {
		res.DeletedDirs += removeEmptyParentDirs(absBase, touchedDirs)
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

	filePath, ok := m.resolvePathWithNamespace(normNS, normKey)
	if !ok {
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey, "无法解析缓存文件路径")
		return
	}

	if err := verifyTargetContainment(absBase, realBase, filePath); err != nil {
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey, fmt.Sprintf("路径边界校验未通过: %v", err))
		return
	}

	extPath := filePath + ".ext"
	if err := verifyTargetContainment(absBase, realBase, extPath); err != nil {
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey, fmt.Sprintf("元数据路径边界校验未通过: %v", err))
		return
	}

	m.persistMu.RLock()
	if m.generation != expectedGen {
		m.persistMu.RUnlock()
		res.Cancelled = true
		return
	}

	shard, canonKey := m.tokenShardFor(normNS, normKey)
	shard.mu.Lock()

	// 1. Advance per-key invalidation token and clear all in-memory representations
	bumpInvalidateTokenLocked(shard, canonKey)
	m.clearKeyMemoryState(normNS, normKey)

	// 2. Verify physical file identity (size + mtime) inside the synchronized boundary
	lfi, statErr := os.Lstat(filePath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			res.AlreadyMissingCount++
			// Clean up paired .ext if it still lingers after data file disappeared
			if extFi, extErr := os.Lstat(extPath); extErr == nil && extFi.Mode().IsRegular() {
				extSize := extFi.Size()
				if rmErr := os.Remove(extPath); rmErr == nil {
					res.DeletedMetaFiles++
					res.DeletedMetaBytes += extSize
					touchedDirs[filepath.Dir(filePath)] = struct{}{}
				}
			}
		} else {
			res.FailedCount++
			appendBoundedFailure(res, normNS, normKey, statErr.Error())
		}
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	if lfi.IsDir() || lfi.Mode()&os.ModeSymlink != 0 || !lfi.Mode().IsRegular() {
		res.FailedCount++
		appendBoundedFailure(res, normNS, normKey, "目标不是普通缓存文件")
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	if lfi.Size() != cand.Size || lfi.ModTime().UnixNano() != cand.MTimeNano {
		// File was modified after the snapshot was taken: skip deleting the disk file
		res.SkippedChangedCount++
		shard.mu.Unlock()
		m.persistMu.RUnlock()
		return
	}

	// 3. Delete verified data file
	if rmErr := os.Remove(filePath); rmErr != nil {
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

	// 4. Delete paired .ext metadata file if present
	if extFi, extErr := os.Lstat(extPath); extErr == nil && extFi.Mode().IsRegular() {
		extSize := extFi.Size()
		if rmExtErr := os.Remove(extPath); rmExtErr == nil {
			res.DeletedMetaFiles++
			res.DeletedMetaBytes += extSize
		} else if !os.IsNotExist(rmExtErr) {
			res.FailedCount++
			appendBoundedFailure(res, normNS, normKey+".ext", rmExtErr.Error())
		}
	}

	shard.mu.Unlock()
	m.persistMu.RUnlock()
}

func removeEmptyParentDirs(absBase string, touchedDirs map[string]struct{}) int {
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
		if err := os.Remove(d); err == nil {
			removed++
		}
	}
	return removed
}

// pruneEmptyDirTree walks dirRoot bottom-up and removes only genuinely empty directories
// using os.Remove (never os.RemoveAll). Returns true if dirRoot itself was removed.
func pruneEmptyDirTree(dirRoot string) bool {
	var subDirs []string
	_ = filepath.WalkDir(dirRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Type()&os.ModeSymlink == 0 {
			subDirs = append(subDirs, p)
		}
		return nil
	})
	sort.Slice(subDirs, func(i, j int) bool {
		return len(subDirs[i]) > len(subDirs[j])
	})
	for _, d := range subDirs {
		_ = os.Remove(d)
	}
	_, err := os.Stat(dirRoot)
	return os.IsNotExist(err)
}

type discoveredStaleVersionDir struct {
	ns       string
	prefix   string
	version  string
	verNum   int
	fullPath string
	display  string
}

func discoverStaleVersionDirs(base string, keepCount int) []discoveredStaleVersionDir {
	var allStales []discoveredStaleVersionDir

	collectForRoot := func(ns, root string) {
		for _, prefix := range SupportedVersionPrefixes {
			prefixDir := filepath.Join(root, prefix)
			entries, err := os.ReadDir(prefixDir)
			if err != nil {
				continue
			}
			type verItem struct {
				name string
				num  int
			}
			var versions []verItem
			for _, e := range entries {
				if e.IsDir() && e.Type()&os.ModeSymlink == 0 {
					if vNum, ok := parsePositiveVersionSegment(e.Name()); ok {
						versions = append(versions, verItem{name: e.Name(), num: vNum})
					}
				}
			}
			sort.Slice(versions, func(i, j int) bool {
				return versions[i].num > versions[j].num
			})
			if len(versions) > keepCount {
				for _, v := range versions[keepCount:] {
					disp := filepath.Join(prefix, v.name)
					if ns != "gbf" {
						disp = filepath.Join("hosts", ns, prefix, v.name)
					}
					allStales = append(allStales, discoveredStaleVersionDir{
						ns:       ns,
						prefix:   prefix,
						version:  v.name,
						verNum:   v.num,
						fullPath: filepath.Join(prefixDir, v.name),
						display:  disp,
					})
				}
			}
		}
	}

	collectForRoot("gbf", base)

	hostsDir := filepath.Join(base, "hosts")
	if hostEntries, err := os.ReadDir(hostsDir); err == nil {
		var hostNames []string
		for _, he := range hostEntries {
			if he.IsDir() && he.Type()&os.ModeSymlink == 0 {
				hostNames = append(hostNames, he.Name())
			}
		}
		sort.Strings(hostNames)
		for _, hn := range hostNames {
			collectForRoot(sanitizeNamespace(hn), filepath.Join(hostsDir, hn))
		}
	}
	return allStales
}

// PruneStaleVersionsDetailed prunes stale numeric version directories keeping the newest keepCount
// per (namespace, resource_prefix) group. If snapshotID is non-empty, it validates and binds
// the candidate set to that snapshot; otherwise it performs live directory discovery.
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
	if snapshotID != "" {
		var err error
		snap, err = m.getValidatedSnapshot(snapshotID)
		if err != nil {
			return nil, err
		}
	}

	base := m.GetCacheBase()
	startGen := m.currentGeneration()
	absBase, realBase, baseExists, baseErr := resolveContainedRealBase(base)
	if baseErr != nil || !baseExists {
		res := &DeleteResult{
			TaskType:   "slim",
			SnapshotID: snapshotID,
			Done:       true,
			Status:     "completed",
		}
		if progressCb != nil {
			progressCb(SlimProgress{Done: true})
		}
		return res, nil
	}

	var stales []discoveredStaleVersionDir
	var snapCandidatesByDir map[string][]SnapshotEntry

	if snap != nil {
		snapCandidatesByDir = make(map[string][]SnapshotEntry)
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
	} else {
		stales = discoverStaleVersionDirs(absBase, keepCount)
	}

	res := &DeleteResult{
		TaskType:   "slim",
		SnapshotID: snapshotID,
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
		if m.currentGeneration() != startGen || m.GetCacheBase() != base {
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

		var dirCandidates []SnapshotEntry
		var dirOrphanExts []struct {
			path string
			size int64
		}

		if snap != nil {
			dirCandidates = snapCandidatesByDir[s.fullPath]
		} else {
			// Live discovery of managed files within s.fullPath without holding locks
			pairedExts := make(map[string]struct{})
			var foundExts []struct {
				path string
				size int64
			}
			_ = filepath.WalkDir(s.fullPath, func(p string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					res.FailedCount++
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 || d.Type()&os.ModeIrregular != 0 {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					return nil
				}
				if strings.Contains(p, ".tmp.") || strings.Contains(p, ".quarantine") {
					return nil
				}
				info, infoErr := d.Info()
				if infoErr != nil || !info.Mode().IsRegular() {
					return nil
				}
				ns, cleanKey, _, ok := extractStrictNamespaceAndKey(absBase, p)
				if !ok {
					return nil
				}
				if strings.HasSuffix(cleanKey, ".ext") {
					foundExts = append(foundExts, struct {
						path string
						size int64
					}{path: p, size: info.Size()})
					return nil
				}
				pairedExts[p+".ext"] = struct{}{}
				dirCandidates = append(dirCandidates, SnapshotEntry{
					NS:        ns,
					Key:       cleanKey,
					Category:  CategoryVersioned,
					Size:      info.Size(),
					MTimeNano: info.ModTime().UnixNano(),
				})
				return nil
			})
			for _, ext := range foundExts {
				if _, paired := pairedExts[ext.path]; !paired {
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
			m.deleteSingleVerifiedEntry(absBase, realBase, startGen, cand, res, touchedDirs)
		}

		for _, oext := range dirOrphanExts {
			if verifyTargetContainment(absBase, realBase, oext.path) == nil {
				if rmErr := os.Remove(oext.path); rmErr == nil {
					res.DeletedMetaFiles++
					res.DeletedMetaBytes += oext.size
				}
			}
		}

		if !res.Cancelled {
			if pruneEmptyDirTree(s.fullPath) {
				res.DeletedDirs++
			} else if _, err := os.Stat(s.fullPath); err == nil {
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

	if res.DeletedFiles > 0 || res.DeletedDirs > 0 || res.AlreadyMissingCount > 0 {
		m.invalidateSnapshots("已执行历史版本清理，请重新扫描以刷新统计")
	}

	switch {
	case res.Cancelled:
		res.Status = "cancelled"
		res.Message = fmt.Sprintf("版本清理已取消（已清理 %d 个目录，%d 个文件）", res.DeletedDirs, res.DeletedFiles)
	case res.FailedCount > 0 || res.RemainingDirs > 0:
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
