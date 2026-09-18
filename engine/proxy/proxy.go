package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

type bufferedConn struct {
	net.Conn
	r io.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

type ProxyServer struct {
	cfgMgr      *config.Manager
	certMgr     *cert.Manager
	cacheMgr    *cache.Manager
	stats       *telemetry.Stats
	prefetch    *PrefetchEngine
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
	s.prefetch = newPrefetchEngine(s)
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
		assetMaxConn = 32
	}
	assetMaxIdle := c.AssetMaxKeepalive
	if assetMaxIdle <= 0 {
		assetMaxIdle = 16
	}

	apiTransport := &http.Transport{
		Proxy: proxyFunc,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !c.VerifyUpstreamTLS,
		},
		MaxIdleConns:        apiMaxConn,
		MaxIdleConnsPerHost: apiMaxIdle,
		IdleConnTimeout:     time.Duration(c.APIKeepaliveExpiry) * time.Second,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   false, // Dedicated HTTP/1.1 pool for dynamic APIs
		DisableCompression: true,  // P0: Business semantic transparency
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
		MaxIdleConnsPerHost: assetMaxIdle,
		IdleConnTimeout:     time.Duration(c.AssetKeepaliveExpiry) * time.Second,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   true, // HTTP/2 multiplexed for Akamai CDN
		DisableCompression: true,  // P0: Byte-for-byte fidelity
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
	if s.prefetch != nil {
		s.prefetch.Stop()
	}
	close(s.closedChan)
	s.mu.Unlock()
}

func (s *ProxyServer) PrefetchQueueLen() int {
	if s.prefetch != nil {
		return s.prefetch.QueueLen()
	}
	return 0
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
	for {
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
		keepAlive := s.handlePlainHTTP(conn, req)
		if !keepAlive || req.Close {
			return
		}
	}
}

func (s *ProxyServer) handleConnect(conn net.Conn, br *bufio.Reader, req *http.Request) {
	target := req.RequestURI
	host := target
	if h, _, err := net.SplitHostPort(target); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimRight(host, "."))

	// Block telemetry tunnels immediately
	if isTelemetryHost(host) {
		_, _ = conn.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		s.stats.Log("BLOCK", fmt.Sprintf("Blocked telemetry tunnel: %s", target))
		return
	}

	// Check if this host should be MITM'd or Passthrough
	if isPassthroughHost(host) || !isGBFDomain(host) {
		s.handlePassthroughTunnel(conn, br, target)
		return
	}

	// Send 200 Connection Established for MITM
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	// MITM TLS termination using bufferedConn to preserve any early data in br
	bConn := &bufferedConn{Conn: conn, r: br}
	tlsConfig := s.certMgr.GetTLSConfig()
	tlsConn := tls.Server(bConn, tlsConfig)
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

func dialSOCKS5(proxyAddr, targetAddr, username, password string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer conn.SetDeadline(time.Time{})

	// 1. Handshake greeting
	methods := []byte{0x00}
	if username != "" {
		methods = []byte{0x00, 0x02}
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := conn.Write(greeting); err != nil {
		_ = conn.Close()
		return nil, err
	}

	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp[0] != 0x05 || resp[1] == 0xff {
		_ = conn.Close()
		return nil, fmt.Errorf("socks5 auth negotiation failed")
	}

	// 2. Auth if requested
	if resp[1] == 0x02 {
		authBuf := []byte{0x01, byte(len(username))}
		authBuf = append(authBuf, []byte(username)...)
		authBuf = append(authBuf, byte(len(password)))
		authBuf = append(authBuf, []byte(password)...)
		if _, err := conn.Write(authBuf); err != nil {
			_ = conn.Close()
			return nil, err
		}
		authResp := make([]byte, 2)
		if _, err := io.ReadFull(conn, authResp); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if authResp[1] != 0x00 {
			_ = conn.Close()
			return nil, fmt.Errorf("socks5 auth failed")
		}
	}

	// 3. Connect request: 0x05, 0x01 (CONNECT), 0x00 (RSV)
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		_ = conn.Close()
		return nil, fmt.Errorf("invalid target port: %s", portStr)
	}

	reqBuf := []byte{0x05, 0x01, 0x00}
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		reqBuf = append(reqBuf, 0x01)
		reqBuf = append(reqBuf, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		reqBuf = append(reqBuf, 0x04)
		reqBuf = append(reqBuf, ip6...)
	} else {
		reqBuf = append(reqBuf, 0x03, byte(len(host)))
		reqBuf = append(reqBuf, []byte(host)...)
	}
	reqBuf = append(reqBuf, byte(port>>8), byte(port&0xff))

	if _, err := conn.Write(reqBuf); err != nil {
		_ = conn.Close()
		return nil, err
	}

	replyHeader := make([]byte, 4)
	if _, err := io.ReadFull(conn, replyHeader); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if replyHeader[1] != 0x00 {
		_ = conn.Close()
		return nil, fmt.Errorf("socks5 connect failed with status 0x%02x", replyHeader[1])
	}

	// Drain bound address
	var toRead int
	switch replyHeader[3] {
	case 0x01:
		toRead = 4 + 2
	case 0x04:
		toRead = 16 + 2
	case 0x03:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			_ = conn.Close()
			return nil, err
		}
		toRead = int(lenBuf[0]) + 2
	default:
		_ = conn.Close()
		return nil, fmt.Errorf("socks5 unknown address type 0x%02x", replyHeader[3])
	}

	drainBuf := make([]byte, toRead)
	if _, err := io.ReadFull(conn, drainBuf); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return conn, nil
}

