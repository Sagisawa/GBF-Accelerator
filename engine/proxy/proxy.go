package proxy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	listenerGen uint64
	serveLoopWg sync.WaitGroup
	mu          sync.RWMutex
	apiClient   atomic.Pointer[http.Client]
	assetClient atomic.Pointer[http.Client]
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

	oldAPI := s.apiClient.Load()
	oldAsset := s.assetClient.Load()

	var proxyFunc func(*http.Request) (*url.URL, error)
	effProxy := c.GetEffectiveUpstreamProxy()
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

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 15 * time.Second,
	}

	apiTransport := &http.Transport{
		Proxy:       proxyFunc,
		DialContext: dialer.DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !c.VerifyUpstreamTLS || c.ShimakazeMode,
		},
		MaxConnsPerHost:     apiMaxConn,
		MaxIdleConns:        apiMaxConn,
		MaxIdleConnsPerHost: apiMaxIdle,
		IdleConnTimeout:     time.Duration(c.APIKeepaliveExpiry * float64(time.Second)),
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   false, // Dedicated HTTP/1.1 pool for dynamic APIs
		DisableCompression: true,  // P0: Business semantic transparency
	}
	newAPI := &http.Client{
		Transport: apiTransport,
		Timeout:   45 * time.Second,
	}
	s.apiClient.Store(newAPI)

	assetTransport := &http.Transport{
		Proxy:       proxyFunc,
		DialContext: dialer.DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: !c.VerifyUpstreamTLS || c.ShimakazeMode,
		},
		MaxConnsPerHost:     assetMaxConn,
		MaxIdleConns:        assetMaxConn,
		MaxIdleConnsPerHost: assetMaxIdle,
		IdleConnTimeout:     time.Duration(c.AssetKeepaliveExpiry * float64(time.Second)),
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   true, // HTTP/2 multiplexed for Akamai CDN
		DisableCompression: true,  // P0: Byte-for-byte fidelity
	}
	newAsset := &http.Client{
		Transport: assetTransport,
		Timeout:   45 * time.Second,
	}
	s.assetClient.Store(newAsset)

	if oldAPI != nil {
		if tr, ok := oldAPI.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
	if oldAsset != nil {
		if tr, ok := oldAsset.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
}

func (s *ProxyServer) getAPIClient() *http.Client {
	return s.apiClient.Load()
}

func (s *ProxyServer) getAssetClient() *http.Client {
	return s.assetClient.Load()
}

// tracedRequest attaches a non-intrusive httptrace hook that records real
// connection reuse for telemetry. It never mutates the request or its bytes.
// The negotiated protocol is recorded separately from the response.
func (s *ProxyServer) telemetryEnabled() bool {
	return s.cfgMgr != nil && s.cfgMgr.Get().EnableAPITelemetry
}

func (s *ProxyServer) tracedRequest(req *http.Request) *http.Request {
	if !s.telemetryEnabled() {
		return req
	}
	return s.tracedRequestWithCallback(req, nil)
}

func (s *ProxyServer) tracedRequestWithCallback(req *http.Request, onGotConn func(reused bool)) *http.Request {
	if !s.telemetryEnabled() {
		return req
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			s.stats.RecordConnReuse(info.Reused)
			if onGotConn != nil {
				onGotConn(info.Reused)
			}
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
}


func (s *ProxyServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}

	cfg := s.cfgMgr.Get()
	bindHost := s.cfgMgr.GetEffectiveListenHost()
	addr := fmt.Sprintf("%s:%d", bindHost, cfg.ListenPort)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	s.listener = ln
	s.listenerGen++
	s.running = true
	s.closedChan = make(chan struct{})
	if s.prefetch == nil || s.prefetch.IsStopped() {
		s.prefetch = newPrefetchEngine(s)
	}

	s.serveLoopWg.Add(1)
	go s.serveLoop(ln, s.listenerGen)
	return nil
}

func (s *ProxyServer) ReloadListener(newHost string, newPort int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return nil
	}

	newAddr := fmt.Sprintf("%s:%d", newHost, newPort)
	if s.listener != nil && s.listener.Addr().String() == newAddr {
		return nil
	}

	oldLn := s.listener
	var oldAddr string
	if oldLn != nil {
		oldAddr = oldLn.Addr().String()
		_ = oldLn.Close()
	}

	newLn, err := net.Listen("tcp", newAddr)
	if err != nil {
		// Rollback to old listener if available
		if oldAddr != "" {
			if rollbackLn, rErr := net.Listen("tcp", oldAddr); rErr == nil {
				s.listener = rollbackLn
				s.listenerGen++
				s.serveLoopWg.Add(1)
				go s.serveLoop(rollbackLn, s.listenerGen)
				return fmt.Errorf("failed to listen on %s: %w (rolled back to %s)", newAddr, err, oldAddr)
			} else {
				s.listener = nil
				return fmt.Errorf("failed to listen on %s: %v (rollback to %s failed: %v)", newAddr, err, oldAddr, rErr)
			}
		}
		return fmt.Errorf("failed to listen on %s: %w", newAddr, err)
	}

	s.listener = newLn
	s.listenerGen++
	s.serveLoopWg.Add(1)
	go s.serveLoop(newLn, s.listenerGen)
	if s.stats != nil {
		s.stats.Log("INFO", fmt.Sprintf("[PROXY] 监听地址已动态重载至: %s (generation: %d)", newAddr, s.listenerGen))
	}
	return nil
}

func (s *ProxyServer) ListenerAddr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return ""
}

