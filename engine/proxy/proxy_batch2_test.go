package proxy

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/telemetry"
)

// TestBug6_ShimakazeModeInsecureTLS verifies that when ShimakazeMode is enabled,
// both apiClient and assetClient skip upstream TLS verification so self-signed
// upstream certs (e.g. from ShimakazeGO) do not cause TLS handshake errors.
func TestBug6_ShimakazeModeInsecureTLS(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	// 1. Default: ShimakazeMode = false, VerifyUpstreamTLS = true -> InsecureSkipVerify must be false
	cfgMgr.Update(func(c *config.Config) {
		c.ShimakazeMode = false
		c.VerifyUpstreamTLS = true
	})
	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	defer srv.Stop()

	apiTr := srv.getAPIClient().Transport.(*http.Transport)
	assetTr := srv.getAssetClient().Transport.(*http.Transport)

	if apiTr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("apiClient should NOT skip TLS verification when ShimakazeMode=false and VerifyUpstreamTLS=true")
	}
	if assetTr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("assetClient should NOT skip TLS verification when ShimakazeMode=false and VerifyUpstreamTLS=true")
	}

	// 2. Enable ShimakazeMode = true with VerifyUpstreamTLS = true -> InsecureSkipVerify must be true
	cfgMgr.Update(func(c *config.Config) {
		c.ShimakazeMode = true
		c.VerifyUpstreamTLS = true
	})

	apiTr = srv.getAPIClient().Transport.(*http.Transport)
	assetTr = srv.getAssetClient().Transport.(*http.Transport)

	if !apiTr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("apiClient MUST skip TLS verification when ShimakazeMode=true")
	}
	if !assetTr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("assetClient MUST skip TLS verification when ShimakazeMode=true")
	}

	// 3. Disable ShimakazeMode, but VerifyUpstreamTLS = false -> InsecureSkipVerify must still be true
	cfgMgr.Update(func(c *config.Config) {
		c.ShimakazeMode = false
		c.VerifyUpstreamTLS = false
	})

	apiTr = srv.getAPIClient().Transport.(*http.Transport)
	assetTr = srv.getAssetClient().Transport.(*http.Transport)

	if !apiTr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("apiClient MUST skip TLS verification when VerifyUpstreamTLS=false")
	}
	if !assetTr.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("assetClient MUST skip TLS verification when VerifyUpstreamTLS=false")
	}
}

// generateSelfSignedCert creates a self-signed TLS certificate for localhost/127.0.0.1 testing.
func generateSelfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Proxy"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}
}

// TestBug4_HandlePassthroughTunnel_HTTPSUpstream verifies that when an upstream proxy
// is configured with scheme "https://", handlePassthroughTunnel correctly performs a TLS
// handshake before sending the CONNECT command, and establishes a bidirectional tunnel.
func TestBug4_HandlePassthroughTunnel_HTTPSUpstream(t *testing.T) {
	cert := generateSelfSignedCert(t)
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	// 1. Start mock HTTPS upstream proxy server
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsConf)
	if err != nil {
		t.Fatalf("failed to start mock HTTPS proxy: %v", err)
	}
	defer ln.Close()

	proxyPort := ln.Addr().(*net.TCPAddr).Port

	// Mock HTTPS proxy handler: accepts TLS connection, reads CONNECT, replies 200, echoes data
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				if req.Method != http.MethodConnect {
					return
				}
				if !strings.Contains(req.Host, "game.granbluefantasy.jp") {
					return
				}

				// Respond 200 Connection Established
				_, _ = c.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

				// Echo loop: read from client, write back
				buf := make([]byte, 1024)
				for {
					n, rErr := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(append([]byte("ECHO:"), buf[:n]...))
					}
					if rErr != nil {
						break
					}
				}
			}(conn)
		}
	}()

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.UpstreamProxy = fmt.Sprintf("https://127.0.0.1:%d", proxyPort)
		c.DirectMode = false
		c.ShimakazeMode = true // Skips TLS verification on test self-signed cert
	})

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	defer srv.Stop()

	// 2. Set up client connection using net.Pipe
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	tunnelDone := make(chan struct{})
	go func() {
		defer close(tunnelDone)
		srv.handlePassthroughTunnel(serverSide, serverSide, "game.granbluefantasy.jp:443")
	}()

	// 3. Read initial response on clientSide: must be HTTP/1.1 200 Connection Established
	clientReader := bufio.NewReader(clientSide)
	statusLine, err := clientReader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read status line: %v", err)
	}
	if !strings.Contains(statusLine, "200 Connection Established") {
		t.Fatalf("expected 200 Connection Established, got: %s", statusLine)
	}

	// Read empty line after headers
	for {
		line, err := clientReader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read header: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	// 4. Test bidirectional communication through the tunnel
	testPayload := []byte("hello-gbf")
	if _, err := clientSide.Write(testPayload); err != nil {
		t.Fatalf("failed to write to tunnel: %v", err)
	}

	echoBuf := make([]byte, 1024)
	n, err := clientReader.Read(echoBuf)
	if err != nil && err != io.EOF {
		t.Fatalf("failed to read echo: %v", err)
	}

	expected := "ECHO:hello-gbf"
	if string(echoBuf[:n]) != expected {
		t.Errorf("expected %q, got %q", expected, string(echoBuf[:n]))
	}

	_ = clientSide.Close()
	<-tunnelDone
}