func (s *ProxyServer) handlePassthroughTunnel(clientConn net.Conn, clientReader io.Reader, target string) {
	effProxy := s.cfgMgr.GetEffectiveUpstreamProxy()

	targetHost := target
	if !strings.Contains(targetHost, ":") {
		targetHost += ":443"
	}

	var upConn net.Conn
	var upReader io.Reader
	var err error

	if effProxy != "" {
		if u, parseErr := url.Parse(effProxy); parseErr == nil {
			proxyAddr := u.Host
			scheme := strings.ToLower(u.Scheme)

			if strings.HasPrefix(scheme, "socks") {
				if !strings.Contains(proxyAddr, ":") {
					proxyAddr += ":1080"
				}
				user := ""
				pass := ""
				if u.User != nil {
					user = u.User.Username()
					pass, _ = u.User.Password()
				}
				upConn, err = dialSOCKS5(proxyAddr, targetHost, user, pass, 10*time.Second)
			} else if scheme == "http" || scheme == "https" {
				if !strings.Contains(proxyAddr, ":") {
					proxyAddr += ":80"
				}
				upConn, err = net.DialTimeout("tcp", proxyAddr, 10*time.Second)
				if err == nil {
					var authHeader string
					if u.User != nil {
						user := u.User.Username()
						pass, _ := u.User.Password()
						auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
						authHeader = fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
					}
					connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", targetHost, targetHost, authHeader)
					if _, wErr := upConn.Write([]byte(connectReq)); wErr != nil {
						_ = upConn.Close()
						upConn = nil
						err = wErr
					} else {
						br := bufio.NewReader(upConn)
						resp, rErr := http.ReadResponse(br, nil)
						if rErr != nil || resp.StatusCode != http.StatusOK {
							_ = upConn.Close()
							upConn = nil
							if rErr != nil {
								err = rErr
							} else {
								err = fmt.Errorf("proxy returned %d", resp.StatusCode)
							}
						} else {
							upReader = br
						}
					}
				}
			}
		}
	}

	if upConn == nil && err == nil {
		upConn, err = net.DialTimeout("tcp", targetHost, 10*time.Second)
	}

	if err != nil || upConn == nil {
		_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return
	}
	defer upConn.Close()

	if upReader == nil {
		upReader = upConn
	}

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	s.stats.Log("INFO", fmt.Sprintf("[BYPASS-TCP] Tunneling %s via upstream", targetHost))

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upConn, clientReader)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(clientConn, upReader)
		done <- struct{}{}
	}()
	<-done
}

