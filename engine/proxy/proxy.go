package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

type ProxyServer struct {
	cfgMgr      *config.Manager
	certMgr     *cert.Manager
	cacheMgr    *cache.Manager
	stats       *telemetry.Stats
	listener    net.Listener
	mu          sync.RWMutex
	apiClient   *http.Client
	assetClient *http.Client
	running     bool
	closedChan  chan struct{}
}

func NewProxyServer(cfgMgr *config.Manager, certMgr *cert.Manager, cacheMgr *cache.Manager, stats *telemetry.Stats) *ProxyServer {
	s := &ProxyServer{
		cfgMgr:     cfgMgr,
		certMgr:    certMgr,
		cacheMgr:   cacheMgr,
		stats:      stats,
		closedChan: make(chan struct{}),
	}
	initialCfg := cfgMgr.Get()
	s.updateClients(&initialCfg)
	cfgMgr.OnUpdate(func(c *config.Config) {
		s.updateClients(c)
	})
	return s
}

func (s *ProxyServer) updateClients(c *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var proxyFunc func(*http.Request) (*url.URL, error)
	effProxy := c.UpstreamProxy
	if c.DirectMode {
		effProxy = ""
	}
	if effProxy != "" {
		if u, err := url.Parse(effProxy); err == nil {
			proxyFunc = http.ProxyURL(u)
		}
	}

	apiMaxConn := c.APIMaxConnections
	if apiMaxConn <= 0 {
		apiMaxConn = 16
	}
	apiMaxIdle := c.APIMaxKeepalive
	if apiMaxIdle <= 0 {
		apiMaxIdle = 4
	}

	assetMaxConn := c.AssetMaxConnections
	if assetMaxConn <= 0 {
		assetMaxConn = 100
	}
	assetMaxIdle := c.AssetMaxKeepalive
	if assetMaxIdle <= 0 {
		assetMaxIdle = 40
	}

	apiTransport := &http.Transport{
		Proxy: proxyFunc,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !c.VerifyUpstreamTLS,
		},
		MaxIdleConns:        apiMaxConn,
		MaxIdleConnsPerHost: apiMaxConn,
		IdleConnTimeout:     time.Duration(c.APIKeepaliveExpiry) * time.Second,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   false, // Dedicated HTTP/1.1 pool for dynamic APIs
	}
	s.apiClient = &http.Client{
		Transport: apiTransport,
		Timeout:   45 * time.Second,
	}

	assetTransport := &http.Transport{
		Proxy: proxyFunc,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !c.VerifyUpstreamTLS,
		},
		MaxIdleConns:        assetMaxConn,
		MaxIdleConnsPerHost: assetMaxConn,
		IdleConnTimeout:     time.Duration(c.AssetKeepaliveExpiry) * time.Second,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   true, // HTTP/2 multiplexed for Akamai CDN
	}
	s.assetClient = &http.Client{
		Transport: assetTransport,
		Timeout:   45 * time.Second,
	}
}

func (s *ProxyServer) getAPIClient() *http.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.apiClient
}

func (s *ProxyServer) getAssetClient() *http.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.assetClient
}

func (s *ProxyServer) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}

	cfg := s.cfgMgr.Get()
	bindHost := s.cfgMgr.GetEffectiveListenHost()
	addr := fmt.Sprintf("%s:%d", bindHost, cfg.ListenPort)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = ln
	s.running = true
	s.mu.Unlock()

	go s.serveLoop()
	return nil
}

func (s *ProxyServer) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	if s.listener != nil {
		_ = s.listener.Close()
	}
	close(s.closedChan)
	s.mu.Unlock()
}

func (s *ProxyServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *ProxyServer) serveLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			s.mu.RLock()
			running := s.running
			s.mu.RUnlock()
			if !running {
				return
			}
			continue
		}

		// Check LAN ACL
		if !s.isClientAllowed(conn.RemoteAddr()) {
			_ = conn.Close()
			continue
		}

		go s.handleConnection(conn)
	}
}

func (s *ProxyServer) isClientAllowed(addr net.Addr) bool {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip := tcpAddr.IP
	if ip.IsLoopback() {
		return true
	}

	cfg := s.cfgMgr.Get()
	if !cfg.AllowLAN {
		return false
	}

	// Permitted LAN ranges: RFC 1918, link-local, ULA
	if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return false
}

