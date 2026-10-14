package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/cert"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

func TestTelemetryToggleSkipsTraceWhenDisabled(t *testing.T) {
	cfgMgr := config.NewManager("")
	cfgMgr.Update(func(c *config.Config) {
		c.EnableAPITelemetry = false
	})
	mgr := cache.NewManager(t.TempDir(), 16)
	defer mgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, &cert.Manager{}, mgr, stats)
	req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.tracedRequest(req); got != req {
		t.Fatal("disabled telemetry should leave request untouched")
	}

	cfgMgr.Update(func(c *config.Config) { c.EnableAPITelemetry = true })
	got := srv.tracedRequest(req)
	if got == req {
		t.Fatal("enabled telemetry should attach a trace request clone")
	}
}

// TestTelemetryDomainsPassthroughNotBlocked verifies that former telemetry domains
// (e.g. smbeat.jp, datadoghq.com, smrtbeat.com, etc.) and official telemetry endpoints
// are no longer blocked with 403 Forbidden, and instead pass through standard proxy channels
// (plain HTTP forwarding & CONNECT tunnel).
func TestTelemetryDomainsPassthroughNotBlocked(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	certMgr, err := cert.NewManager(filepath.Join(tempDir, "certs"))
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, certMgr, cacheMgr, stats)

	testDomains := []string{
		"smbeat.jp",
		"smrtbeat.com",
		"rcv.a-i-ad.com",
		"datadoghq-browser-agent.com",
		"datadoghq.com",
		"spdmg-backend.i-mobile.co.jp",
		"creativecdn.com",
	}

	// 1. Plain HTTP: must NOT return 403 Forbidden, must forward via apiClient
	for _, domain := range testDomains {
		t.Run("PlainHTTP/"+domain, func(t *testing.T) {
			var forwardedHost string
			srv.apiClient.Store(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					forwardedHost = r.Host
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("upstream-ok")),
						Proto:      "HTTP/1.1",
					}, nil
				}),
			})

			conn := &dummyConn{}
			req := &http.Request{
				Method: http.MethodGet,
				Host:   domain,
				URL:    &url.URL{Scheme: "http", Host: domain, Path: "/collect"},
			}
			keepAlive := srv.handlePlainHTTP(conn, req)
			out := conn.writeBuf.String()

			if strings.Contains(out, "403 Forbidden") {
				t.Errorf("domain %s must NOT be blocked with 403 Forbidden in handlePlainHTTP", domain)
			}
			if !strings.Contains(out, "200 OK") || !strings.Contains(out, "upstream-ok") {
				t.Errorf("expected 200 OK forwarded response for %s, got: %s", domain, out)
			}
			if forwardedHost != domain {
				t.Errorf("expected apiClient to receive request for %s, got %s", domain, forwardedHost)
			}
			if !keepAlive {
				t.Errorf("expected keepAlive=true for plain proxy request to %s", domain)
			}
		})
	}

	// Also verify official telemetry paths on GBF domains are forwarded rather than blocked
	officialTargets := []struct {
		host string
		path string
	}{
		{"sp.mbga.jp", "/telemetry"},
		{"log.granbluefantasy.jp", "/log/collect"},
	}
	for _, ot := range officialTargets {
		t.Run("PlainHTTP/Official/"+ot.host, func(t *testing.T) {
			var forwardedHost string
			srv.apiClient.Store(&http.Client{
				Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					forwardedHost = r.Host
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("upstream-ok")),
						Proto:      "HTTP/1.1",
					}, nil
				}),
			})

			conn := &dummyConn{}
			req := &http.Request{
				Method: http.MethodGet,
				Host:   ot.host,
				URL:    &url.URL{Scheme: "http", Host: ot.host, Path: ot.path},
			}
			keepAlive := srv.handlePlainHTTP(conn, req)
			out := conn.writeBuf.String()

			if strings.Contains(out, "403 Forbidden") {
				t.Errorf("target %s%s must NOT be blocked with 403 Forbidden in handlePlainHTTP", ot.host, ot.path)
			}
			if !strings.Contains(out, "200 OK") || !strings.Contains(out, "upstream-ok") {
				t.Errorf("expected 200 OK forwarded response for %s%s, got: %s", ot.host, ot.path, out)
			}
			if forwardedHost != ot.host {
				t.Errorf("expected apiClient to receive request for %s, got %s", ot.host, forwardedHost)
			}
			if !keepAlive {
				t.Errorf("expected keepAlive=true for request to %s", ot.host)
			}
		})
	}

	// 2. CONNECT tunnel: must NOT return 403 Forbidden, must route to passthrough tunnel
	for _, domain := range testDomains {
		t.Run("CONNECT/"+domain, func(t *testing.T) {
			target := domain + ":443"

			upstreamLn, lErr := net.Listen("tcp", "127.0.0.1:0")
			if lErr != nil {
				t.Fatalf("failed to start mock upstream: %v", lErr)
			}
			defer upstreamLn.Close()

			connectReceived := make(chan string, 1)
			go func(ln net.Listener) {
				c, aErr := ln.Accept()
				if aErr != nil {
					return
				}
				defer c.Close()
				br := bufio.NewReader(c)
				upReq, rErr := http.ReadRequest(br)
				if rErr != nil {
					return
				}
				if upReq.Method == http.MethodConnect {
					connectReceived <- upReq.Host
					_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
					_, _ = io.Copy(io.Discard, c)
				}
			}(upstreamLn)

			cfgMgr.Update(func(c *config.Config) {
				c.UpstreamProxy = "http://" + upstreamLn.Addr().String()
			})

			clientSide, serverSide := net.Pipe()
			br := bufio.NewReader(serverSide)

			connectDone := make(chan struct{})
			go func() {
				defer close(connectDone)
				defer serverSide.Close()
				req, rErr := http.ReadRequest(br)
				if rErr != nil {
					return
				}
				srv.handleConnect(serverSide, br, req)
			}()

			// Write CONNECT request from client side
			connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
			_, _ = clientSide.Write([]byte(connectReq))

			clientBr := bufio.NewReader(clientSide)
			respLine, rErr := clientBr.ReadString('\n')

			if strings.Contains(respLine, "403") {
				t.Errorf("domain %s must NOT be blocked with 403 Forbidden in handleConnect", domain)
			}
			if !strings.Contains(respLine, "200") {
				t.Errorf("expected 200 Connection Established for %s, got: %s (err: %v)", domain, respLine, rErr)
			}

			select {
			case gotTarget := <-connectReceived:
				if gotTarget != target {
					t.Errorf("expected mock upstream to receive CONNECT for %s, got %s", target, gotTarget)
				}
			case <-time.After(2 * time.Second):
				t.Errorf("timed out waiting for upstream to receive CONNECT for %s", target)
			}

			_ = clientSide.Close()
			<-connectDone
		})
	}
}