func (s *ProxyServer) handlePlainHTTP(conn net.Conn, req *http.Request) bool {
	host := req.Host
	if h, _, err := net.SplitHostPort(req.Host); err == nil {
		host = h
	}
	if isTelemetryHost(host) {
		_, _ = conn.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
		return false
	}

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
		return false
	}

	// 2. Dynamic PAC script
	if cleanPath == "/proxy.pac" || strings.HasSuffix(cleanPath, "/proxy.pac") {
		cfg := s.cfgMgr.Get()
		h := "127.0.0.1"
		if req.Host != "" {
			hostNoPort, _, err := net.SplitHostPort(req.Host)
			if err == nil && hostNoPort != "127.0.0.1" && hostNoPort != "localhost" {
				h = hostNoPort
			}
		}
		if cfg.AllowLAN && h == "127.0.0.1" {
			if lanIP := config.GetLANIP(); lanIP != "" {
				h = lanIP
			}
		}
		pacText := GetPAC(h, cfg.ListenPort)
		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"Content-Type: application/x-ns-proxy-autoconfig\r\n"+
			"Content-Length: %d\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Cache-Control: no-cache\r\n"+
			"Connection: close\r\n\r\n", len(pacText))
		_, _ = conn.Write([]byte(res))
		_, _ = conn.Write([]byte(pacText))
		return false
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
		return false
	}

	// 4. Plain HTTP proxy request (e.g. GET http://gbf.game.mbga.jp/)
	if isStaticTarget(req.Host, req.URL.Path) && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		return s.handleStaticAsset(conn, req, req.Host)
	}
	return s.forwardPlainProxy(conn, req)
}

func (s *ProxyServer) forwardPlainProxy(conn net.Conn, req *http.Request) bool {
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
		return false
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
		return false
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	s.forwardDynamicResponse(conn, resp, respBytes, req.Method == http.MethodHead, req.Close)
	return !req.Close
}

func (s *ProxyServer) handleDecryptedRequest(w io.Writer, req *http.Request, targetHost string) bool {
	// Rule 1: CORS Preflight
	if req.Method == http.MethodOptions {
		connHdr := "keep-alive"
		if req.Close {
			connHdr = "close"
		}
		_, _ = fmt.Fprintf(w, "HTTP/1.1 200 OK\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS\r\n"+
			"Access-Control-Allow-Headers: *\r\n"+
			"Access-Control-Max-Age: 604800\r\n"+
			"Content-Length: 0\r\n"+
			"Connection: %s\r\n\r\n", connHdr)
		return !req.Close
	}

	// Rule 2: Static Asset vs Dynamic Game API
	isStatic := (req.Method == http.MethodGet || req.Method == http.MethodHead) && isStaticTarget(targetHost, req.URL.Path)
	if isStatic {
		return s.handleStaticAsset(w, req, targetHost)
	}

	return s.handleDynamicAPI(w, req, targetHost)
}

var reVersioned = regexp.MustCompile(`/(?:assets(?:_(?:en|jp))?)/\d+/|/\d{8,}/`)

