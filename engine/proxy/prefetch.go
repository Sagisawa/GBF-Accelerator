package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gbf-proxy/cache"
)

var (
	assetRefRe     = regexp.MustCompile(`['"(](?:https?://([^'"()/\s]+))?/?((?:assets(?:_(?:en|jp))?|img|sound|css|js|font)/[A-Za-z0-9_\-./%]+\.(?:png|jpe?g|gif|webp|mp3|wav|ogg|m4a|mp4|webm|js|json|css|woff2?|ttf|otf|svg))`)
	cjsImgRefRe    = regexp.MustCompile(`['"(]/?((?:sp|assets(?:_(?:en|jp))?/img/sp)/[A-Za-z0-9_\-./%]+\.(?:png|jpe?g|gif|webp))['")\s]`)
	twinManifestRe = regexp.MustCompile(`^/(assets(?:_(?:en|jp))?/\d+/js/)model/manifest/([^/]+\.js)$`)
)

// prefetchUserAgent is a single, stable, version-agnostic browser UA used for
// background asset warmup requests. It is deliberately constant (not rotated) to
// comply with the P2 restraint principle: no UA rotation, no fingerprint spoofing.
const prefetchUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome Safari"
const defaultGBFHost = "prd-game-a-granbluefantasy.akamaized.net"

type prefetchCandidate struct {
	host       string
	path       string
	data       []byte
	isGameData bool
	reqMethod  string
	xVersion   string
	langPrefix string
}

type discoveredRef struct {
	host string
	path string
	prio int
}

type prefetchItem struct {
	prio int
	host string
	path string
}

const (
	defaultDeadFilterCap = 1024
	defaultDeadFilterTTL = 30 * time.Minute
)

type deadKey struct {
	host string
	path string
}

type deadNode struct {
	key       deadKey
	expiresAt time.Time
	prev      *deadNode
	next      *deadNode
}

// prefetchDeadFilter records assets that returned 404 or 410 on recent background prefetch
// attempts. It operates exclusively in the prefetch pipeline (never touched by foreground
// requests), using an in-memory LRU doubly-linked list with capacity bounding and TTL to keep
// memory strictly bounded within approximately 100~150 KB.
type prefetchDeadFilter struct {
	mu       sync.RWMutex
	capacity int
	ttl      time.Duration
	items    map[deadKey]*deadNode
	head     *deadNode // dummy sentinel head
	tail     *deadNode // dummy sentinel tail
}

func newPrefetchDeadFilter(capacity int, ttl time.Duration) *prefetchDeadFilter {
	if capacity <= 0 {
		capacity = defaultDeadFilterCap
	}
	if ttl <= 0 {
		ttl = defaultDeadFilterTTL
	}
	head := &deadNode{}
	tail := &deadNode{}
	head.next = tail
	tail.prev = head

	return &prefetchDeadFilter{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[deadKey]*deadNode, capacity),
		head:     head,
		tail:     tail,
	}
}

func (f *prefetchDeadFilter) removeNode(n *deadNode) {
	n.prev.next = n.next
	n.next.prev = n.prev
	n.prev = nil
	n.next = nil
}

func (f *prefetchDeadFilter) pushFront(n *deadNode) {
	n.next = f.head.next
	n.prev = f.head
	f.head.next.prev = n
	f.head.next = n
}

func (f *prefetchDeadFilter) popTail() *deadNode {
	if f.tail.prev == f.head {
		return nil
	}
	n := f.tail.prev
	f.removeNode(n)
	return n
}

func (f *prefetchDeadFilter) Add(host, path string) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	k := deadKey{host: host, path: path}
	now := time.Now()
	exp := now.Add(f.ttl)

	if node, exists := f.items[k]; exists {
		node.expiresAt = exp
		f.removeNode(node)
		f.pushFront(node)
		return
	}

	if len(f.items) >= f.capacity {
		if old := f.popTail(); old != nil {
			delete(f.items, old.key)
		}
	}

	node := &deadNode{
		key:       k,
		expiresAt: exp,
	}
	f.pushFront(node)
	f.items[k] = node
}

func (f *prefetchDeadFilter) IsDead(host, path string) bool {
	if f == nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	k := deadKey{host: host, path: path}
	node, exists := f.items[k]
	if !exists {
		return false
	}
	if time.Now().After(node.expiresAt) {
		return false
	}
	return true
}