type lanClientDummyConn struct {
	dummyConn
	remote net.Addr
}

func (c *lanClientDummyConn) RemoteAddr() net.Addr {
	return c.remote
}

func TestControlPlaneAndLoopbackBlockedViaDataPlane(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = 8124
		c.ControlPort = 8125
		c.AllowLAN = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, &cert.Manager{}, cacheMgr, stats)
	srv.apiClient.Store(&http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			t.Errorf("apiClient should never be invoked for blocked control-plane or LAN loopback target %s", r.URL.String())
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("unexpected")),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	})

	// 1. Plain HTTP to control port (8125) from local or LAN must be blocked with 403
	for _, host := range []string{"127.0.0.1:8125", "localhost:8125", "[::1]:8125"} {
		conn := &dummyConn{}
		req := &http.Request{
			Method: http.MethodPost,
			Host:   host,
			URL:    &url.URL{Scheme: "http", Host: host, Path: "/api/config/apply"},
		}
		srv.handlePlainHTTP(conn, req)
		if !strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("expected 403 Forbidden for plain HTTP to control plane %s, got: %s", host, conn.writeBuf.String())
		}
	}

	// 2. Plain HTTP to any loopback port from a non-loopback LAN client must be blocked with 403
	lanConn := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54321},
	}
	reqLAN := &http.Request{
		Method: http.MethodGet,
		Host:   "127.0.0.1:9000",
		URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:9000", Path: "/internal"},
	}
	srv.handlePlainHTTP(lanConn, reqLAN)
	if !strings.Contains(lanConn.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for LAN client targeting loopback, got: %s", lanConn.writeBuf.String())
	}

	// 3. CONNECT tunnel to control port (8125) or proxy port (8124) must be blocked with 403
	for _, target := range []string{"127.0.0.1:8125", "localhost:8125", "127.0.0.1:8124"} {
		conn := &dummyConn{}
		srv.handlePassthroughTunnel(conn, strings.NewReader(""), target)
		if !strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("expected 403 Forbidden for CONNECT tunnel to %s, got: %s", target, conn.writeBuf.String())
		}
	}

	// 4. CONNECT tunnel from LAN client to any loopback port must be blocked with 403
	lanTunnelConn := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54322},
	}
	srv.handlePassthroughTunnel(lanTunnelConn, strings.NewReader(""), "127.0.0.1:9000")
	if !strings.Contains(lanTunnelConn.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for LAN CONNECT tunnel to loopback, got: %s", lanTunnelConn.writeBuf.String())
	}
}

