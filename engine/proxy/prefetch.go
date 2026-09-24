package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strings"
	"sync"
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

type prefetchCandidate struct {
	host string
	path string
	data []byte
}

type prefetchItem struct {
	prio int
	host string
	path string
}

type PrefetchEngine struct {
	srv           *ProxyServer
	discoveryCh   chan prefetchCandidate
	prio1Queue    chan prefetchItem // High: JS / JSON
	prio2Queue    chan prefetchItem // Medium: Images / Textures
	prio3Queue    chan prefetchItem // Low: Audio / Other
	inflightMu    sync.Mutex
	inflight      map[string]struct{}
	discoverySeen map[string]struct{}
	stopChan      chan struct{}
	stopOnce      sync.Once
}

func newPrefetchEngine(srv *ProxyServer) *PrefetchEngine {
	pe := &PrefetchEngine{
		srv:           srv,
		discoveryCh:   make(chan prefetchCandidate, 128),
		prio1Queue:    make(chan prefetchItem, 256),
		prio2Queue:    make(chan prefetchItem, 256),
		prio3Queue:    make(chan prefetchItem, 256),
		inflight:      make(map[string]struct{}),
		discoverySeen: make(map[string]struct{}),
		stopChan:      make(chan struct{}),
	}
	go pe.discoveryWorker()
	go pe.fetchWorker()
	return pe
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
	if len(pe.discoverySeen) > 1000 {
		pe.discoverySeen = make(map[string]struct{})
	}
	pe.discoverySeen[key] = struct{}{}
	pe.inflightMu.Unlock()

	select {
	case pe.discoveryCh <- prefetchCandidate{host: host, path: path, data: data}:
	default:
		// Queue full, drop without delaying foreground path
	}
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

func (pe *PrefetchEngine) discoveryWorker() {
	for {
		select {
		case <-pe.stopChan:
			return
		case cand := <-pe.discoveryCh:
			raw := cand.data
			if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
				if gr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
					if decompressed, err := io.ReadAll(gr); err == nil {
						raw = decompressed
					}
					_ = gr.Close()
				}
			}
			refs := pe.extractAssetRefs(cand.path, raw, cand.host)

			for _, r := range refs {
				h, p := r[0], r[1]
				ns, _ := NormalizeAssetNamespace(h)
				if pe.srv.cacheMgr.HasCacheWithNamespace(ns, p) {
					continue
				}
				key := h + p
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

				// Asset references are matched by lowercase extensions/prefixes, so p is
				// already normalized enough for priority checks; avoid another ToLower allocation.
				prio := getPrefetchPriorityLower(p)
				if !pe.enqueueTask(prefetchItem{prio: prio, host: h, path: p}) {
					// Queue full, drop and release inflight key
					pe.removeInflight(key)
				}
			}
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
	if pe.srv.cacheMgr.HasCacheWithNamespace(ns, item.path) {
		return
	}

	pe.srv.stats.IncPrefetchRequest()
	upURL := fmt.Sprintf("https://%s%s", item.host, item.path)
	req, err := http.NewRequestWithContext(context.Background(), "GET", upURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", prefetchUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Host = item.host

	fetchStart := time.Now()
	resp, err := pe.srv.getAssetClient().Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return
	}

	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	fetchMs := time.Since(fetchStart).Milliseconds()
	if err != nil || len(data) == 0 {
		return
	}

	ct := resp.Header.Get("Content-Type")
	if !cache.IsValidCacheContent(item.path, ct, data) {
		return
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

	if pe.srv.cacheMgr.SaveWithNamespace(ns, item.path, headersMap, data) {
		pe.srv.stats.IncPrefetchSuccess()
		pe.srv.stats.MarkPrefetchSaved(item.path)
		pe.srv.stats.Log("INFO", fmt.Sprintf("[PREFETCH] P%d wait=%dms fetch=%dms -> %s%s (%d B)", item.prio, waitMs, fetchMs, item.host, item.path, len(data)))
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