func (f *prefetchDeadFilter) Len() int {
	if f == nil {
		return 0
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.items)
}

type PrefetchEngine struct {
	srv               *ProxyServer
	discoveryCh       chan prefetchCandidate
	prio1Queue        chan prefetchItem // High: JS / JSON
	prio2Queue        chan prefetchItem // Medium: Images / Textures
	prio3Queue        chan prefetchItem // Low: Audio / Other
	inflightMu        sync.Mutex
	inflight          map[string]struct{}
	discoverySeen     map[string]struct{}
	discoverySeenPrev map[string]struct{}
	stopChan          chan struct{}
	stopOnce          sync.Once
	deadFilter        *prefetchDeadFilter
	activeGBFHost     atomic.Pointer[string]
	activeLangPrefix  atomic.Pointer[string]
}

func newPrefetchEngine(srv *ProxyServer) *PrefetchEngine {
	pe := &PrefetchEngine{
		srv:               srv,
		discoveryCh:       make(chan prefetchCandidate, 128),
		prio1Queue:        make(chan prefetchItem, 256),
		prio2Queue:        make(chan prefetchItem, 256),
		prio3Queue:        make(chan prefetchItem, 256),
		inflight:          make(map[string]struct{}),
		discoverySeen:     make(map[string]struct{}),
		discoverySeenPrev: make(map[string]struct{}),
		stopChan:          make(chan struct{}),
		deadFilter:        newPrefetchDeadFilter(defaultDeadFilterCap, defaultDeadFilterTTL),
	}
	defaultHost := defaultGBFHost
	pe.activeGBFHost.Store(&defaultHost)
	go pe.discoveryWorker()
	go pe.fetchWorker()
	return pe
}

func (pe *PrefetchEngine) RecordActiveGBFHost(host string) {
	if pe == nil || host == "" {
		return
	}
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	h := strings.ToLower(strings.TrimRight(host, "."))
	if isGBFAkamaiNormalized(h) {
		pe.activeGBFHost.Store(&h)
	}
}

func (pe *PrefetchEngine) GetActiveGBFHost() string {
	if pe != nil {
		if p := pe.activeGBFHost.Load(); p != nil && *p != "" {
			return *p
		}
	}
	return defaultGBFHost
}

func (pe *PrefetchEngine) RecordActiveLangPrefix(prefix string) {
	if pe == nil {
		return
	}
	if prefix == "/assets_en" || prefix == "/assets" {
		pe.activeLangPrefix.Store(&prefix)
	}
}

func (pe *PrefetchEngine) GetActiveLangPrefix() string {
	if pe != nil {
		if p := pe.activeLangPrefix.Load(); p != nil && *p != "" {
			return *p
		}
	}
	return ""
}

func (pe *PrefetchEngine) Stop() {
	pe.stopOnce.Do(func() {
		close(pe.stopChan)
	})
}

func (pe *PrefetchEngine) IsStopped() bool {
	select {
	case <-pe.stopChan:
		return true
	default:
		return false
	}
}

func (pe *PrefetchEngine) removeInflight(key string) {
	pe.inflightMu.Lock()
	delete(pe.inflight, key)
	pe.inflightMu.Unlock()
}

func (pe *PrefetchEngine) QueueLen() int {
	return len(pe.prio1Queue) + len(pe.prio2Queue) + len(pe.prio3Queue)
}

func (pe *PrefetchEngine) enqueueTask(item prefetchItem) bool {
	var target chan prefetchItem
	switch item.prio {
	case 1:
		target = pe.prio1Queue
	case 2:
		target = pe.prio2Queue
	default:
		target = pe.prio3Queue
	}

	select {
	case target <- item:
		return true
	default:
		return false
	}
}

func (pe *PrefetchEngine) getNextTask() (prefetchItem, bool) {
	// Strict Priority Scheduling: P1 (JS/JSON) > P2 (Textures) > P3 (Audio/Other)
	select {
	case <-pe.stopChan:
		return prefetchItem{}, false
	case item := <-pe.prio1Queue:
		return item, true
	default:
	}

	select {
	case <-pe.stopChan:
		return prefetchItem{}, false
	case item := <-pe.prio1Queue:
		return item, true
	case item := <-pe.prio2Queue:
		return item, true
	default:
	}

	select {
	case <-pe.stopChan:
		return prefetchItem{}, false
	case item := <-pe.prio1Queue:
		return item, true
	case item := <-pe.prio2Queue:
		return item, true
	case item := <-pe.prio3Queue:
		return item, true
	}
}