func (s *ProxyServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	// 1. CONNECT tunnel (HTTPS proxy request)
	if req.Method == http.MethodConnect {
		s.handleConnect(conn, br, req)
		return
	}

	// 2. Plain HTTP request (ca.crt, proxy.pac, mobile landing, or plain proxy)
	s.handlePlainHTTP(conn, req)
}

func (s *ProxyServer) handleConnect(conn net.Conn, br *bufio.Reader, req *http.Request) {
	target := req.RequestURI
	host := target
	if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimRight(host, "."))

	// Send 200 Connection Established
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	// Check if this host should be MITM'd
	if !isGBFDomain(host) {
		// Passthrough tunnel
		s.handlePassthroughTunnel(conn, target)
		return
	}

	// MITM TLS termination
	tlsConfig := s.certMgr.GetTLSConfig()
	tlsConn := tls.Server(conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		return
	}
	defer tlsConn.Close()

	tlsReader := bufio.NewReader(tlsConn)
	for {
		clientReq, err := http.ReadRequest(tlsReader)
		if err != nil {
			break
		}

		keepAlive := s.handleDecryptedRequest(tlsConn, clientReq, host)
		if !keepAlive || clientReq.Close {
			break
		}
	}
}

func (s *ProxyServer) handlePassthroughTunnel(clientConn net.Conn, target string) {
	upConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		return
	}
	defer upConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, upConn)
		done <- struct{}{}
	}()
	<-done
}

func (s *ProxyServer) handlePlainHTTP(conn net.Conn, req *http.Request) {
	cleanPath := strings.ToLower(strings.Split(req.URL.Path, "?")[0])

	// 1. Root CA Certificate download
	if cleanPath == "/ca.crt" || cleanPath == "/ca.pem" || strings.HasSuffix(cleanPath, "/ca.crt") {
		caBytes := s.certMgr.GetCAPEM()
		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"Content-Type: application/x-x509-ca-cert\r\n"+
			"Content-Length: %d\r\n"+
			"Content-Disposition: attachment; filename=\"gbf_ca.crt\"\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Cache-Control: no-cache\r\n"+
			"Connection: close\r\n\r\n", len(caBytes))
		_, _ = conn.Write([]byte(res))
		_, _ = conn.Write(caBytes)
		return
	}

	// 2. Dynamic PAC script
	if cleanPath == "/proxy.pac" || strings.HasSuffix(cleanPath, "/proxy.pac") {
		cfg := s.cfgMgr.Get()
		host := "127.0.0.1"
		if req.Host != "" {
			h, _, err := net.SplitHostPort(req.Host)
			if err == nil && h != "127.0.0.1" && h != "localhost" {
				host = h
			}
		}
		if cfg.AllowLAN && host == "127.0.0.1" {
			if lanIP := config.GetLANIP(); lanIP != "" {
				host = lanIP
			}
		}
		pacText := GetPAC(host, cfg.ListenPort)
		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"Content-Type: application/x-ns-proxy-autoconfig\r\n"+
			"Content-Length: %d\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Cache-Control: no-cache\r\n"+
			"Connection: close\r\n\r\n", len(pacText))
		_, _ = conn.Write([]byte(res))
		_, _ = conn.Write([]byte(pacText))
		return
	}

	// 3. Mobile LAN landing guide (only for direct requests to proxy host itself)
	isDirectLocal := !req.URL.IsAbs() && (req.Host == "" ||
		strings.HasPrefix(req.Host, "127.0.0.1") ||
		strings.HasPrefix(req.Host, "localhost") ||
		(s.cfgMgr.Get().AllowLAN && strings.HasPrefix(req.Host, config.GetLANIP())))

	if isDirectLocal && (cleanPath == "" || cleanPath == "/" || cleanPath == "/index.html") {
		cfg := s.cfgMgr.Get()
		lanIP := config.GetLANIP()
		if lanIP == "" {
			lanIP = "127.0.0.1"
		}
		html := GetLandingHTML(lanIP, cfg.ListenPort)
		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"Content-Type: text/html; charset=utf-8\r\n"+
			"Content-Length: %d\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Connection: close\r\n\r\n", len(html))
		_, _ = conn.Write([]byte(res))
		_, _ = conn.Write([]byte(html))
		return
	}

	// 4. Plain HTTP proxy request (e.g. GET http://gbf.game.mbga.jp/)
	s.forwardPlainProxy(conn, req)
}