func TestFailoverWatchdogLateTrialErrorDoesNotClobberNewTrial(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, &cert.Manager{}, cacheMgr, stats)
	srv.failover.configure(true, true, 50*time.Millisecond, 1, time.Second, true)

	now := time.Now()
	if tr := srv.failover.observe(failoverRoutePrimary, false, 100*time.Millisecond, errors.New("down"), now.Add(-5*time.Second)); tr == nil {
		t.Fatal("expected initial failover to backup")
	}

	// Start Trial #1 and simulate watchdog firing on Trial #1
	route1, trial1 := srv.failover.routeForRequest(true, now)
	if route1 != failoverRoutePrimary || !trial1 {
		t.Fatalf("expected trial #1 on primary, got %s/%v", route1, trial1)
	}
	tracker1 := &inFlightTracker{threshold: 50 * time.Millisecond, stopped: true}
	tracker1.fired.Store(true)
	srv.observeInFlightTimeout(route1, trial1)

	// Advance cooldown and start Trial #2 while Trial #1 is still unwinding
	srv.failover.mu.Lock()
	srv.failover.lastSwitchAt = time.Now().Add(-2 * time.Second)
	srv.failover.mu.Unlock()
	_, trial2 := srv.failover.routeForRequest(true, time.Now())
	if !trial2 {
		t.Fatal("expected trial #2 to start after cooldown")
	}

	// Trial #1 finally returns a late hard error; it must NOT clear trialInFlight for Trial #2
	srv.finishInFlightWatch(tracker1, route1, trial1, 200*time.Millisecond, errors.New("late trial #1 error"))

	srv.failover.mu.Lock()
	stillInFlight := srv.failover.trialInFlight
	srv.failover.mu.Unlock()
	if !stillInFlight {
		t.Fatal("late error from timed-out trial #1 clobbered active trialInFlight of trial #2")
	}
}