func (pe *PrefetchEngine) MaybeEnqueueDiscovery(host, path string, data []byte) {
	cfg := pe.srv.cfgMgr.Get()
	if !cfg.EnablePrefetch {
		return
	}
	if len(data) < 2 {
		return
	}
	cleanPath, _, _ := strings.Cut(path, "?")
	p := strings.ToLower(cleanPath)
	if !strings.HasSuffix(p, ".js") && !strings.HasSuffix(p, ".json") {
		return
	}

	key := host + p
	pe.inflightMu.Lock()
	if _, exists := pe.discoverySeen[key]; exists {
		pe.inflightMu.Unlock()
		return
	}
	if _, exists := pe.discoverySeenPrev[key]; exists {
		pe.inflightMu.Unlock()
		return
	}
	if len(pe.discoverySeen) > 2000 {
		pe.discoverySeenPrev = pe.discoverySeen
		pe.discoverySeen = make(map[string]struct{}, 512)
	}
	pe.discoverySeen[key] = struct{}{}
	pe.inflightMu.Unlock()

	select {
	case pe.discoveryCh <- prefetchCandidate{host: host, path: path, data: data}:
	default:
		// Queue full, drop without delaying foreground path
	}
}

func (pe *PrefetchEngine) MaybeEnqueueGameData(req *http.Request, targetHost string, respBytes []byte) {
	if pe == nil || req == nil || req.URL == nil || len(respBytes) < 2 || len(respBytes) > 2*1024*1024 {
		return
	}
	if pe.srv == nil || pe.srv.cfgMgr == nil {
		return
	}
	cfg := pe.srv.cfgMgr.Get()
	if !cfg.EnablePrefetch {
		return
	}
	if pe.IsStopped() {
		return
	}

	if targetHost == "" {
		targetHost = req.Host
	}
	if !isGBFDomain(targetHost) {
		return
	}

	cleanPath := strings.ToLower(strings.Split(req.URL.Path, "?")[0])
	cleanPath = strings.TrimSuffix(cleanPath, "/")
	if !isGameDataAPI(req.Method, cleanPath) {
		return
	}

	langPrefix := pe.determineLangPrefix(req)
	if langPrefix != "/assets" && langPrefix != "/assets_en" {
		// If unable to reliably determine language directory, skip Game-Data Prefetch
		return
	}
	host := pe.GetActiveGBFHost()
	xVer := strings.TrimSpace(req.Header.Get("X-VERSION"))

	cand := prefetchCandidate{
		host:       host,
		path:       cleanPath,
		data:       respBytes,
		isGameData: true,
		reqMethod:  req.Method,
		xVersion:   xVer,
		langPrefix: langPrefix,
	}

	select {
	case pe.discoveryCh <- cand:
	default:
		// Queue full, drop without delaying foreground path
	}
}

func (pe *PrefetchEngine) determineLangPrefix(req *http.Request) string {
	if req != nil {
		for _, c := range req.Cookies() {
			if c.Name == "language_type" {
				val := strings.Trim(c.Value, "\"")
				if val == "2" {
					pe.RecordActiveLangPrefix("/assets_en")
					return "/assets_en"
				} else if val == "1" {
					pe.RecordActiveLangPrefix("/assets")
					return "/assets"
				}
			}
		}
	}
	return pe.GetActiveLangPrefix()
}

func getPrefetchPriority(urlPath string) int {
	return getPrefetchPriorityLower(strings.ToLower(urlPath))
}

func getPrefetchPriorityLower(p string) int {
	if strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".json") {
		return 1
	}
	if strings.Contains(p, "/img/sp/") || strings.Contains(p, "/cjs/") || strings.HasSuffix(p, ".png") || strings.HasSuffix(p, ".webp") {
		return 2
	}
	if strings.HasSuffix(p, ".mp3") || strings.HasSuffix(p, ".wav") || strings.HasSuffix(p, ".ogg") || strings.HasSuffix(p, ".m4a") {
		return 3
	}
	return 4
}