func (s *ProxyServer) forwardPlainProxy(conn net.Conn, req *http.Request) {
	upURL := req.URL.String()
	if !strings.HasPrefix(upURL, "http://") && !strings.HasPrefix(upURL, "https://") {
		host := req.Host
		if host == "" {
			host = "gbf.game.mbga.jp"
		}
		upURL = "http://" + host + req.URL.RequestURI()
	}

	bodyBytes, _ := io.ReadAll(req.Body)
	upReq, err := http.NewRequestWithContext(context.Background(), req.Method, upURL, bytes.NewReader(bodyBytes))
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}

	for k, vv := range req.Header {
		if isHopByHop(strings.ToLower(k)) {
			continue
		}
		for _, v := range vv {
			upReq.Header.Add(k, v)
		}
	}
	upReq.Host = req.Host

	resp, err := s.getAPIClient().Do(upReq)
	if err != nil {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	s.forwardDynamicResponse(conn, resp, respBytes, req.Method == http.MethodHead)
}

func (s *ProxyServer) handleDecryptedRequest(w io.Writer, req *http.Request, targetHost string) bool {
	// Rule 1: CORS Preflight
	if req.Method == http.MethodOptions {
		_, _ = fmt.Fprintf(w, "HTTP/1.1 200 OK\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS\r\n"+
			"Access-Control-Allow-Headers: *\r\n"+
			"Access-Control-Max-Age: 604800\r\n"+
			"Content-Length: 0\r\n"+
			"Connection: keep-alive\r\n\r\n")
		return true
	}

	// Rule 2: Static Asset vs Dynamic Game API
	isStatic := (req.Method == http.MethodGet || req.Method == http.MethodHead) && isStaticTarget(targetHost, req.URL.Path)
	if isStatic {
		return s.handleStaticAsset(w, req, targetHost)
	}

	return s.handleDynamicAPI(w, req, targetHost)
}

