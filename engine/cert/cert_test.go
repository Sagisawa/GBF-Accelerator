package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
)

func TestCertManager(t *testing.T) {
	tempCertsDir, err := os.MkdirTemp("", "gbf_certs_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempCertsDir)

	mgr, err := NewManager(tempCertsDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	caPEM := mgr.GetCAPEM()
	if len(caPEM) == 0 {
		t.Fatal("Root CA PEM must not be empty")
	}

	// 1. Mint certificate for domain
	tlsCert, err := mgr.GetOrCreateCert("game.granbluefantasy.jp")
	if err != nil {
		t.Fatalf("failed to mint leaf cert: %v", err)
	}
	if len(tlsCert.Certificate) < 2 {
		t.Fatal("expected at least server cert and CA cert in chain")
	}

	// 2. Cached certificate lookup must return same pointer
	tlsCert2, err2 := mgr.GetOrCreateCert("game.granbluefantasy.jp")
	if err2 != nil || tlsCert != tlsCert2 {
		t.Error("expected cached certificate to be returned on second call")
	}

	// 3. Mint certificate for IP address
	ipCert, err := mgr.GetOrCreateCert("127.0.0.1")
	if err != nil {
		t.Fatalf("failed to mint IP cert: %v", err)
	}
	if ipCert == nil {
		t.Fatal("IP cert was nil")
	}

	// 4. Test TLS Config GetCertificate hook
	tlsCfg := mgr.GetTLSConfig()
	clientHello := &tls.ClientHelloInfo{
		ServerName: "prd-game-a-granbluefantasy.akamaized.net",
	}
	helloCert, err := tlsCfg.GetCertificate(clientHello)
	if err != nil || helloCert == nil {
		t.Fatalf("GetCertificate failed: %v", err)
	}
}

func TestCertCacheCapping(t *testing.T) {
	tempCertsDir, err := os.MkdirTemp("", "gbf_certs_cap_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempCertsDir)

	mgr, err := NewManager(tempCertsDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	// Pre-populate certCache to 512 entries
	dummyCert := &tls.Certificate{}
	mgr.mu.Lock()
	for i := 0; i < 512; i++ {
		mgr.certCache[fmt.Sprintf("host%d.granbluefantasy.jp", i)] = dummyCert
	}
	mgr.mu.Unlock()

	// Add 10 more certs via GetOrCreateCert
	for i := 0; i < 10; i++ {
		_, err := mgr.GetOrCreateCert(fmt.Sprintf("newhost%d.granbluefantasy.jp", i))
		if err != nil {
			t.Fatalf("GetOrCreateCert failed: %v", err)
		}
	}

	mgr.mu.RLock()
	cacheSize := len(mgr.certCache)
	mgr.mu.RUnlock()

	if cacheSize > 512 {
		t.Errorf("expected certCache to be capped at 512 entries, got %d", cacheSize)
	}
}

func TestCertManager_ConcurrentRegenerateAndRead(t *testing.T) {
	tempCertsDir := t.TempDir()
	mgr, err := NewManager(tempCertsDir)
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if fp := mgr.GetFingerprintSHA256(); fp == "" {
					t.Errorf("GetFingerprintSHA256 returned empty")
				}
				if fp1 := mgr.GetFingerprintSHA1(); fp1 == "" {
					t.Errorf("GetFingerprintSHA1 returned empty")
				}
				if pem := mgr.GetCAPEM(); len(pem) == 0 {
					t.Errorf("GetCAPEM returned empty")
				}
				if dir := mgr.GetCertsDir(); dir == "" {
					t.Errorf("GetCertsDir returned empty")
				}
				_, _ = mgr.GetOrCreateCert("game.granbluefantasy.jp")
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 3; j++ {
			if err := mgr.RegenerateCA(); err != nil {
				t.Errorf("RegenerateCA failed: %v", err)
			}
		}
	}()

	wg.Wait()
}

func TestCertManager_ECDSALeafAndSessionResumptionAndALPN(t *testing.T) {
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create cert manager: %v", err)
	}

	leaf, err := mgr.GetOrCreateCert("prd-game-a-granbluefantasy.akamaized.net")
	if err != nil {
		t.Fatalf("GetOrCreateCert failed: %v", err)
	}
	ecKey, ok := leaf.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("expected *ecdsa.PrivateKey leaf key, got %T", leaf.PrivateKey)
	}
	if ecKey.Curve != elliptic.P256() {
		t.Fatalf("expected P-256 curve, got %v", ecKey.Curve.Params().Name)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(mgr.GetCAPEM()) {
		t.Fatal("failed to append CA PEM")
	}
	sessionCache := tls.NewLRUClientSessionCache(16)

	handshakeOnce := func() tls.ConnectionState {
		cConn, sConn := net.Pipe()
		srvErrCh := make(chan error, 1)
		go func() {
			defer sConn.Close()
			srvTLS := tls.Server(sConn, mgr.GetTLSConfig())
			err := srvTLS.Handshake()
			if err == nil {
				_, _ = srvTLS.Write([]byte("ok"))
				var buf [8]byte
				_, _ = srvTLS.Read(buf[:])
			}
			srvErrCh <- err
		}()

		cliTLS := tls.Client(cConn, &tls.Config{
			ServerName:         "prd-game-a-granbluefantasy.akamaized.net",
			RootCAs:            pool,
			NextProtos:         []string{"h2", "http/1.1"},
			ClientSessionCache: sessionCache,
		})
		if err := cliTLS.Handshake(); err != nil {
			_ = cConn.Close()
			t.Fatalf("client handshake failed: %v", err)
		}
		var rbuf [8]byte
		_, _ = cliTLS.Read(rbuf[:])
		state := cliTLS.ConnectionState()
		_ = cliTLS.Close()
		if srvErr := <-srvErrCh; srvErr != nil {
			t.Fatalf("server handshake failed: %v", srvErr)
		}
		return state
	}

	st1 := handshakeOnce()
	if st1.NegotiatedProtocol != "h2" {
		t.Fatalf("expected ALPN 'h2', got %q", st1.NegotiatedProtocol)
	}

	st2 := handshakeOnce()
	if !st2.DidResume {
		t.Fatalf("expected second handshake across separate GetTLSConfig() calls to resume TLS session (DidResume=false)")
	}
	if st2.NegotiatedProtocol != "h2" {
		t.Fatalf("expected ALPN 'h2' on resumed handshake, got %q", st2.NegotiatedProtocol)
	}
}