func (pe *PrefetchEngine) extractAssetRefs(urlPath string, body []byte, defaultHost string) [][2]string {
	seen := make(map[string]struct{})
	var refs [][2]string

	cleanURL, _, _ := strings.Cut(urlPath, "?")

	// 1. Deduced twin CreateJS script for model manifest files
	if mTwin := twinManifestRe.FindStringSubmatch(cleanURL); len(mTwin) == 3 {
		twinCJS := fmt.Sprintf("/%scjs/%s", mTwin[1], mTwin[2])
		key := defaultHost + twinCJS
		seen[key] = struct{}{}
		refs = append(refs, [2]string{defaultHost, twinCJS})
	}

	// 2. Standard asset references (streamed with early exit)
	offset := 0
	bodyLen := len(body)
	scannedMatches := 0
	for len(refs) < 120 && offset < bodyLen && scannedMatches < 300 {
		loc := assetRefRe.FindSubmatchIndex(body[offset:])
		if loc == nil {
			break
		}
		scannedMatches++
		var refHost string
		if loc[2] >= 0 && loc[3] >= 0 {
			hBytes := body[offset+loc[2] : offset+loc[3]]
			refHost = strings.ToLower(string(hBytes))
			if refHost != "" && !isGBFAkamaiHost(refHost) && !isDomainOrSubdomain(refHost, "granbluefantasy.jp") && !isDomainOrSubdomain(refHost, "granbluefantasy.com") {
				if loc[1] == 0 {
					offset++
				} else {
					offset += loc[1]
				}
				continue
			}
		}
		if refHost == "" {
			refHost = defaultHost
		}

		if loc[4] >= 0 && loc[5] >= 0 {
			pBytes := body[offset+loc[4] : offset+loc[5]]
			if len(pBytes) <= 200 {
				for len(pBytes) > 0 && pBytes[0] == '/' {
					pBytes = pBytes[1:]
				}
				refPath := "/" + string(pBytes)
				key := refHost + refPath
				if _, ok := seen[key]; !ok {
					seen[key] = struct{}{}
					refs = append(refs, [2]string{refHost, refPath})
				}
			}
		}

		if loc[1] == 0 {
			offset++
		} else {
			offset += loc[1]
		}
	}

	// 3. CreateJS & Game.imgUri spritesheets and textures (streamed with early exit)
	if len(refs) < 120 && scannedMatches < 300 {
		imgPrefix := "/assets/img"
		if strings.HasPrefix(cleanURL, "/assets_en/") {
			imgPrefix = "/assets_en/img"
		}
		offset = 0
		for len(refs) < 120 && offset < bodyLen && scannedMatches < 300 {
			loc := cjsImgRefRe.FindSubmatchIndex(body[offset:])
			if loc == nil {
				break
			}
			scannedMatches++
			if loc[2] >= 0 && loc[3] >= 0 {
				rawBytes := body[offset+loc[2] : offset+loc[3]]
				if len(rawBytes) <= 200 {
					var imgPath string
					if bytes.HasPrefix(rawBytes, []byte("sp/")) {
						imgPath = imgPrefix + "/" + string(rawBytes)
					} else {
						for len(rawBytes) > 0 && rawBytes[0] == '/' {
							rawBytes = rawBytes[1:]
						}
						imgPath = "/" + string(rawBytes)
					}
					key := defaultHost + imgPath
					if _, ok := seen[key]; !ok {
						seen[key] = struct{}{}
						refs = append(refs, [2]string{defaultHost, imgPath})
					}
				}
			}
			if loc[1] == 0 {
				offset++
			} else {
				offset += loc[1]
			}
		}
	}

	return refs
}

func (pe *PrefetchEngine) dispatchDiscoveredRefs(refs []discoveredRef) {
	if pe == nil || pe.srv == nil || pe.srv.cacheMgr == nil {
		return
	}
	for _, r := range refs {
		h, p, prio := r.host, r.path, r.prio
		ns, _ := NormalizeAssetNamespace(h)
		cleanP, _, _ := strings.Cut(p, "?")
		if pe.srv.cacheMgr.HasCacheWithNamespace(ns, cleanP) {
			continue
		}
		if pe.deadFilter != nil && pe.deadFilter.IsDead(h, cleanP) {
			continue
		}
		key := h + cleanP
		pe.inflightMu.Lock()
		if _, ok := pe.inflight[key]; ok {
			pe.inflightMu.Unlock()
			continue
		}
		if len(pe.inflight) > 2000 {
			pe.inflight = make(map[string]struct{})
		}
		pe.inflight[key] = struct{}{}
		pe.inflightMu.Unlock()

		if !pe.enqueueTask(prefetchItem{prio: prio, host: h, path: p}) {
			// Queue full, drop and release inflight key
			pe.removeInflight(key)
		}
	}
}