func (s *ProxyServer) handleStaticAsset(w io.Writer, req *http.Request, targetHost string) bool {
	cleanPath := strings.Split(req.URL.Path, "?")[0]

	// Security: Reject path traversal sequences immediately
	reqURILower := strings.ToLower(req.RequestURI)
	pathLower := strings.ToLower(req.URL.Path)
	if strings.Contains(req.RequestURI, "..") ||
		strings.Contains(req.URL.Path, "..") ||
		strings.Contains(reqURILower, "windows") ||
		strings.Contains(pathLower, "windows") ||
		strings.Contains(reqURILower, "passwd") ||
		strings.Contains(pathLower, "passwd") ||
		strings.Contains(cleanPath, ":") {
		_, _ = fmt.Fprintf(w, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: keep-alive\r\n\r\n")
		return true
	}

	s.stats.IncAsset()
	s.stats.AddActiveFG(1)
	defer s.stats.AddActiveFG(-1)

	isHead := (req.Method == http.MethodHead)
	reqETag := req.Header.Get("If-None-Match")

	// 1. Cache Lookup (RAM first, then SSD)
	item, hitSrc := s.cacheMgr.Get(req.URL.Path)
	if item != nil {
		if hitSrc == "RAM" {
			s.stats.IncRAMHit()
		} else {
			s.stats.IncDiskHit()
		}

		// Conditional GET: 304 Not Modified check
		if matchETag(reqETag, item.ETag) {
			_, _ = fmt.Fprintf(w, "HTTP/1.1 304 Not Modified\r\n"+
				"ETag: %s\r\n"+
				"Access-Control-Allow-Origin: *\r\n"+
				"Connection: keep-alive\r\n\r\n", item.ETag)
			return true
		}

		s.sendAssetResponse(w, 200, item, isHead)
		return true
	}

	// 2. Cache Miss: Coalesce concurrent fetches using SingleFlight
	s.stats.IncMiss()
	flightKey := targetHost + cleanPath

	res, err := s.cacheMgr.SingleFlight().Do(flightKey, func() (interface{}, error) {
		upURL := fmt.Sprintf("https://%s%s", targetHost, req.URL.RequestURI())
		upReq, err := http.NewRequestWithContext(context.Background(), "GET", upURL, nil)
		if err != nil {
			return nil, err
		}

		for k, vv := range req.Header {
			if isHopByHop(strings.ToLower(k)) {
				continue
			}
			for _, v := range vv {
				upReq.Header.Add(k, v)
			}
		}
		upReq.Host = targetHost

		resp, err := s.getAssetClient().Do(upReq)
		if err != nil || resp.StatusCode != http.StatusOK {
			// Check offline cache fallback
			if fb, _ := s.cacheMgr.GetFallback(req.URL.Path); fb != nil {
				return fb, nil
			}
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}

		ct := resp.Header.Get("Content-Type")
		if !cache.IsValidCacheContent(cleanPath, ct, data) {
			// Upstream HTML error page (e.g. 502/503) or malformed content
			if fb, _ := s.cacheMgr.GetFallback(req.URL.Path); fb != nil {
				return fb, nil
			}
			return nil, fmt.Errorf("invalid cache content")
		}

		etag := resp.Header.Get("ETag")
		if etag == "" {
			etag = resp.Header.Get("Etag")
		}
		ce := resp.Header.Get("Content-Encoding")

		// Persist to cache with lowercase header keys
		headersMap := make(map[string]string)
		for k, vv := range resp.Header {
			if len(vv) > 0 {
				headersMap[strings.ToLower(k)] = vv[0]
			}
		}
		if etag != "" {
			headersMap["etag"] = etag
		}
		s.cacheMgr.Save(req.URL.Path, headersMap, data)

		return &cache.CacheItem{
			Data:            data,
			ContentType:     ct,
			ContentEncoding: ce,
			ETag:            etag,
			Size:            int64(len(data)),
		}, nil
	})

	if err != nil || res == nil {
		_, _ = fmt.Fprintf(w, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: keep-alive\r\n\r\n")
		return true
	}

	cachedItem := res.(*cache.CacheItem)
	if matchETag(reqETag, cachedItem.ETag) {
		_, _ = fmt.Fprintf(w, "HTTP/1.1 304 Not Modified\r\n"+
			"ETag: %s\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Connection: keep-alive\r\n\r\n", cachedItem.ETag)
		return true
	}

	s.sendAssetResponse(w, 200, cachedItem, isHead)
	return true
}

func matchETag(clientETag, serverETag string) bool {
	if clientETag == "" || serverETag == "" {
		return false
	}
	c := strings.Trim(strings.TrimPrefix(clientETag, "W/"), "\"")
	s := strings.Trim(strings.TrimPrefix(serverETag, "W/"), "\"")
	return c == s || clientETag == serverETag
}

func (s *ProxyServer) handleDynamicAPI(w io.Writer, req *http.Request, targetHost string) bool {
	s.stats.IncAPI()
	s.stats.AddActiveAPI(1)
	defer s.stats.AddActiveAPI(-1)

	bodyBytes, _ := io.ReadAll(req.Body)
	cleanPath := strings.ToLower(strings.Split(req.URL.Path, "?")[0])

	// Strict P0 Safe Retry Rule:
	// Only read-only idempotent GET requests to whitelisted paths may be retried on connection drop.
	// All POST/PUT/DELETE requests have maxAttempts = 1 (strictly zero retry).
	isRetryable := (req.Method == http.MethodGet && isRetryableAPI(cleanPath))
	maxAttempts := 1
	if isRetryable {
		maxAttempts = 2
	}

	upURL := fmt.Sprintf("https://%s%s", targetHost, req.URL.RequestURI())
	var resp *http.Response
	var fetchErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		upReq, err := http.NewRequestWithContext(context.Background(), req.Method, upURL, bytes.NewReader(bodyBytes))
		if err != nil {
			fetchErr = err
			break
		}

		for k, vv := range req.Header {
			if isHopByHop(strings.ToLower(k)) {
				continue
			}
			for _, v := range vv {
				upReq.Header.Add(k, v)
			}
		}
		upReq.Host = targetHost

		resp, fetchErr = s.getAPIClient().Do(upReq)
		if fetchErr == nil {
			break
		}
		if attempt+1 < maxAttempts {
			s.stats.IncAPIRetry()
			continue
		}
	}

	if fetchErr != nil || resp == nil {
		_, _ = fmt.Fprintf(w, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: keep-alive\r\n\r\n")
		return true
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	s.forwardDynamicResponse(w, resp, respBytes, req.Method == http.MethodHead)
	return true
}

func (s *ProxyServer) sendAssetResponse(w io.Writer, status int, item *cache.CacheItem, isHead bool) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "HTTP/1.1 %d OK\r\n", status)
	if item.ContentType != "" {
		fmt.Fprintf(&buf, "Content-Type: %s\r\n", item.ContentType)
	}
	fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(item.Data))
	if item.ETag != "" {
		fmt.Fprintf(&buf, "ETag: %s\r\n", item.ETag)
	}
	if item.ContentEncoding != "" {
		fmt.Fprintf(&buf, "Content-Encoding: %s\r\n", item.ContentEncoding)
	}
	// Permitted standard headers
	buf.WriteString("Access-Control-Allow-Origin: *\r\n")
	buf.WriteString("Cache-Control: public, max-age=31536000, immutable\r\n")
	buf.WriteString("Connection: keep-alive\r\n\r\n")

	if isHead {
		_, _ = w.Write(buf.Bytes())
	} else {
		_, _ = w.Write(buf.Bytes())
		_, _ = w.Write(item.Data)
	}
}