func (s *ProxyServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	s.listenerGen++
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
	if s.prefetch != nil {
		s.prefetch.Stop()
	}
	if api := s.apiClient.Load(); api != nil {
		if tr, ok := api.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
	if asset := s.assetClient.Load(); asset != nil {
		if tr, ok := asset.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
	select {
	case <-s.closedChan:
	default:
		close(s.closedChan)
	}
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

func (s *ProxyServer) WaitServeLoops(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serveLoopWg.Wait()
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *ProxyServer) serveLoop(ln net.Listener, gen uint64) {
	defer s.serveLoopWg.Done()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.RLock()
			running := s.running
			currentGen := s.listenerGen
			s.mu.RUnlock()

			if !running || gen != currentGen {
				return
			}
			time.Sleep(20 * time.Millisecond)
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
		writeHTTPResponse(conn, http.StatusForbidden, nil, nil, false, true)
		s.stats.Log("BLOCK", fmt.Sprintf("Blocked telemetry tunnel: %s", target))
		return
	}

	clientIP := "127.0.0.1"
	if cAddr := conn.RemoteAddr(); cAddr != nil {
		if ch, _, err := net.SplitHostPort(cAddr.String()); err == nil {
			clientIP = ch
		}
	}

	// Check if this host should be MITM'd or Passthrough
	if isPassthroughHost(host) || !isGBFDomain(host) {
		s.stats.Log("INFO", fmt.Sprintf("[CONNECT] [%s] %s -> TUNNEL", clientIP, target))
		s.handlePassthroughTunnel(conn, br, target)
		return
	}

	s.stats.Log("INFO", fmt.Sprintf("[CONNECT] [%s] %s -> MITM", clientIP, target))

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

var tunnelBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

func (s *ProxyServer) handlePassthroughTunnel(clientConn net.Conn, clientReader io.Reader, target string) {
	effProxy := s.cfgMgr.GetEffectiveUpstreamProxy()

	targetHost := target
	if _, _, err := net.SplitHostPort(targetHost); err != nil {
		targetHost = net.JoinHostPort(strings.Trim(targetHost, "[]"), "443")
	}

	var upConn net.Conn
	var upReader io.Reader
	var err error

	if effProxy != "" {
		if u, parseErr := url.Parse(effProxy); parseErr == nil {
			scheme := strings.ToLower(u.Scheme)

			if strings.HasPrefix(scheme, "socks") {
				proxyHost := u.Hostname()
				proxyPort := u.Port()
				if proxyPort == "" {
					proxyPort = "1080"
				}
				proxyAddr := net.JoinHostPort(proxyHost, proxyPort)
				user := ""
				pass := ""
				if u.User != nil {
					user = u.User.Username()
					pass, _ = u.User.Password()
				}
				upConn, err = dialSOCKS5(proxyAddr, targetHost, user, pass, 10*time.Second)
			} else if scheme == "http" || scheme == "https" {
				proxyHost := u.Hostname()
				proxyPort := u.Port()
				if proxyPort == "" {
					if scheme == "https" {
						proxyPort = "443"
					} else {
						proxyPort = "80"
					}
				}
				proxyAddr := net.JoinHostPort(proxyHost, proxyPort)
				upConn, err = net.DialTimeout("tcp", proxyAddr, 10*time.Second)
				if err == nil && scheme == "https" {
					cfg := s.cfgMgr.Get()
					tlsCfg := &tls.Config{
						ServerName:         proxyHost,
						InsecureSkipVerify: !cfg.VerifyUpstreamTLS || cfg.ShimakazeMode,
					}
					tlsConn := tls.Client(upConn, tlsCfg)
					_ = upConn.SetDeadline(time.Now().Add(10 * time.Second))
					if hErr := tlsConn.Handshake(); hErr != nil {
						_ = upConn.Close()
						upConn = nil
						err = hErr
					} else {
						_ = upConn.SetDeadline(time.Time{})
						upConn = tlsConn
					}
				}
				if err == nil && upConn != nil {
					var authHeader string
					if u.User != nil {
						user := u.User.Username()
						pass, _ := u.User.Password()
						auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
						authHeader = fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
					}
					connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n%s\r\n", targetHost, targetHost, authHeader)
					_ = upConn.SetDeadline(time.Now().Add(10 * time.Second))
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
							_ = upConn.SetDeadline(time.Time{})
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
		writeHTTPResponse(clientConn, http.StatusBadGateway, nil, nil, false, true)
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

	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = clientConn.Close()
			_ = upConn.Close()
		})
	}
	defer closeBoth()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		defer closeBoth()
		bufPtr := tunnelBufferPool.Get().(*[]byte)
		defer tunnelBufferPool.Put(bufPtr)
		_, _ = io.CopyBuffer(clientConn, upReader, *bufPtr)
	}()

	go func() {
		defer wg.Done()
		defer closeBoth()
		bufPtr := tunnelBufferPool.Get().(*[]byte)
		defer tunnelBufferPool.Put(bufPtr)
		_, _ = io.CopyBuffer(upConn, clientReader, *bufPtr)
	}()

	wg.Wait()
}

func isPrivateProxyHost(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.To4() != nil && ip.IsPrivate()
}

func safeLocalAddr(conn net.Conn) (localIP string, localPort string) {
	if conn == nil {
		return "", ""
	}
	defer func() {
		_ = recover()
	}()
	if la := conn.LocalAddr(); la != nil {
		if tcpAddr, ok := la.(*net.TCPAddr); ok && tcpAddr != nil {
			return tcpAddr.IP.String(), strconv.Itoa(tcpAddr.Port)
		}
		if h, p, err := net.SplitHostPort(la.String()); err == nil {
			return strings.Trim(h, "[]"), p
		}
	}
	return "", ""
}

func (s *ProxyServer) handlePlainHTTP(conn net.Conn, req *http.Request) bool {
	host := req.Host
	reqPort := ""
	if h, p, err := net.SplitHostPort(req.Host); err == nil {
		host = h
		reqPort = p
	}
	hostTrimmed := strings.Trim(host, "[]")
	if isTelemetryHost(hostTrimmed) {
		writeHTTPResponse(conn, http.StatusForbidden, nil, nil, req.Method == http.MethodHead, true)
		return false
	}

	cleanPath := strings.ToLower(strings.Split(req.URL.Path, "?")[0])

	// Check if this request targets local internal proxy endpoints
	cfg := s.cfgMgr.Get()
	listenPortStr := strconv.Itoa(cfg.ListenPort)
	localIP, localPort := safeLocalAddr(conn)
	if localPort == "" {
		localPort = listenPortStr
	}

	effectivePort := reqPort
	if effectivePort == "" {
		effectivePort = "80"
	}

	isProxyPort := reqPort == listenPortStr || reqPort == localPort || effectivePort == listenPortStr || effectivePort == localPort

	isInternalCertOrPac := cleanPath == "/ca.crt" || cleanPath == "/ca.pem" ||
		cleanPath == "/proxy.pac" ||
		((strings.HasSuffix(cleanPath, "/ca.crt") || strings.HasSuffix(cleanPath, "/proxy.pac")) && (!req.URL.IsAbs() || isProxyPort))
	isLandingPage := cleanPath == "" || cleanPath == "/" || cleanPath == "/index.html"

	isLocalHost := hostTrimmed == "" || hostTrimmed == "127.0.0.1" || hostTrimmed == "localhost" ||
		hostTrimmed == "::1" || hostTrimmed == "proxy" || hostTrimmed == "gbf-proxy" ||
		(localIP != "" && hostTrimmed == localIP) ||
		(config.GetLANIP() != "" && hostTrimmed == config.GetLANIP())

	// A proxy client may address the listener using any of the machine's private
	// LAN addresses (important on multi-NIC hosts). The proxy port is the anchor:
	// public/external hosts are never treated as internal, while RFC1918 addresses
	// on this listener can use the local helper endpoints.
	isProxyEndpointHost := isLocalHost || (isProxyPort && isPrivateProxyHost(hostTrimmed))
	isDirectLocal := false
	if isProxyEndpointHost {
		if isInternalCertOrPac {
			isDirectLocal = true
		} else if isLandingPage {
			isDirectLocal = !req.URL.IsAbs() || isProxyPort
		}
	}

	// 1. Root CA Certificate download (only for direct requests to proxy host itself)
	if isDirectLocal && (cleanPath == "/ca.crt" || cleanPath == "/ca.pem" || strings.HasSuffix(cleanPath, "/ca.crt")) {
		caBytes := s.certMgr.GetCAPEM()
		hdr := make(http.Header)
		hdr.Set("Content-Type", "application/x-x509-ca-cert")
		hdr.Set("Content-Disposition", `attachment; filename="gbf_ca.crt"`)
		hdr.Set("Access-Control-Allow-Origin", "*")
		hdr.Set("Cache-Control", "no-cache")
		writeHTTPResponse(conn, http.StatusOK, hdr, caBytes, req.Method == http.MethodHead, req.Close)
		return !req.Close
	}

	// 2. Dynamic PAC script (only for direct requests to proxy host itself)
	if isDirectLocal && (cleanPath == "/proxy.pac" || strings.HasSuffix(cleanPath, "/proxy.pac")) {
		cfg := s.cfgMgr.Get()
		h := "127.0.0.1"
		hostNoPort := req.Host
		if hp, _, err := net.SplitHostPort(req.Host); err == nil {
			hostNoPort = hp
		}
		if hostNoPort != "" && hostNoPort != "127.0.0.1" && hostNoPort != "localhost" && hostNoPort != "::1" {
			h = hostNoPort
		}
		pacText := GetPAC(h, cfg.ListenPort)
		hdr := make(http.Header)
		hdr.Set("Content-Type", "application/x-ns-proxy-autoconfig")
		hdr.Set("Access-Control-Allow-Origin", "*")
		hdr.Set("Cache-Control", "no-store, no-cache, must-revalidate")
		hdr.Set("Pragma", "no-cache")
		hdr.Set("Expires", "0")
		writeHTTPResponse(conn, http.StatusOK, hdr, []byte(pacText), req.Method == http.MethodHead, req.Close)
		return !req.Close
	}

	// 3. Mobile LAN landing guide (only for direct requests to proxy host itself)
	if isDirectLocal && (cleanPath == "" || cleanPath == "/" || cleanPath == "/index.html") {
		cfg := s.cfgMgr.Get()
		lanIP := config.GetLANIP()
		if hostTrimmed != "127.0.0.1" && hostTrimmed != "localhost" && hostTrimmed != "::1" && hostTrimmed != "" && !isExternalGBFDomain(hostTrimmed) {
			lanIP = hostTrimmed
		} else if lanIP == "" {
			lanIP = "127.0.0.1"
		}
		html := GetLandingHTML(lanIP, cfg.ListenPort)
		hdr := make(http.Header)
		hdr.Set("Content-Type", "text/html; charset=utf-8")
		hdr.Set("Access-Control-Allow-Origin", "*")
		writeHTTPResponse(conn, http.StatusOK, hdr, []byte(html), req.Method == http.MethodHead, req.Close)
		return !req.Close
	}

	// 4. Plain HTTP proxy request (e.g. GET http://gbf.game.mbga.jp/)
	if isGBFDomain(hostTrimmed) && isStaticTarget(hostTrimmed, req.URL.Path) && (req.Method == http.MethodGet || req.Method == http.MethodHead) {
		return s.handleStaticAsset(conn, req, hostTrimmed)
	}
	return s.forwardPlainProxy(conn, req)
}

func (s *ProxyServer) forwardPlainProxy(conn net.Conn, req *http.Request) bool {
	s.stats.IncAPI()
	s.stats.AddActiveAPI(1)
	defer s.stats.AddActiveAPI(-1)

	// Loop prevention: do not forward requests targeted at the proxy itself
	hostOnly := req.Host
	reqPort := ""
	if h, p, err := net.SplitHostPort(req.Host); err == nil {
		hostOnly = h
		reqPort = p
	}
	hostTrimmed := strings.Trim(hostOnly, "[]")
	effectivePort := reqPort
	if effectivePort == "" {
		effectivePort = "80"
	}

	cfg := s.cfgMgr.Get()
	listenPortStr := strconv.Itoa(cfg.ListenPort)
	localIP, localPort := safeLocalAddr(conn)
	if localPort == "" {
		localPort = listenPortStr
	}

	isProxyPort := effectivePort == listenPortStr || effectivePort == localPort
	isSelfHost := hostTrimmed == "" || hostTrimmed == "127.0.0.1" || hostTrimmed == "localhost" ||
		hostTrimmed == "::1" || hostTrimmed == "proxy" || hostTrimmed == "gbf-proxy" ||
		(localIP != "" && hostTrimmed == localIP) ||
		(config.GetLANIP() != "" && hostTrimmed == config.GetLANIP())

	if isProxyPort && isSelfHost {
		writeHTTPResponse(conn, http.StatusNotFound, nil, nil, req.Method == http.MethodHead, true)
		return false
	}

	startTime := time.Now()
	upURL := req.URL.String()
	if !strings.HasPrefix(upURL, "http://") && !strings.HasPrefix(upURL, "https://") {
		host := req.Host
		if host == "" {
			host = "gbf.game.mbga.jp"
		}
		upURL = "http://" + host + req.URL.RequestURI()
	}

	var bodyBytes []byte
	if req.Body != nil {
		var bodyErr error
		bodyBytes, bodyErr = io.ReadAll(req.Body)
		if bodyErr != nil {
			writeHTTPResponse(conn, http.StatusBadRequest, nil, nil, req.Method == http.MethodHead, req.Close)
			return !req.Close
		}
	}
	cleanPath := strings.ToLower(strings.Split(req.URL.Path, "?")[0])

	// Strict P0 Safe Retry Rule:
	// Only read-only idempotent GET requests to whitelisted paths may be retried on connection drop.
	// All POST/PUT/DELETE requests have maxAttempts = 1 (strictly zero retry).
	isRetryable := (req.Method == http.MethodGet && isRetryableAPI(cleanPath))
	maxAttempts := 1
	if isRetryable {
		maxAttempts = 2
	}

	var resp *http.Response
	var fetchErr error
	var reqReused bool

	for attempt := 0; attempt < maxAttempts; attempt++ {
		reqCtx := req.Context()
		if reqCtx == nil {
			reqCtx = context.Background()
		}
		upReq, err := http.NewRequestWithContext(reqCtx, req.Method, upURL, bytes.NewReader(bodyBytes))
		if err != nil {
			fetchErr = err
			break
		}

		// P0: Business semantic transparency & strictly zero retry on write requests
		// Explicitly disable GetBody for non-retryable requests to prevent Go Transport from silently replaying
		if !isRetryable {
			upReq.GetBody = nil
		}

		for k, vv := range req.Header {
			if isHopByHop(k) {
				continue
			}
			for _, v := range vv {
				upReq.Header.Add(k, v)
			}
		}
		upReq.Host = req.Host

		resp, fetchErr = s.getAPIClient().Do(s.tracedRequestWithCallback(upReq, func(reused bool) {
			reqReused = reused
		}))
		if fetchErr == nil {
			break
		}
		if attempt+1 < maxAttempts && isConnectionDropError(fetchErr) {
			s.stats.IncAPIRetry()
			continue
		}
		break
	}

	if fetchErr != nil || resp == nil {
		writeHTTPResponse(conn, http.StatusBadGateway, nil, nil, req.Method == http.MethodHead, req.Close)
		return !req.Close
	}
	defer resp.Body.Close()

	respBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeHTTPResponse(conn, http.StatusBadGateway, nil, nil, req.Method == http.MethodHead, req.Close)
		return !req.Close
	}
	s.forwardDynamicResponse(conn, resp, respBytes, req.Method == http.MethodHead, req.Close)
	elapsed := time.Since(startTime).Milliseconds()
	if s.telemetryEnabled() {
		s.stats.RecordProtocol(resp.Proto)
		s.stats.RecordLatency(float64(elapsed))
	}
	reusedStr := "new"
	if reqReused {
		reusedStr = "reused"
	}
	protoStr := resp.Proto
	if protoStr == "" {
		protoStr = "HTTP/1.1"
	}
	uri := req.URL.RequestURI()
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	s.stats.Log("INFO", fmt.Sprintf("[BYPASS-PLAIN-API] %d %s %s (%dms, %s, %s)", resp.StatusCode, req.Method, uri, elapsed, protoStr, reusedStr))
	return !req.Close
}

func (s *ProxyServer) handleDecryptedRequest(w io.Writer, req *http.Request, targetHost string) bool {
	// Rule 1: CORS Preflight
	if req.Method == http.MethodOptions {
		hdr := make(http.Header)
		hdr.Set("Access-Control-Allow-Origin", "*")
		hdr.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		hdr.Set("Access-Control-Allow-Headers", "*")
		hdr.Set("Access-Control-Max-Age", "604800")
		writeHTTPResponse(w, http.StatusOK, hdr, nil, req.Method == http.MethodHead, req.Close)
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

func isBlockedWindowsPath(s string) bool {
	s = strings.ToLower(s)
	if strings.HasPrefix(s, "windows/") || strings.HasPrefix(s, "windows\\") ||
		s == "windows" ||
		strings.HasPrefix(s, "/windows/") || strings.HasPrefix(s, `\windows\`) ||
		strings.HasPrefix(s, "/windows\\") || strings.HasPrefix(s, `\windows/`) ||
		s == "/windows" || s == `\windows` ||
		strings.Contains(s, "/windows/") || strings.Contains(s, `\windows\`) ||
		strings.Contains(s, "/windows\\") || strings.Contains(s, `\windows/`) ||
		strings.HasSuffix(s, "/windows") || strings.HasSuffix(s, `\windows`) {
		return true
	}
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		rest := s[2:]
		if rest == `\windows` || rest == `/windows` ||
			strings.HasPrefix(rest, `\windows\`) || strings.HasPrefix(rest, `/windows/`) ||
			strings.HasPrefix(rest, `\windows/`) || strings.HasPrefix(rest, `/windows\`) {
			return true
		}
	}
	return false
}

func (s *ProxyServer) getCacheControlHeader(urlPath string) string {
	cfg := s.cfgMgr.Get()
	if !cfg.EnableBrowserCache {
		return "no-cache"
	} else if reVersioned.MatchString(urlPath) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

func isClientNotModified(req *http.Request, item *cache.CacheItem) bool {
	if item == nil {
		return false
	}
	reqETag := req.Header.Get("If-None-Match")
	if reqETag != "" {
		return matchETag(reqETag, item.ETag)
	}
	reqIMS := req.Header.Get("If-Modified-Since")
	if reqIMS != "" && item.LastModified != "" {
		if reqIMS == item.LastModified {
			return true
		}
		tIMS, err1 := http.ParseTime(reqIMS)
		tLM, err2 := http.ParseTime(item.LastModified)
		if err1 == nil && err2 == nil {
			return !tLM.After(tIMS)
		}
	}
	return false
}

func (s *ProxyServer) sendNotModifiedResponse(w io.Writer, item *cache.CacheItem, urlPath string, reqClose bool) {
	hdr := make(http.Header)
	if item.ETag != "" {
		hdr.Set("ETag", item.ETag)
	}
	if item.LastModified != "" {
		hdr.Set("Last-Modified", item.LastModified)
	}
	hdr.Set("Cache-Control", s.getCacheControlHeader(urlPath))
	hdr.Set("Access-Control-Allow-Origin", "*")

	writeHTTPResponse(w, http.StatusNotModified, hdr, nil, false, reqClose)
}

func (s *ProxyServer) handleStaticAsset(w io.Writer, req *http.Request, targetHost string) bool {
	cleanPath := strings.Split(req.URL.Path, "?")[0]

	// Security: Reject path traversal sequences immediately
	// Extract the path portion from raw req.RequestURI before unescaping
	// (so that query parameters like ?t=12:34:56 do not falsely trigger colon checks,
	// and encoded %3F in path is not confused with query delimiter).
	rawURIPath := strings.Split(req.RequestURI, "?")[0]
	rawURIPath = strings.Split(rawURIPath, "#")[0]

	if idx := strings.Index(rawURIPath, "://"); idx != -1 {
		afterScheme := rawURIPath[idx+3:]
		if slashIdx := strings.Index(afterScheme, "/"); slashIdx != -1 {
			rawURIPath = afterScheme[slashIdx:]
		} else {
			rawURIPath = "/"
		}
	} else if strings.HasPrefix(rawURIPath, "//") {
		afterSlashes := strings.TrimPrefix(rawURIPath, "//")
		if slashIdx := strings.Index(afterSlashes, "/"); slashIdx != -1 {
			rawURIPath = afterSlashes[slashIdx:]
		} else {
			rawURIPath = "/"
		}
	}

	unescapedPath, _ := url.PathUnescape(rawURIPath)
	unescapedURI, _ := url.PathUnescape(req.RequestURI)

	thHost := targetHost
	thHostHasColon := false
	if hp, port, err := net.SplitHostPort(targetHost); err == nil {
		thHost = hp
		if _, pErr := strconv.Atoi(port); pErr != nil {
			thHostHasColon = true
		}
	}
	thHost = strings.Trim(thHost, "[]")
	if strings.Contains(thHost, ":") && net.ParseIP(thHost) == nil {
		thHostHasColon = true
	}

	// Normalize targetHost: strip standard ports so upstream https://targetHost connects to 443
	if hp, port, err := net.SplitHostPort(targetHost); err == nil {
		if port == "80" || port == "443" {
			targetHost = hp
		}
	}

	unescapedLower := strings.ToLower(unescapedURI)
	unescapedPathLower := strings.ToLower(unescapedPath)
	reqURILower := strings.ToLower(req.RequestURI)
	pathLower := strings.ToLower(req.URL.Path)
	cleanLower := strings.ToLower(cleanPath)
	if strings.Contains(req.RequestURI, "..") ||
		strings.Contains(req.URL.Path, "..") ||
		strings.Contains(unescapedURI, "..") ||
		isBlockedWindowsPath(reqURILower) ||
		isBlockedWindowsPath(pathLower) ||
		isBlockedWindowsPath(unescapedLower) ||
		isBlockedWindowsPath(unescapedPathLower) ||
		strings.Contains(reqURILower, "passwd") ||
		strings.Contains(pathLower, "passwd") ||
		strings.Contains(unescapedLower, "passwd") ||
		strings.Contains(cleanPath, ":") ||
		strings.Contains(unescapedPath, ":") ||
		thHostHasColon ||
		strings.HasPrefix(cleanLower, "\\") ||
		strings.HasPrefix(unescapedLower, "\\") ||
		strings.HasPrefix(unescapedPathLower, "\\") {
		writeHTTPResponse(w, http.StatusBadRequest, nil, nil, req.Method == http.MethodHead, req.Close)
		return !req.Close
	}

	s.stats.IncAsset()
	s.stats.AddActiveFG(1)
	defer s.stats.AddActiveFG(-1)

	isHead := (req.Method == http.MethodHead)
	ns, _ := NormalizeAssetNamespace(targetHost)

	// 1. Cache Lookup (RAM first, then SSD)
	item, hitSrc := s.cacheMgr.GetWithNamespace(ns, req.URL.Path)
	if item != nil {
		s.stats.CheckAndRecordPrefetchReused(cleanPath)
		if hitSrc == "RAM" {
			s.stats.IncRAMHit()
			// P2: Hot path optimization: lightweight atomic counters only for RAM hits
		} else {
			s.stats.IncDiskHit()
			s.stats.Log("INFO", fmt.Sprintf("[CACHE-DISK] HIT -> %s (%d B)", cleanPath, len(item.Data)))
		}

		// Conditional GET: 304 Not Modified check
		if isClientNotModified(req, item) {
			s.stats.Log("INFO", fmt.Sprintf("[%s] 304 Not Modified -> %s", "CACHE-"+hitSrc, cleanPath))
			s.sendNotModifiedResponse(w, item, req.URL.Path, req.Close)
			return !req.Close
		}

		s.sendAssetResponse(w, 200, item, isHead, req.URL.Path, req.Close)
		return !req.Close
	}

	// 2. Cache Miss: Coalesce concurrent fetches using SingleFlight with normalized key
	s.stats.IncMiss()
	flightKey := ns + ":" + cleanPath

	reqCtx := req.Context()
	if reqCtx == nil {
		reqCtx = context.Background()
	}

	res, err := s.cacheMgr.SingleFlight().DoContext(reqCtx, flightKey, func() (interface{}, error) {
		upURL := fmt.Sprintf("https://%s%s", targetHost, req.URL.RequestURI())
		upReq, err := http.NewRequestWithContext(context.Background(), "GET", upURL, nil)
		if err != nil {
			return nil, err
		}

		for k, vv := range req.Header {
			if isHopByHop(k) || strings.EqualFold(k, "if-none-match") || strings.EqualFold(k, "if-modified-since") {
				continue
			}
			for _, v := range vv {
				upReq.Header.Add(k, v)
			}
		}
		upReq.Host = targetHost

		fetchStart := time.Now()
		resp, err := s.getAssetClient().Do(s.tracedRequest(upReq))
		if err == nil && resp != nil && s.telemetryEnabled() {
			s.stats.RecordProtocol(resp.Proto)
			s.stats.RecordLatency(float64(time.Since(fetchStart).Milliseconds()))
		}
		if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			// Check offline cache fallback
			if fb, _ := s.cacheMgr.GetFallback(req.URL.Path); fb != nil {
				return fb, nil
			}
			if err != nil {
				return nil, err
			}
			if resp == nil {
				return nil, errors.New("upstream returned nil response")
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

		// Respond-First: Instant RAM cache write & non-blocking background disk persistence
		savedItem, ok := s.cacheMgr.SaveRAMWithNamespace(ns, req.URL.Path, headersMap, data)
		if !ok || savedItem == nil {
			return nil, fmt.Errorf("failed to store asset in cache")
		}

		if s.prefetch != nil {
			s.prefetch.MaybeEnqueueDiscovery(targetHost, req.URL.Path, data)
		}
		s.stats.Log("INFO", fmt.Sprintf("[FETCH-ASSET] 200 OK -> %s%s (%d B)", targetHost, cleanPath, len(data)))

		return savedItem, nil
	})

	if err != nil || res == nil {
		if reqCtx.Err() != nil {
			return false
		}
		writeHTTPResponse(w, http.StatusBadGateway, nil, nil, isHead, req.Close)
		return !req.Close
	}

	cachedItem := res.(*cache.CacheItem)
	if isClientNotModified(req, cachedItem) {
		s.sendNotModifiedResponse(w, cachedItem, req.URL.Path, req.Close)
		return !req.Close
	}

	s.sendAssetResponse(w, 200, cachedItem, isHead, req.URL.Path, req.Close)
	return !req.Close
}

func matchETag(clientETag, serverETag string) bool {
	if clientETag == "" || serverETag == "" {
		return false
	}
	clientETag = strings.TrimSpace(clientETag)
	if clientETag == "*" {
		return true
	}
	sNorm := strings.Trim(strings.TrimPrefix(serverETag, "W/"), "\"")
	for _, part := range strings.Split(clientETag, ",") {
		part = strings.TrimSpace(part)
		if part == "*" {
			return true
		}
		cNorm := strings.Trim(strings.TrimPrefix(part, "W/"), "\"")
		if cNorm == sNorm || part == serverETag {
			return true
		}
	}
	return false
}

func (s *ProxyServer) handleDynamicAPI(w io.Writer, req *http.Request, targetHost string) bool {
	s.stats.IncAPI()
	s.stats.AddActiveAPI(1)
	defer s.stats.AddActiveAPI(-1)

	startTime := time.Now()
	var bodyBytes []byte
	if req.Body != nil {
		var bodyErr error
		bodyBytes, bodyErr = io.ReadAll(req.Body)
		if bodyErr != nil {
			writeHTTPResponse(w, http.StatusBadRequest, nil, nil, req.Method == http.MethodHead, req.Close)
			return !req.Close
		}
	}
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
	var reqReused bool

	for attempt := 0; attempt < maxAttempts; attempt++ {
		reqCtx := req.Context()
		if reqCtx == nil {
			reqCtx = context.Background()
		}
		upReq, err := http.NewRequestWithContext(reqCtx, req.Method, upURL, bytes.NewReader(bodyBytes))
		if err != nil {
			fetchErr = err
			break
		}

		// P0: Business semantic transparency & strictly zero retry on write requests
		// Explicitly disable GetBody for non-retryable requests to prevent Go Transport from silently replaying the request
		if !isRetryable {
			upReq.GetBody = nil
		}

		for k, vv := range req.Header {
			if isHopByHop(k) {
				continue
			}
			for _, v := range vv {
				upReq.Header.Add(k, v)
			}
		}
		upReq.Host = targetHost

		resp, fetchErr = s.getAPIClient().Do(s.tracedRequestWithCallback(upReq, func(reused bool) {
			reqReused = reused
		}))
		if fetchErr == nil {
			break
		}
		if attempt+1 < maxAttempts && isConnectionDropError(fetchErr) {
			s.stats.IncAPIRetry()
			continue
		}
		break
	}

	if fetchErr != nil || resp == nil {
		writeHTTPResponse(w, http.StatusBadGateway, nil, nil, req.Method == http.MethodHead, req.Close)
		return !req.Close
	}
	defer resp.Body.Close()

	respBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		writeHTTPResponse(w, http.StatusBadGateway, nil, nil, req.Method == http.MethodHead, req.Close)
		return !req.Close
	}
	s.forwardDynamicResponse(w, resp, respBytes, req.Method == http.MethodHead, req.Close)
	elapsed := time.Since(startTime).Milliseconds()
	if s.telemetryEnabled() {
		s.stats.RecordProtocol(resp.Proto)
		s.stats.RecordLatency(float64(elapsed))
	}
	reusedStr := "new"
	if reqReused {
		reusedStr = "reused"
	}
	protoStr := resp.Proto
	if protoStr == "" {
		protoStr = "HTTP/1.1"
	}
	uri := req.URL.RequestURI()
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	s.stats.Log("INFO", fmt.Sprintf("[BYPASS-API] %d %s %s%s (%dms, %s, %s)", resp.StatusCode, req.Method, targetHost, uri, elapsed, protoStr, reusedStr))
	return !req.Close
}

var responseBufferPool = sync.Pool{
	New: func() interface{} {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	},
}

// writeHTTPResponse serializes an RFC-compliant HTTP/1.1 response to w.
// It ensures standard Date header presence, P0-compliant header filtering
// (zero proxy-fingerprint pollution, hop-by-hop stripping, Set-Cookie line-by-line
// preservation), RFC 7230 content-length rules (omitted for 1xx, 204, 304), and HEAD body suppression.
func writeHTTPResponse(w io.Writer, statusCode int, header http.Header, body []byte, isHead bool, reqClose bool) {
	buf := responseBufferPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer func() {
		if buf.Cap() <= 128*1024 {
			responseBufferPool.Put(buf)
		}
	}()

	statusPhrase := http.StatusText(statusCode)
	if statusPhrase == "" {
		statusPhrase = "OK"
	}

	var numBuf [32]byte

	buf.WriteString("HTTP/1.1 ")
	buf.Write(strconv.AppendInt(numBuf[:0], int64(statusCode), 10))
	buf.WriteByte(' ')
	buf.WriteString(statusPhrase)
	buf.WriteString("\r\n")

	hasDate := false
	if header != nil && header.Get("Date") != "" {
		hasDate = true
	}
	if !hasDate {
		buf.WriteString("Date: ")
		var dateBuf [32]byte
		buf.Write(time.Now().UTC().AppendFormat(dateBuf[:0], http.TimeFormat))
		buf.WriteString("\r\n")
	}

	if header != nil {
		var cookieArr [8]string
		cookies := cookieArr[:0]
		for k, vv := range header {
			if strings.EqualFold(k, "set-cookie") {
				cookies = append(cookies, vv...)
				continue
			}
			if isHopByHop(k) || strings.EqualFold(k, "content-length") || strings.EqualFold(k, "connection") {
				continue
			}
			if !hasDate && strings.EqualFold(k, "date") {
				continue
			}
			// P0: Zero header pollution
			if hasCaseInsensitivePrefix(k, "x-proxy-") ||
				hasCaseInsensitivePrefix(k, "x-cache-") ||
				hasCaseInsensitivePrefix(k, "x-acceleration-") {
				continue
			}
			formattedKey := formatHeaderKey(k)
			for _, v := range vv {
				buf.WriteString(formattedKey)
				buf.WriteString(": ")
				buf.WriteString(v)
				buf.WriteString("\r\n")
			}
		}

		// P0: Multi-line Set-Cookie preservation (never fold into single line with commas)
		for _, cookie := range cookies {
			buf.WriteString("Set-Cookie: ")
			buf.WriteString(cookie)
			buf.WriteString("\r\n")
		}
	}

	if statusCode != http.StatusNoContent && statusCode != http.StatusNotModified && (statusCode < 100 || statusCode >= 200) {
		buf.WriteString("Content-Length: ")
		buf.Write(strconv.AppendInt(numBuf[:0], int64(len(body)), 10))
		buf.WriteString("\r\n")
	}
	if reqClose {
		buf.WriteString("Connection: close\r\n\r\n")
	} else {
		buf.WriteString("Connection: keep-alive\r\n\r\n")
	}

	if isHead || statusCode == http.StatusNoContent || statusCode == http.StatusNotModified || (statusCode >= 100 && statusCode < 200) {
		_, _ = w.Write(buf.Bytes())
	} else if len(body) == 0 {
		_, _ = w.Write(buf.Bytes())
	} else if buf.Len()+len(body) <= 64*1024 {
		buf.Write(body)
		_, _ = w.Write(buf.Bytes())
	} else {
		bufs := net.Buffers{buf.Bytes(), body}
		_, _ = bufs.WriteTo(w)
	}
}

func (s *ProxyServer) sendAssetResponse(w io.Writer, status int, item *cache.CacheItem, isHead bool, urlPath string, reqClose bool) {
	hdr := make(http.Header)
	if item.ContentType != "" {
		hdr.Set("Content-Type", item.ContentType)
	}
	if item.ETag != "" {
		hdr.Set("ETag", item.ETag)
	}
	if item.LastModified != "" {
		hdr.Set("Last-Modified", item.LastModified)
	}
	if item.ContentEncoding != "" {
		hdr.Set("Content-Encoding", item.ContentEncoding)
	}
	hdr.Set("Access-Control-Allow-Origin", "*")

	// P1: Safe browser cache policy - immutable ONLY for versioned assets
	hdr.Set("Cache-Control", s.getCacheControlHeader(urlPath))

	writeHTTPResponse(w, status, hdr, item.Data, isHead, reqClose)
}

func (s *ProxyServer) forwardDynamicResponse(w io.Writer, resp *http.Response, body []byte, isHead bool, reqClose bool) {
	writeHTTPResponse(w, resp.StatusCode, resp.Header, body, isHead, reqClose)
}

func isHopByHop(h string) bool {
	return strings.EqualFold(h, "connection") ||
		strings.EqualFold(h, "keep-alive") ||
		strings.EqualFold(h, "proxy-authenticate") ||
		strings.EqualFold(h, "proxy-authorization") ||
		strings.EqualFold(h, "te") ||
		strings.EqualFold(h, "trailers") ||
		strings.EqualFold(h, "transfer-encoding") ||
		strings.EqualFold(h, "upgrade")
}

func hasCaseInsensitivePrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}

func formatHeaderKey(k string) string {
	switch {
	case strings.EqualFold(k, "etag"):
		return "ETag"
	case strings.EqualFold(k, "www-authenticate"):
		return "WWW-Authenticate"
	default:
		return k
	}
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

func isConnectionDropError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "eof") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "closed network connection") ||
		strings.Contains(msg, "connection refused")
}

func NormalizeAssetNamespace(host string) (string, bool) {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	h := strings.ToLower(strings.TrimRight(host, "."))
	if isGBFAkamaiHost(h) || strings.HasPrefix(h, "game-a") {
		return "gbf", true
	}
	return h, false
}

func isPassthroughHost(host string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	h := strings.ToLower(host)
	return h == "ws.game.granbluefantasy.jp" || strings.HasPrefix(h, "ws.game.")
}

func isDomainOrSubdomain(host, domain string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	h := strings.ToLower(strings.TrimRight(host, "."))
	d := strings.ToLower(strings.TrimRight(domain, "."))
	return h == d || strings.HasSuffix(h, "."+d)
}

func isGBFAkamaiHost(host string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
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
	// Strict prefix matching for future Akamai CDN shards
	if strings.HasPrefix(h, "prd-game-a") && strings.HasSuffix(h, "-granbluefantasy.akamaized.net") {
		return true
	}
	return false
}

func isGBFDomain(host string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	if isPassthroughHost(host) {
		return false
	}
	if isGBFAkamaiHost(host) {
		return true
	}
	return isDomainOrSubdomain(host, "granbluefantasy.jp") ||
		isDomainOrSubdomain(host, "granbluefantasy.com") ||
		isDomainOrSubdomain(host, "gbf.game.mbga.jp") ||
		isDomainOrSubdomain(host, "connect.mobage.jp") ||
		isDomainOrSubdomain(host, "sp.mbga.jp") ||
		host == "localhost" || host == "127.0.0.1"
}

func isExternalGBFDomain(host string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}
	return isGBFDomain(host)
}

func isPrivateOrLocalHost(host string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	return host == "proxy" || host == "proxy.pac" || host == "ca.crt" || host == "gbf-proxy"
}

func isStaticTarget(host, path string) bool {
	if hp, _, err := net.SplitHostPort(host); err == nil {
		host = hp
	}
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