func (pe *PrefetchEngine) discoveryWorker() {
	for {
		select {
		case <-pe.stopChan:
			return
		case cand := <-pe.discoveryCh:
			raw := cand.data
			if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
				if gr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
					if decompressed, err := io.ReadAll(io.LimitReader(gr, 2*1024*1024)); err == nil {
						raw = decompressed
					}
					_ = gr.Close()
				}
			}

			if cand.isGameData {
				cand.data = raw
				refs := pe.extractGameDataRefs(cand)
				pe.dispatchDiscoveredRefs(refs)
				continue
			}

			refs := pe.extractAssetRefs(cand.path, raw, cand.host)
			var discRefs []discoveredRef
			for _, r := range refs {
				h, p := r[0], r[1]
				prio := getPrefetchPriorityLower(p)
				discRefs = append(discRefs, discoveredRef{host: h, path: p, prio: prio})
			}
			pe.dispatchDiscoveredRefs(discRefs)
		}
	}
}

func (pe *PrefetchEngine) fetchWorker() {
	for {
		item, ok := pe.getNextTask()
		if !ok {
			return
		}
		key := item.host + item.path
		pe.processFetchItem(item)
		pe.removeInflight(key)
	}
}

func (pe *PrefetchEngine) processFetchItem(item prefetchItem) {
	// P1 Dynamic yielding: pause if active dynamic API or foreground assets
	waitStart := time.Now()
	if !pe.srv.stats.WaitForegroundIdle(pe.stopChan) {
		return
	}
	waitMs := time.Since(waitStart).Milliseconds()

	if !pe.srv.cfgMgr.Get().EnablePrefetch {
		return
	}
	ns, _ := NormalizeAssetNamespace(item.host)
	cleanPath, _, _ := strings.Cut(item.path, "?")
	if pe.deadFilter.IsDead(item.host, cleanPath) {
		return
	}
	flightKey := ns + ":" + cleanPath

	if pe.srv.cacheMgr.HasCacheWithNamespace(ns, cleanPath) {
		return
	}

	// Guard against duplicate fetch if foreground is ALREADY fetching this exact asset
	if pe.srv.cacheMgr.SingleFlight().IsInFlight(flightKey) {
		return
	}

	pe.srv.stats.IncPrefetchRequest()
	fetchStart := time.Now()

	// Execute through SingleFlight so concurrent foreground requests coalesce naturally
	res, err := pe.srv.cacheMgr.SingleFlight().DoContext(context.Background(), flightKey, func() (interface{}, error) {
		// Double check cache in case it landed just before acquiring flight
		if pe.srv.cacheMgr.SingleFlight().Waiters(flightKey) > 0 {
			// Foreground is actively waiting: promote probationary items to protected
			if cached, _ := pe.srv.cacheMgr.GetWithNamespace(ns, cleanPath); cached != nil {
				return cached, nil
			}
		} else {
			// Pure prefetch: peek RAM cache without promoting probationary items
			if cached := pe.srv.cacheMgr.PeekRAMWithNamespace(ns, cleanPath); cached != nil {
				return cached, nil
			}
			if pe.srv.cacheMgr.HasCacheWithNamespace(ns, cleanPath) {
				return nil, nil
			}
		}

		upURL := fmt.Sprintf("https://%s%s", item.host, item.path)
		req, err := http.NewRequestWithContext(context.Background(), "GET", upURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", prefetchUserAgent)
		req.Header.Set("Accept", "*/*")
		req.Header.Set("Accept-Encoding", "gzip")
		req.Host = item.host

		resp, err := pe.srv.getAssetClient().Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone) {
				pe.deadFilter.Add(item.host, cleanPath)
			}
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
		}

		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || len(data) == 0 {
			return nil, fmt.Errorf("empty body: %v", err)
		}

		ct := resp.Header.Get("Content-Type")
		if !cache.IsValidCacheContent(item.path, ct, data) {
			return nil, fmt.Errorf("invalid cache content")
		}

		headersMap := make(map[string]string)
		for k, vv := range resp.Header {
			if len(vv) > 0 {
				headersMap[strings.ToLower(k)] = vv[0]
			}
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			etag = resp.Header.Get("Etag")
		}
		if etag != "" {
			headersMap["etag"] = etag
		}

		var savedItem *cache.CacheItem
		var ok bool
		if pe.srv.cacheMgr.SingleFlight().Waiters(flightKey) > 0 {
			// Foreground is actively waiting: admit directly to Protected segment
			savedItem, ok = pe.srv.cacheMgr.SaveRAMValidatedWithNamespace(ns, item.path, headersMap, data)
		} else {
			// Pure background prefetch: admit to Probationary segment to protect hot cache
			savedItem, ok = pe.srv.cacheMgr.SavePrefetchValidatedWithNamespace(ns, item.path, headersMap, data)
		}
		if !ok || savedItem == nil {
			return nil, fmt.Errorf("failed to save asset")
		}
		return savedItem, nil
	})

	if err == nil && res != nil {
		fetchMs := time.Since(fetchStart).Milliseconds()
		dataLen := 0
		if ci, ok := res.(*cache.CacheItem); ok && ci != nil {
			dataLen = len(ci.Data)
		}
		pe.srv.stats.IncPrefetchSuccess()
		pe.srv.stats.MarkPrefetchSaved(cleanPath)
		pe.srv.stats.Log("INFO", fmt.Sprintf("[PREFETCH] P%d wait=%dms fetch=%dms -> %s%s (%d B)", item.prio, waitMs, fetchMs, item.host, cleanPath, dataLen))
	}

	// Pacing jitter (15~35ms)
	if pe.QueueLen() > 0 {
		jitter := 15 + rand.IntN(21)
		select {
		case <-pe.stopChan:
			return
		case <-time.After(time.Duration(jitter) * time.Millisecond):
		}
	}
}