func TestControlPlaneHostConfusionAndExternalPort8125Allowed(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = 8124
		c.ControlPort = 8125
		c.AllowLAN = true
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, &cert.Manager{}, cacheMgr, stats)

	var lastTargetURL string
	var lastTargetHostHeader string
	mockClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			lastTargetURL = r.URL.String()
			lastTargetHostHeader = r.Host
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("upstream-ok")),
				Proto:      "HTTP/1.1",
			}, nil
		}),
	}
	srv.primaryClient.Store(&upstreamClients{api: mockClient, asset: mockClient})
	srv.apiClient.Store(mockClient)
	srv.assetClient.Store(mockClient)

	// 1. Host Confusion targeting control plane:
	// req.URL.Host = 127.0.0.1:8125, req.Host = innocent.com MUST be blocked with 403 Forbidden
	for _, target := range []string{"127.0.0.1:8125", "localhost:8125", "[::1]:8125", "0.0.0.0:8125"} {
		conn := &dummyConn{}
		req := &http.Request{
			Method: http.MethodPost,
			Host:   "innocent.com",
			URL:    &url.URL{Scheme: "http", Host: target, Path: "/api/config/apply"},
		}
		srv.handlePlainHTTP(conn, req)
		if !strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("expected 403 Forbidden for Host confusion targeting %s with Host innocent.com, got: %s", target, conn.writeBuf.String())
		}
	}

	// 2. Dynamically configured local control port:
	// If control port is changed to 9125, 127.0.0.1:9125 and [::1]:9125 must be blocked with 403
	cfgMgr.Update(func(c *config.Config) {
		c.ControlPort = 9125
	})
	for _, target := range []string{"127.0.0.1:9125", "[::1]:9125"} {
		conn := &dummyConn{}
		req := &http.Request{
			Method: http.MethodPost,
			Host:   "innocent.com",
			URL:    &url.URL{Scheme: "http", Host: target, Path: "/api/config/apply"},
		}
		srv.handlePlainHTTP(conn, req)
		if !strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("expected 403 Forbidden for dynamically configured control port %s, got: %s", target, conn.writeBuf.String())
		}
	}
	// Restore control port
	cfgMgr.Update(func(c *config.Config) {
		c.ControlPort = 8125
	})

	// 3. Legitimate external target with port 8125 MUST NOT be falsely blocked:
	srv.primaryClient.Store(&upstreamClients{api: mockClient, asset: mockClient})
	srv.apiClient.Store(mockClient)
	srv.assetClient.Store(mockClient)

	externalTargets := []struct {
		urlHost string
		reqHost string
	}{
		{urlHost: "api.legitimate-external.com:8125", reqHost: "api.legitimate-external.com:8125"},
		{urlHost: "api.legitimate-external.com:8125", reqHost: "127.0.0.1:8125"},
		{urlHost: "api.legitimate-external.com:8125", reqHost: "innocent.com"},
	}
	for _, tc := range externalTargets {
		conn := &dummyConn{}
		req := &http.Request{
			Method: http.MethodGet,
			Host:   tc.reqHost,
			URL:    &url.URL{Scheme: "http", Host: tc.urlHost, Path: "/v1/data"},
		}
		srv.handlePlainHTTP(conn, req)
		if strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("external port 8125 request falsely blocked! urlHost=%s reqHost=%s, response: %s", tc.urlHost, tc.reqHost, conn.writeBuf.String())
		}
		if !strings.Contains(conn.writeBuf.String(), "200 OK") {
			t.Fatalf("expected 200 OK for external port 8125, got: %s", conn.writeBuf.String())
		}
		if !strings.Contains(lastTargetURL, "api.legitimate-external.com:8125") {
			t.Fatalf("expected upstream target to be api.legitimate-external.com:8125, got: %s", lastTargetURL)
		}
		if lastTargetHostHeader != tc.urlHost {
			t.Fatalf("expected forwarded Host header normalized to %s per RFC 9112 §3.2.2, got %s", tc.urlHost, lastTargetHostHeader)
		}
	}

	// 4. Mismatched forwarding actual target and Host boundary scenarios:
	// 4a. Relative URI with no URL.Host, but Host: 127.0.0.1:8125 -> MUST be blocked with 403
	connRel := &dummyConn{}
	reqRel := &http.Request{
		Method: http.MethodPost,
		Host:   "127.0.0.1:8125",
		URL:    &url.URL{Path: "/api/config/apply"},
	}
	srv.handlePlainHTTP(connRel, reqRel)
	if !strings.Contains(connRel.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for relative URI targeting 127.0.0.1:8125, got: %s", connRel.writeBuf.String())
	}

	// 4b. Absolute URI targeting proxy port (8124) with Host: innocent.com -> MUST be blocked with 404 (loop prevention)
	connLoop := &dummyConn{}
	reqLoop := &http.Request{
		Method: http.MethodGet,
		Host:   "innocent.com",
		URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:8124", Path: "/loop"},
	}
	srv.handlePlainHTTP(connLoop, reqLoop)
	if !strings.Contains(connLoop.writeBuf.String(), "404 Not Found") {
		t.Fatalf("expected 404 Not Found loop prevention for 127.0.0.1:8124, got: %s", connLoop.writeBuf.String())
	}

	// 4c. LAN client targeting loopback via absolute URI with Host: innocent.com -> MUST be blocked with 403
	lanConn := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54323},
	}
	reqLAN := &http.Request{
		Method: http.MethodGet,
		Host:   "innocent.com",
		URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:8080", Path: "/service"},
	}
	srv.handlePlainHTTP(lanConn, reqLAN)
	if !strings.Contains(lanConn.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for LAN client targeting loopback via absolute URI, got: %s", lanConn.writeBuf.String())
	}

	// 4d. Userinfo in absolute URI authority targeting control plane -> MUST be blocked with 403
	for _, targetWithUser := range []string{
		"admin@127.0.0.1:8125",
		"admin:secret@127.0.0.1:8125",
		"user:pass@[::1]:8125",
		"admin:secret@localhost:8125",
	} {
		conn := &dummyConn{}
		req := &http.Request{
			Method: http.MethodPost,
			Host:   "innocent.com",
			URL:    &url.URL{Scheme: "http", Host: targetWithUser, Path: "/api/config/apply"},
		}
		srv.handlePlainHTTP(conn, req)
		if !strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("expected 403 Forbidden for userinfo authority targeting %s, got: %s", targetWithUser, conn.writeBuf.String())
		}
	}

	// 4e. Userinfo in Host header for relative URI targeting control plane -> MUST be blocked with 403
	connUserHost := &dummyConn{}
	reqUserHost := &http.Request{
		Method: http.MethodPost,
		Host:   "admin:secret@127.0.0.1:8125",
		URL:    &url.URL{Path: "/api/config/apply"},
	}
	srv.handlePlainHTTP(connUserHost, reqUserHost)
	if !strings.Contains(connUserHost.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for userinfo in Host header targeting 127.0.0.1:8125, got: %s", connUserHost.writeBuf.String())
	}

	// 4f. Internal cert / PAC endpoints explicitly targeting control port (8125) MUST be blocked with 403, not served
	for _, p := range []string{"/ca.crt", "/proxy.pac"} {
		conn := &dummyConn{}
		req := &http.Request{
			Method: http.MethodGet,
			Host:   "127.0.0.1:8125",
			URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:8125", Path: p},
		}
		srv.handlePlainHTTP(conn, req)
		if !strings.Contains(conn.writeBuf.String(), "403 Forbidden") {
			t.Fatalf("expected 403 Forbidden for %s targeting control port 8125, got: %s", p, conn.writeBuf.String())
		}
	}

	// 4g. Internal cert endpoint targeting proxy port (8124) MUST return 200 OK
	connCA := &dummyConn{}
	reqCA := &http.Request{
		Method: http.MethodGet,
		Host:   "127.0.0.1:8124",
		URL:    &url.URL{Scheme: "http", Host: "127.0.0.1:8124", Path: "/ca.crt"},
	}
	srv.handlePlainHTTP(connCA, reqCA)
	if !strings.Contains(connCA.writeBuf.String(), "200 OK") {
		t.Fatalf("expected 200 OK for /ca.crt targeting proxy port 8124, got: %s", connCA.writeBuf.String())
	}

	// 4h. Forwarding absolute-form request with spoofed Host header:
	// Client sends http://innocent.com/api with Host: 127.0.0.1:8125.
	// Proxy MUST forward to innocent.com and NOT block locally because actual target is innocent.com.
	// Per RFC 9112 §3.2.2, a proxy forwarding an absolute-form request MUST ignore the received
	// Host header and generate a new Host field-value based on the received request-target ("innocent.com").
	connExt := &dummyConn{}
	reqExt := &http.Request{
		Method: http.MethodGet,
		Host:   "127.0.0.1:8125",
		URL:    &url.URL{Scheme: "http", Host: "innocent.com", Path: "/api"},
	}
	srv.handlePlainHTTP(connExt, reqExt)
	if !strings.Contains(connExt.writeBuf.String(), "200 OK") {
		t.Fatalf("expected 200 OK forwarding external target with custom Host header, got: %s", connExt.writeBuf.String())
	}
	if !strings.Contains(lastTargetURL, "innocent.com/api") {
		t.Fatalf("expected upstream URL targeting innocent.com/api, got: %s", lastTargetURL)
	}
	if lastTargetHostHeader != "innocent.com" {
		t.Fatalf("expected forwarded Host header normalized to innocent.com per RFC 9112 §3.2.2, got: %s", lastTargetHostHeader)
	}

	// 4i. IPv6 literal target forwarding with default port 80 formatted with brackets
	connIPv6 := &dummyConn{}
	reqIPv6 := &http.Request{
		Method: http.MethodGet,
		Host:   "2001:db8::1",
		URL:    &url.URL{Path: "/test"},
	}
	srv.handlePlainHTTP(connIPv6, reqIPv6)
	if !strings.Contains(connIPv6.writeBuf.String(), "200 OK") {
		t.Fatalf("expected 200 OK for IPv6 literal target, got: %s", connIPv6.writeBuf.String())
	}
	if !strings.Contains(lastTargetURL, "http://[2001:db8::1]/test") {
		t.Fatalf("expected correctly bracketed IPv6 URL http://[2001:db8::1]/test, got: %s", lastTargetURL)
	}
}

