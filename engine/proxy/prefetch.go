package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
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
	taskQueue     chan prefetchItem
	inflightMu    sync.Mutex
	inflight      map[string]struct{}
	discoverySeen map[string]struct{}
	stopChan      chan struct{}
}

func newPrefetchEngine(srv *ProxyServer) *PrefetchEngine {
	pe := &PrefetchEngine{
		srv:           srv,
		discoveryCh:   make(chan prefetchCandidate, 128),
		taskQueue:     make(chan prefetchItem, 512),
		inflight:      make(map[string]struct{}),
		discoverySeen: make(map[string]struct{}),
		stopChan:      make(chan struct{}),
	}
	go pe.discoveryWorker()
	go pe.fetchWorker()
	return pe
}

func (pe *PrefetchEngine) Stop() {
	close(pe.stopChan)
}

func (pe *PrefetchEngine) QueueLen() int {
	return len(pe.taskQueue)
}

func (pe *PrefetchEngine) MaybeEnqueueDiscovery(host, path string, data []byte) {
	cfg := pe.srv.cfgMgr.Get()
	if !cfg.EnablePrefetch {
		return
	}
	if len(data) < 2 {
		return
	}
	p := strings.ToLower(strings.Split(path, "?")[0])
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
	p := strings.ToLower(urlPath)
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

func (pe *PrefetchEngine) extractAssetRefs(urlPath string, body string, defaultHost string) [][2]string {
	seen := make(map[string]struct{})
	var refs [][2]string

	cleanURL := strings.Split(urlPath, "?")[0]

	// 1. Deduced twin CreateJS script for model manifest files
	if mTwin := twinManifestRe.FindStringSubmatch(cleanURL); len(mTwin) == 3 {
		twinCJS := fmt.Sprintf("/%scjs/%s", mTwin[1], mTwin[2])
		key := defaultHost + twinCJS
		seen[key] = struct{}{}
		refs = append(refs, [2]string{defaultHost, twinCJS})
	}

	// 2. Standard asset references
	matches := assetRefRe.FindAllStringSubmatch(body, -1)
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		refHost := strings.ToLower(m[1])
		if refHost != "" && !isGBFAkamaiHost(refHost) && !isDomainOrSubdomain(refHost, "granbluefantasy.jp") && !isDomainOrSubdomain(refHost, "granbluefantasy.com") {
			continue
		}
		if refHost == "" {
			refHost = defaultHost
		}
		refPath := "/" + strings.TrimPrefix(m[2], "/")
		if len(refPath) > 200 {
			continue
		}
		key := refHost + refPath
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			refs = append(refs, [2]string{refHost, refPath})
			if len(refs) >= 120 {
				return refs
			}
		}
	}

	// 3. CreateJS & Game.imgUri spritesheets and textures
	if len(refs) < 120 {
		imgPrefix := "/assets/img"
		if strings.HasPrefix(cleanURL, "/assets_en/") {
			imgPrefix = "/assets_en/img"
		}
		cjsMatches := cjsImgRefRe.FindAllStringSubmatch(body, -1)
		for _, m := range cjsMatches {
			if len(m) < 2 {
				continue
			}
			raw := m[1]
			var imgPath string
			if strings.HasPrefix(raw, "sp/") {
				imgPath = imgPrefix + "/" + raw
			} else {
				imgPath = "/" + strings.TrimPrefix(raw, "/")
			}
			if len(imgPath) > 200 {
				continue
			}
			key := defaultHost + imgPath
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				refs = append(refs, [2]string{defaultHost, imgPath})
				if len(refs) >= 120 {
					return refs
				}
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
			refs := pe.extractAssetRefs(cand.path, string(raw), cand.host)

			for _, r := range refs {
				h, p := r[0], r[1]
				if pe.srv.cacheMgr.HasCache(p) {
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

				prio := getPrefetchPriority(p)
				select {
				case pe.taskQueue <- prefetchItem{prio: prio, host: h, path: p}:
				default:
					// Task queue full, drop
				}
			}
		}
	}
}

func (pe *PrefetchEngine) fetchWorker() {
	for {
		select {
		case <-pe.stopChan:
			return
		case item := <-pe.taskQueue:
			// P1 Dynamic yielding: pause if active dynamic API or foreground assets
			for atomic.LoadInt32(&pe.srv.stats.ActiveAPICount) > 0 || atomic.LoadInt32(&pe.srv.stats.ActiveForegroundAssets) > 0 {
				select {
				case <-pe.stopChan:
					return
				case <-time.After(50 * time.Millisecond):
				}
			}

			if !pe.srv.cfgMgr.Get().EnablePrefetch {
				continue
			}
			if pe.srv.cacheMgr.HasCache(item.path) {
				continue
			}

			pe.srv.stats.IncPrefetchRequest()
			upURL := fmt.Sprintf("https://%s%s", item.host, item.path)
			req, err := http.NewRequestWithContext(context.Background(), "GET", upURL, nil)
			if err != nil {
				continue
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
			req.Header.Set("Accept", "*/*")
			req.Host = item.host

			resp, err := pe.srv.getAssetClient().Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				continue
			}

			data, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || len(data) == 0 {
				continue
			}

			ct := resp.Header.Get("Content-Type")
			if !cache.IsValidCacheContent(item.path, ct, data) {
				continue
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

			if pe.srv.cacheMgr.Save(item.path, headersMap, data) {
				pe.srv.stats.IncPrefetchSuccess()
				pe.srv.stats.MarkPrefetchSaved(item.path)
				pe.srv.stats.Log("INFO", fmt.Sprintf("[PREFETCH] Warmed (P%d) -> %s%s (%d B)", item.prio, item.host, item.path, len(data)))
			}

			// Pacing jitter (15~35ms)
			if len(pe.taskQueue) > 0 {
				jitter := 15 + rand.Intn(21)
				select {
				case <-pe.stopChan:
					return
				case <-time.After(time.Duration(jitter) * time.Millisecond):
				}
			}
		}
	}
}