func isGameDataAPI(method, cleanPath string) bool {
	cleanPath = strings.TrimSuffix(cleanPath, "/")
	if method == http.MethodPost && cleanPath == "/rest/multiraid/start.json" {
		return true
	}
	if method == http.MethodGet && isClearedQuestScenarioPath(cleanPath) {
		return true
	}
	return false
}

func isClearedQuestScenarioPath(cleanPath string) bool {
	cleanPath = strings.TrimSuffix(cleanPath, "/")
	const prefix = "/rest/quest/cleared_quest_scenario/"
	if !strings.HasPrefix(cleanPath, prefix) {
		return false
	}
	rest := cleanPath[len(prefix):]
	if strings.Contains(rest, "..") || strings.Contains(rest, "\\") {
		return false
	}
	slashIdx := strings.IndexByte(rest, '/')
	if slashIdx <= 0 || slashIdx == len(rest)-1 {
		return false
	}
	second := rest[slashIdx+1:]
	if strings.IndexByte(second, '/') != -1 {
		return false
	}
	return true
}

func (pe *PrefetchEngine) extractGameDataRefs(cand prefetchCandidate) []discoveredRef {
	cleanPath := strings.TrimSuffix(cand.path, "/")
	if cand.reqMethod == http.MethodPost && cleanPath == "/rest/multiraid/start.json" {
		return pe.extractMultiRaidRefs(cand)
	}
	if cand.reqMethod == http.MethodGet && isClearedQuestScenarioPath(cleanPath) {
		return pe.extractClearedQuestRefs(cand)
	}
	return nil
}