// TestBug4_HandlePassthroughTunnel_HTTPSUpstream_TLSVerificationEnforced verifies that
// when ShimakazeMode is false and VerifyUpstreamTLS is true, a self-signed HTTPS proxy
// fails TLS handshake and the client receives HTTP 502 Bad Gateway.
func TestBug4_HandlePassthroughTunnel_HTTPSUpstream_TLSVerificationEnforced(t *testing.T) {
	cert := generateSelfSignedCert(t)
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsConf)
	if err != nil {
		t.Fatalf("failed to start mock HTTPS proxy: %v", err)
	}
	defer ln.Close()

	proxyPort := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.UpstreamProxy = fmt.Sprintf("https://127.0.0.1:%d", proxyPort)
		c.DirectMode = false
		c.ShimakazeMode = false
		c.VerifyUpstreamTLS = true // Must strictly verify TLS
	})

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	defer srv.Stop()

	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	tunnelDone := make(chan struct{})
	go func() {
		defer close(tunnelDone)
		srv.handlePassthroughTunnel(serverSide, serverSide, "game.granbluefantasy.jp:443")
	}()

	clientReader := bufio.NewReader(clientSide)
	statusLine, err := clientReader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read status line: %v", err)
	}
	if !strings.Contains(statusLine, "502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway when TLS verification fails, got: %s", statusLine)
	}

	_ = clientSide.Close()
	<-tunnelDone
}

// TestBug4_HandlePassthroughTunnel_HTTPSUpstream_Non200Error verifies that
// if the upstream proxy returns a non-200 status (e.g. 403 Forbidden) to CONNECT,
// handlePassthroughTunnel aborts and returns 502 Bad Gateway to the client.
func TestBug4_HandlePassthroughTunnel_HTTPSUpstream_Non200Error(t *testing.T) {
	cert := generateSelfSignedCert(t)
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsConf)
	if err != nil {
		t.Fatalf("failed to start mock HTTPS proxy: %v", err)
	}
	defer ln.Close()

	proxyPort := ln.Addr().(*net.TCPAddr).Port

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil || req.Method != http.MethodConnect {
					return
				}
				// Reject CONNECT with 403 Forbidden
				_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
			}(conn)
		}
	}()

	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cfgMgr.Update(func(c *config.Config) {
		c.UpstreamProxy = fmt.Sprintf("https://127.0.0.1:%d", proxyPort)
		c.DirectMode = false
		c.ShimakazeMode = true
	})

	cacheMgr := cache.NewManager(tempDir, 16)
	defer cacheMgr.Close()
	stats := telemetry.NewStats()

	srv := NewProxyServer(cfgMgr, nil, cacheMgr, stats)
	defer srv.Stop()

	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()

	tunnelDone := make(chan struct{})
	go func() {
		defer close(tunnelDone)
		srv.handlePassthroughTunnel(serverSide, serverSide, "game.granbluefantasy.jp:443")
	}()

	clientReader := bufio.NewReader(clientSide)
	statusLine, err := clientReader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read status line: %v", err)
	}
	if !strings.Contains(statusLine, "502 Bad Gateway") {
		t.Fatalf("expected 502 Bad Gateway when upstream returns 403, got: %s", statusLine)
	}

	_ = clientSide.Close()
	<-tunnelDone
}