func (s *ProxyServer) forwardDynamicResponse(w io.Writer, resp *http.Response, body []byte, isHead bool) {
	var buf bytes.Buffer
	statusPhrase := http.StatusText(resp.StatusCode)
	if statusPhrase == "" {
		statusPhrase = "OK"
	}
	fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", resp.StatusCode, statusPhrase)

	// Write upstream headers preserving original case and CORS
	for k, vv := range resp.Header {
		kLower := strings.ToLower(k)
		if isHopByHop(kLower) || kLower == "content-length" || kLower == "set-cookie" {
			continue
		}
		// P0: Zero header pollution
		if strings.HasPrefix(kLower, "x-proxy-") || strings.HasPrefix(kLower, "x-cache-") {
			continue
		}
		for _, v := range vv {
			fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
		}
	}

	// P0: Multi-line Set-Cookie preservation (never fold into single line with commas)
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		fmt.Fprintf(&buf, "Set-Cookie: %s\r\n", cookie)
	}

	fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(body))
	buf.WriteString("Connection: keep-alive\r\n\r\n")

	if isHead {
		_, _ = w.Write(buf.Bytes())
	} else {
		_, _ = w.Write(buf.Bytes())
		_, _ = w.Write(body)
	}
}

func isHopByHop(h string) bool {
	switch h {
	case "connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade":
		return true
	}
	return false
}

func isRetryableAPI(path string) bool {
	switch path {
	case "/rest/multiraid/condition.json",
		"/rest/quest/stage_list",
		"/rest/party/deck_info":
		return true
	}
	return false
}

func isAkamaiHost(host string) bool {
	return strings.HasSuffix(host, ".akamaized.net")
}

func isGBFDomain(host string) bool {
	if isAkamaiHost(host) {
		return true
	}
	return strings.HasSuffix(host, "granbluefantasy.jp") ||
		strings.HasSuffix(host, "granbluefantasy.com") ||
		strings.HasSuffix(host, "mbga.jp") ||
		host == "localhost" || host == "127.0.0.1"
}

func isStaticTarget(host, path string) bool {
	p := strings.ToLower(path)

	// Dynamic prefixes are strictly non-static
	dynamicPrefixes := []string{
		"/rest/", "/quest/", "/party/", "/user/",
		"/deck/", "/gacha/", "/casino/", "/mypage/", "/ob/",
	}
	for _, dp := range dynamicPrefixes {
		if strings.HasPrefix(p, dp) {
			return false
		}
	}

	if isAkamaiHost(host) || strings.HasPrefix(host, "game-a") {
		return true
	}

	if strings.HasPrefix(p, "/assets/") || strings.HasPrefix(p, "/assets_en/") {
		return true
	}

	staticExts := []string{
		".png", ".jpg", ".jpeg", ".webp", ".gif", ".css", ".js",
		".mp3", ".wav", ".wasm", ".woff", ".woff2", ".ttf", ".otf",
	}
	clean := strings.Split(p, "?")[0]
	for _, ext := range staticExts {
		if strings.HasSuffix(clean, ext) {
			return true
		}
	}

	return false
}