func (pe *PrefetchEngine) extractMultiRaidRefs(cand prefetchCandidate) []discoveredRef {
	langPrefix := cand.langPrefix
	if langPrefix != "/assets" && langPrefix != "/assets_en" {
		return nil
	}

	var root map[string]any
	if err := json.Unmarshal(cand.data, &root); err != nil || root == nil {
		return nil
	}

	var refs []discoveredRef
	host := cand.host

	// 1. CJS extraction: manifest and cjs scripts -> P1
	ver := strings.TrimSpace(cand.xVersion)
	if ver != "" && isDigitsOnly(ver) {
		seenCJS := make(map[string]struct{})
		addCJS := func(cjs string) {
			cjs = strings.TrimSpace(cjs)
			if !isValidCJSIdentifier(cjs) {
				return
			}
			if _, ok := seenCJS[cjs]; ok {
				return
			}
			seenCJS[cjs] = struct{}{}

			manifestURL := fmt.Sprintf("%s/%s/js/model/manifest/%s.js", langPrefix, ver, cjs)
			cjsURL := fmt.Sprintf("%s/%s/js/cjs/%s.js", langPrefix, ver, cjs)

			refs = append(refs,
				discoveredRef{host: host, path: manifestURL, prio: 1},
				discoveredRef{host: host, path: cjsURL, prio: 1},
			)
		}

		extractCJSFromParam := func(paramObj any) {
			if paramObj == nil {
				return
			}
			switch pv := paramObj.(type) {
			case []any:
				for _, item := range pv {
					if itemMap, ok := item.(map[string]any); ok {
						if cjsVal, ok := itemMap["cjs"].(string); ok {
							addCJS(cjsVal)
						}
					}
				}
			case map[string]any:
				if cjsVal, ok := pv["cjs"].(string); ok {
					addCJS(cjsVal)
				} else {
					for _, item := range pv {
						if itemMap, ok := item.(map[string]any); ok {
							if cjsVal, ok := itemMap["cjs"].(string); ok {
								addCJS(cjsVal)
							}
						}
					}
				}
			}
		}

		if playerObj, ok := root["player"].(map[string]any); ok {
			extractCJSFromParam(playerObj["param"])
		} else if playerSlice, ok := root["player"].([]any); ok {
			extractCJSFromParam(playerSlice)
		}

		if bossObj, ok := root["boss"].(map[string]any); ok {
			extractCJSFromParam(bossObj["param"])
		} else if bossSlice, ok := root["boss"].([]any); ok {
			extractCJSFromParam(bossSlice)
		}
	}

	// 2. Background image: strictly the first priority background -> P2
	if bgPath := extractFirstBackgroundImage(root["background_image_object"], langPrefix); bgPath != "" {
		refs = append(refs, discoveredRef{host: host, path: bgPath, prio: 2})
	}

	return refs
}

func extractFirstBackgroundImage(obj any, langPrefix string) string {
	if obj == nil || (langPrefix != "/assets" && langPrefix != "/assets_en") {
		return ""
	}

	tryExtractString := func(raw string) string {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > 256 || strings.Contains(raw, "..") || strings.Contains(raw, "\\") || strings.Contains(raw, ":") {
			return ""
		}
		if !isVisualImageExt(raw) {
			return ""
		}
		return normalizeCDNImagePath(raw, langPrefix)
	}

	tryExtractMap := func(m map[string]any) string {
		if m == nil {
			return ""
		}
		for _, key := range []string{"image", "path", "url", "src", "bg", "0", "1"} {
			if s, ok := m[key].(string); ok {
				if path := tryExtractString(s); path != "" {
					return path
				}
			}
		}
		return ""
	}

	switch v := obj.(type) {
	case string:
		return tryExtractString(v)
	case []any:
		for _, item := range v {
			if item == nil {
				continue
			}
			switch iv := item.(type) {
			case string:
				if path := tryExtractString(iv); path != "" {
					return path
				}
			case map[string]any:
				if path := tryExtractMap(iv); path != "" {
					return path
				}
			}
		}
	case []string:
		for _, s := range v {
			if path := tryExtractString(s); path != "" {
				return path
			}
		}
	case map[string]any:
		return tryExtractMap(v)
	}

	return ""
}