func (s *ProxyServer) handleStaticAsset(w io.Writer, req *http.Request, targetHost string) bool {
	cleanPath := strings.Split(req.URL.Path, "?")[0]

	// Security: Reject path traversal sequences immediately
	unescapedURI, _ := url.PathUnescape(req.RequestURI)
	unescapedLower := strings.ToLower(unescapedURI)
	reqURILower := strings.ToLower(req.RequestURI)
	pathLower := strings.ToLower(req.URL.Path)
	cleanLower := strings.ToLower(cleanPath)
	if strings.Contains(req.RequestURI, "..") ||
		strings.Contains(req.URL.Path, "..") ||
		strings.Contains(unescapedURI, "..") ||
		strings.Contains(reqURILower, "windows") ||
		strings.Contains(pathLower, "windows") ||
		strings.Contains(unescapedLower, "windows") ||
		strings.Contains(reqURILower, "passwd") ||
		strings.Contains(pathLower, "passwd") ||
		strings.Contains(unescapedLower, "passwd") ||
		strings.Contains(cleanPath, ":") ||
		strings.Contains(unescapedURI, ":") ||
		strings.HasPrefix(cleanLower, "\\") ||
		strings.HasPrefix(unescapedLower, "\\") {
		connHdr := "keep-alive"
		if req.Close {
			connHdr = "close"
		}
		_, _ = fmt.Fprintf(w, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: %s\r\n\r\n", connHdr)
		return !req.Close
	}

	s.stats.IncAsset()
	s.stats.AddActiveFG(1)
	defer s.stats.AddActiveFG(-1)

	isHead := (req.Method == http.MethodHead)
	reqETag := req.Header.Get("If-None-Match")

	// 1. Cache Lookup (RAM first, then SSD)
	item, hitSrc := s.cacheMgr.Get(req.URL.Path)
	if item != nil {
		s.stats.CheckAndRecordPrefetchReused(cleanPath)
		if hitSrc == "RAM" {
			s.stats.IncRAMHit()
			s.stats.Log("INFO", fmt.Sprintf("[CACHE-RAM] HIT -> %s (%d B)", cleanPath, len(item.Data)))
		} else {
			s.stats.IncDiskHit()
			s.stats.Log("INFO", fmt.Sprintf("[CACHE-DISK] HIT -> %s (%d B)", cleanPath, len(item.Data)))
		}

		// Conditional GET: 304 Not Modified check
		if matchETag(reqETag, item.ETag) {
			connHdr := "keep-alive"
			if req.Close {
				connHdr = "close"
			}
			_, _ = fmt.Fprintf(w, "HTTP/1.1 304 Not Modified\r\n"+
				"ETag: %s\r\n"+
				"Access-Control-Allow-Origin: *\r\n"+
				"Connection: %s\r\n\r\n", item.ETag, connHdr)
			return !req.Close
		}

		s.sendAssetResponse(w, 200, item, isHead, req.URL.Path, req.Close)
		return !req.Close
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
		if s.prefetch != nil {
			s.prefetch.MaybeEnqueueDiscovery(targetHost, req.URL.Path, data)
		}
		s.stats.Log("INFO", fmt.Sprintf("[FETCH-ASSET] 200 OK -> %s (%d B)", cleanPath, len(data)))

		return &cache.CacheItem{
			Data:            data,
			ContentType:     ct,
			ContentEncoding: ce,
			ETag:            etag,
			Size:            int64(len(data)),
		}, nil
	})

	if err != nil || res == nil {
		connHdr := "keep-alive"
		if req.Close {
			connHdr = "close"
		}
		_, _ = fmt.Fprintf(w, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: %s\r\n\r\n", connHdr)
		return !req.Close
	}

	cachedItem := res.(*cache.CacheItem)
	if matchETag(reqETag, cachedItem.ETag) {
		connHdr := "keep-alive"
		if req.Close {
			connHdr = "close"
		}
		_, _ = fmt.Fprintf(w, "HTTP/1.1 304 Not Modified\r\n"+
			"ETag: %s\r\n"+
			"Access-Control-Allow-Origin: *\r\n"+
			"Connection: %s\r\n\r\n", cachedItem.ETag, connHdr)
		return !req.Close
	}

	s.sendAssetResponse(w, 200, cachedItem, isHead, req.URL.Path, req.Close)
	return !req.Close
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

	startTime := time.Now()
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
		connHdr := "keep-alive"
		if req.Close {
			connHdr = "close"
		}
		_, _ = fmt.Fprintf(w, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: %s\r\n\r\n", connHdr)
		return !req.Close
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	s.forwardDynamicResponse(w, resp, respBytes, req.Method == http.MethodHead, req.Close)
	elapsed := time.Since(startTime).Milliseconds()
	s.stats.Log("INFO", fmt.Sprintf("[BYPASS-API] %d %s %s%s (%dms)", resp.StatusCode, req.Method, targetHost, req.URL.Path, elapsed))
	return !req.Close
}

func (s *ProxyServer) sendAssetResponse(w io.Writer, status int, item *cache.CacheItem, isHead bool, urlPath string, reqClose bool) {
	cfg := s.cfgMgr.Get()
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
	buf.WriteString("Access-Control-Allow-Origin: *\r\n")

	// P1: Safe browser cache policy - immutable ONLY for versioned assets
	if !cfg.EnableBrowserCache {
		buf.WriteString("Cache-Control: no-cache\r\n")
	} else if reVersioned.MatchString(urlPath) {
		buf.WriteString("Cache-Control: public, max-age=31536000, immutable\r\n")
	} else {
		buf.WriteString("Cache-Control: public, max-age=3600\r\n")
	}
	if reqClose {
		buf.WriteString("Connection: close\r\n\r\n")
	} else {
		buf.WriteString("Connection: keep-alive\r\n\r\n")
	}

	if isHead {
		_, _ = w.Write(buf.Bytes())
	} else {
		_, _ = w.Write(buf.Bytes())
		_, _ = w.Write(item.Data)
	}
}

func (s *ProxyServer) forwardDynamicResponse(w io.Writer, resp *http.Response, body []byte, isHead bool, reqClose bool) {
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

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		fmt.Fprintf(&buf, "Content-Length: %d\r\n", len(body))
	}
	if reqClose {
		buf.WriteString("Connection: close\r\n\r\n")
	} else {
		buf.WriteString("Connection: keep-alive\r\n\r\n")
	}

	if isHead || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
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

var telemetryPatterns = []string{
	"smbeat.jp",
	"smrtbeat.com",
	"rcv.a-i-ad.com",
	"datadoghq-browser-agent",
	"datadoghq.com",
	"spdmg-backend.i-mobile.co.jp",
	"creativecdn.com",
}

func isTelemetryHost(host string) bool {
	h := strings.ToLower(host)
	for _, pat := range telemetryPatterns {
		if strings.Contains(h, pat) {
			return true
		}
	}
	return false
}

func isPassthroughHost(host string) bool {
	h := strings.ToLower(host)
	return h == "ws.game.granbluefantasy.jp" || strings.HasPrefix(h, "ws.game.")
}

func isDomainOrSubdomain(host, domain string) bool {
	h := strings.ToLower(strings.TrimRight(host, "."))
	d := strings.ToLower(strings.TrimRight(domain, "."))
	return h == d || strings.HasSuffix(h, "."+d)
}

func isGBFAkamaiHost(host string) bool {
	h := strings.ToLower(strings.TrimRight(host, "."))
	if !strings.HasSuffix(h, ".akamaized.net") {
		return false
	}
	switch h {
	case "prd-game-a-granbluefantasy.akamaized.net",
		"prd-game-a1-granbluefantasy.akamaized.net",
		"prd-game-a2-granbluefantasy.akamaized.net",
		"prd-game-a3-granbluefantasy.akamaized.net",
		"prd-game-a4-granbluefantasy.akamaized.net",
		"prd-game-a5-granbluefantasy.akamaized.net",
		"prd-game-a-granbluefantasy-steam.akamaized.net",
		"prd-game-a1-granbluefantasy-steam.akamaized.net",
		"prd-game-a2-granbluefantasy-steam.akamaized.net",
		"prd-game-a3-granbluefantasy-steam.akamaized.net",
		"prd-game-a4-granbluefantasy-steam.akamaized.net",
		"prd-game-a5-granbluefantasy-steam.akamaized.net":
		return true
	}
	if isDomainOrSubdomain(h, "granbluefantasy.akamaized.net") ||
		isDomainOrSubdomain(h, "gbf.akamaized.net") {
		return true
	}
	return strings.Contains(h, "granbluefantasy")
}

func isGBFDomain(host string) bool {
	if isPassthroughHost(host) {
		return false
	}
	if isGBFAkamaiHost(host) {
		return true
	}
	return isDomainOrSubdomain(host, "granbluefantasy.jp") ||
		isDomainOrSubdomain(host, "granbluefantasy.com") ||
		isDomainOrSubdomain(host, "mbga.jp") ||
		isDomainOrSubdomain(host, "mobage.jp") ||
		host == "localhost" || host == "127.0.0.1"
}

func isStaticTarget(host, path string) bool {
	p := strings.ToLower(path)

	// Dynamic prefixes are strictly non-static
	dynamicPrefixes := []string{
		"/rest/", "/quest/", "/party/", "/user/",
		"/deck/", "/gacha/", "/casino/", "/present/",
		"/mypage/", "/guild/", "/coopraid/", "/weapon/",
		"/socket/", "/ob/",
	}
	for _, dp := range dynamicPrefixes {
		if strings.HasPrefix(p, dp) {
			return false
		}
	}

	if isGBFAkamaiHost(host) || strings.HasPrefix(host, "game-a") {
		return true
	}

	staticPrefixes := []string{
		"/assets/", "/assets_en/", "/sound/", "/img/", "/css/", "/js/", "/font/",
	}
	for _, sp := range staticPrefixes {
		if strings.HasPrefix(p, sp) {
			return true
		}
	}

	staticExts := []string{
		".png", ".jpg", ".jpeg", ".webp", ".gif", ".css", ".js",
		".mp3", ".wav", ".ogg", ".m4a", ".mp4", ".webm",
		".wasm", ".woff", ".woff2", ".ttf", ".otf", ".svg", ".ico",
	}
	clean := strings.Split(p, "?")[0]
	for _, ext := range staticExts {
		if strings.HasSuffix(clean, ext) {
			return true
		}
	}

	return false
}