// TestDNSLoopbackPenetration_CONNECT_Blocked validates H-01:
// In AllowLAN=true scenario, CONNECT targets resolving to loopback addresses
// (e.g. 127.0.0.1.nip.io:<ControlPort> and IPv6 ::1 equivalents) must be blocked
// with 403 Forbidden. The test verifies the actual dialed target to ensure that
// verified destination addresses are dialed directly without secondary DNS queries,
// and that no tunnel connects to the local Control Plane.
func TestDNSLoopbackPenetration_CONNECT_Blocked(t *testing.T) {
	tempDir := t.TempDir()
	cfgMgr := config.NewManager(filepath.Join(tempDir, "config.json"))
	cfgMgr.Update(func(c *config.Config) {
		c.ListenPort = 8124
		c.ControlPort = 8125
		c.AllowLAN = true
		c.UpstreamProxy = "direct"
	})
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()
	defer stats.Close()

	srv := NewProxyServer(cfgMgr, &cert.Manager{}, cacheMgr, stats)

	// Controlled DNS resolution
	var dnsLookupCount atomic.Int32
	srv.lookupIPFn = func(ctx context.Context, host string) ([]net.IP, error) {
		dnsLookupCount.Add(1)
		switch strings.ToLower(host) {
		case "127.0.0.1.nip.io":
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		case "loopback6.example.test":
			return []net.IP{net.ParseIP("::1")}, nil
		case "lan-internal.example.test":
			return []net.IP{net.ParseIP("127.0.0.2")}, nil
		case "api.legitimate-external.com":
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		default:
			return nil, fmt.Errorf("unknown test host: %s", host)
		}
	}

	var dialedAddrs []string
	var dialMu sync.Mutex
	srv.dialContextFn = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialMu.Lock()
		dialedAddrs = append(dialedAddrs, addr)
		dialMu.Unlock()
		// Return an in-memory client connection for mock dialing
		clientSide, serverSide := net.Pipe()
		go func() {
			_, _ = io.Copy(io.Discard, serverSide)
			_ = serverSide.Close()
		}()
		return clientSide, nil
	}

	// 1. LAN client targeting 127.0.0.1.nip.io:8125 (ControlPort) via CONNECT -> MUST be 403
	lanConn1 := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54321},
	}
	srv.handlePassthroughTunnel(lanConn1, strings.NewReader(""), "127.0.0.1.nip.io:8125")
	if !strings.Contains(lanConn1.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for CONNECT to 127.0.0.1.nip.io:8125, got: %s", lanConn1.writeBuf.String())
	}

	// 2. LAN client targeting IPv6 loopback domain (loopback6.example.test:8125) via CONNECT -> MUST be 403
	lanConn2 := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54322},
	}
	srv.handlePassthroughTunnel(lanConn2, strings.NewReader(""), "loopback6.example.test:8125")
	if !strings.Contains(lanConn2.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for CONNECT to loopback6.example.test:8125, got: %s", lanConn2.writeBuf.String())
	}

	// 3. Local client targeting 127.0.0.1.nip.io:8125 (ControlPort) via CONNECT -> MUST be 403
	localConn := &dummyConn{}
	srv.handlePassthroughTunnel(localConn, strings.NewReader(""), "127.0.0.1.nip.io:8125")
	if !strings.Contains(localConn.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for local CONNECT to 127.0.0.1.nip.io:8125, got: %s", localConn.writeBuf.String())
	}

	// 4. LAN client targeting non-control loopback port (127.0.0.1.nip.io:9000) via CONNECT -> MUST be 403
	lanConn3 := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54323},
	}
	srv.handlePassthroughTunnel(lanConn3, strings.NewReader(""), "127.0.0.1.nip.io:9000")
	if !strings.Contains(lanConn3.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for LAN CONNECT to 127.0.0.1.nip.io:9000, got: %s", lanConn3.writeBuf.String())
	}

	// 5. Plain HTTP targeting control port via DNS loopback -> MUST be 403
	plainConn := &dummyConn{}
	reqPlain := &http.Request{
		Method: http.MethodPost,
		Host:   "innocent.com",
		URL:    &url.URL{Scheme: "http", Host: "127.0.0.1.nip.io:8125", Path: "/api/config/apply"},
	}
	srv.handlePlainHTTP(plainConn, reqPlain)
	if !strings.Contains(plainConn.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for plain HTTP targeting 127.0.0.1.nip.io:8125, got: %s", plainConn.writeBuf.String())
	}

	// 6. Plain HTTP from LAN client targeting loopback port via DNS -> MUST be 403
	lanPlainConn := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54324},
	}
	reqLANPlain := &http.Request{
		Method: http.MethodGet,
		Host:   "innocent.com",
		URL:    &url.URL{Scheme: "http", Host: "127.0.0.1.nip.io:8080", Path: "/admin"},
	}
	srv.handlePlainHTTP(lanPlainConn, reqLANPlain)
	if !strings.Contains(lanPlainConn.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for LAN client plain HTTP targeting loopback via DNS, got: %s", lanPlainConn.writeBuf.String())
	}

	// Verify that NO dials were made to loopback addresses or control plane
	dialMu.Lock()
	for _, dialed := range dialedAddrs {
		if strings.HasPrefix(dialed, "127.") || strings.HasPrefix(dialed, "[::1]") || strings.Contains(dialed, "8125") {
			t.Fatalf("forbidden dial made to loopback/control port: %s", dialed)
		}
	}
	dialMu.Unlock()

	// 7. Legitimate external target with port 8125:
	// Verify that it connects to the actual VERIFIED IP (93.184.216.34:8125), not the hostname string
	extConn := &dummyConn{}
	srv.handlePassthroughTunnel(extConn, strings.NewReader(""), "api.legitimate-external.com:8125")
	if !strings.Contains(extConn.writeBuf.String(), "200 Connection Established") {
		t.Fatalf("expected 200 Connection Established for external port 8125, got: %s", extConn.writeBuf.String())
	}
	dialMu.Lock()
	foundExternalDial := false
	for _, dialed := range dialedAddrs {
		if dialed == "93.184.216.34:8125" {
			foundExternalDial = true
			break
		}
	}
	dialMu.Unlock()
	if !foundExternalDial {
		t.Fatalf("expected dialed address to be verified IP 93.184.216.34:8125, dialed: %v", dialedAddrs)
	}

	// 8. Normal LAN client request to port 443 (e.g. gbf.game.mbga.jp:443):
	// Verify that NO blocking DNS queries are invoked on the normal request path!
	dnsCountBefore := dnsLookupCount.Load()
	lanConnNormal := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54325},
	}
	srv.handlePassthroughTunnel(lanConnNormal, strings.NewReader(""), "gbf.game.mbga.jp:443")
	if !strings.Contains(lanConnNormal.writeBuf.String(), "200 Connection Established") {
		t.Fatalf("expected 200 Connection Established for normal LAN request to port 443, got: %s", lanConnNormal.writeBuf.String())
	}
	dnsCountAfter := dnsLookupCount.Load()
	if dnsCountAfter != dnsCountBefore {
		t.Fatalf("expected 0 DNS lookups on normal request path to port 443, got %d lookups", dnsCountAfter-dnsCountBefore)
	}
	dialMu.Lock()
	foundNormalDial := false
	for _, dialed := range dialedAddrs {
		if dialed == "gbf.game.mbga.jp:443" {
			foundNormalDial = true
			break
		}
	}
	dialMu.Unlock()
	if !foundNormalDial {
		t.Fatalf("expected direct dial to gbf.game.mbga.jp:443 on normal path, dialed: %v", dialedAddrs)
	}

	// 9. IPv6 dashed format loopback (--1.sslip.io:8125) via CONNECT -> MUST be 403
	lanConnIPv6Dashed := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54326},
	}
	srv.handlePassthroughTunnel(lanConnIPv6Dashed, strings.NewReader(""), "--1.sslip.io:8125")
	if !strings.Contains(lanConnIPv6Dashed.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for CONNECT to --1.sslip.io:8125, got: %s", lanConnIPv6Dashed.writeBuf.String())
	}

	// 10. Wildcard loopback domain (foo.localtest.me:8125) via CONNECT -> MUST be 403
	lanConnLocaltest := &lanClientDummyConn{
		remote: &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 54327},
	}
	srv.handlePassthroughTunnel(lanConnLocaltest, strings.NewReader(""), "foo.localtest.me:8125")
	if !strings.Contains(lanConnLocaltest.writeBuf.String(), "403 Forbidden") {
		t.Fatalf("expected 403 Forbidden for CONNECT to foo.localtest.me:8125, got: %s", lanConnLocaltest.writeBuf.String())
	}
}