func (pe *PrefetchEngine) extractClearedQuestRefs(cand prefetchCandidate) []discoveredRef {
	langPrefix := cand.langPrefix
	if langPrefix != "/assets" && langPrefix != "/assets_en" {
		return nil
	}

	var root map[string]any
	if err := json.Unmarshal(cand.data, &root); err != nil || root == nil {
		return nil
	}

	var scenes []map[string]any
	switch sl := root["scene_list"].(type) {
	case []any:
		for _, item := range sl {
			if sm, ok := item.(map[string]any); ok && sm != nil {
				scenes = append(scenes, sm)
			}
		}
	case map[string]any:
		var keys []string
		for k := range sl {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sm, ok := sl[k].(map[string]any); ok && sm != nil {
				scenes = append(scenes, sm)
			}
		}
	}

	if len(scenes) == 0 {
		return nil
	}

	host := cand.host
	if len(scenes) > 5 {
		scenes = scenes[:5]
	}

	var refs []discoveredRef
	seen := make(map[string]struct{})

	priorityKeys := []string{
		"bg", "bg1", "bg2", "bg3",
		"charcter1_big_image", "charcter2_big_image", "charcter3_big_image",
		"character1_big_image", "character2_big_image", "character3_big_image",
	}

	extractVisualString := func(val any) string {
		if val == nil {
			return ""
		}
		var raw string
		switch v := val.(type) {
		case string:
			raw = v
		case map[string]any:
			for _, k := range []string{"image", "path", "url", "src"} {
				if s, ok := v[k].(string); ok && s != "" {
					raw = s
					break
				}
			}
		}
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > 256 || strings.Contains(raw, "..") || strings.Contains(raw, "\\") || strings.Contains(raw, ":") {
			return ""
		}
		if !isVisualImageExt(raw) {
			return ""
		}
		return normalizeCDNImagePath(raw, langPrefix)
	}

	for _, scene := range scenes {
		checkedKeys := make(map[string]struct{}, len(priorityKeys))
		for _, key := range priorityKeys {
			checkedKeys[key] = struct{}{}
			val, ok := scene[key]
			if !ok {
				continue
			}
			cdnPath := extractVisualString(val)
			if cdnPath == "" {
				continue
			}
			if _, exists := seen[cdnPath]; !exists {
				seen[cdnPath] = struct{}{}
				refs = append(refs, discoveredRef{host: host, path: cdnPath, prio: 2})
				if len(refs) >= 5 {
					return refs
				}
			}
		}

		var otherKeys []string
		for k := range scene {
			if _, already := checkedKeys[k]; already {
				continue
			}
			lowerK := strings.ToLower(k)
			// Strictly exclude audio/bgm/sound/voice/se/music
			if strings.HasPrefix(lowerK, "bgm") || strings.Contains(lowerK, "sound") || strings.Contains(lowerK, "voice") || strings.Contains(lowerK, "se") || strings.Contains(lowerK, "audio") || strings.Contains(lowerK, "music") {
				continue
			}
			if (strings.HasPrefix(lowerK, "charcter") && strings.HasSuffix(lowerK, "_big_image")) ||
				(strings.HasPrefix(lowerK, "character") && strings.HasSuffix(lowerK, "_big_image")) ||
				strings.HasPrefix(lowerK, "bg") {
				otherKeys = append(otherKeys, k)
			}
		}
		sort.Strings(otherKeys)
		for _, key := range otherKeys {
			val, ok := scene[key]
			if !ok {
				continue
			}
			cdnPath := extractVisualString(val)
			if cdnPath == "" {
				continue
			}
			if _, exists := seen[cdnPath]; !exists {
				seen[cdnPath] = struct{}{}
				refs = append(refs, discoveredRef{host: host, path: cdnPath, prio: 2})
				if len(refs) >= 5 {
					return refs
				}
			}
		}
	}

	return refs
}

func normalizeCDNImagePath(raw, langPrefix string) string {
	clean, _, _ := strings.Cut(raw, "?")
	clean = strings.TrimSpace(clean)
	if strings.HasPrefix(clean, "/assets/") || strings.HasPrefix(clean, "/assets_en/") {
		return clean
	}
	if strings.HasPrefix(clean, "assets/") {
		return "/" + clean
	}
	if strings.HasPrefix(clean, "assets_en/") {
		return "/" + clean
	}
	if langPrefix != "/assets" && langPrefix != "/assets_en" {
		return ""
	}
	if strings.HasPrefix(clean, "/sp/") {
		return langPrefix + "/img" + clean
	}
	if strings.HasPrefix(clean, "sp/") {
		return langPrefix + "/img/" + clean
	}
	if strings.HasPrefix(clean, "/") {
		return langPrefix + "/img" + clean
	}
	return langPrefix + "/img/" + clean
}

func isDigitsOnly(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isValidCJSIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func isVisualImageExt(p string) bool {
	lower := strings.ToLower(p)
	clean, _, _ := strings.Cut(lower, "?")
	return strings.HasSuffix(clean, ".png") ||
		strings.HasSuffix(clean, ".jpg") ||
		strings.HasSuffix(clean, ".jpeg") ||
		strings.HasSuffix(clean, ".webp") ||
		strings.HasSuffix(clean, ".gif")
}
