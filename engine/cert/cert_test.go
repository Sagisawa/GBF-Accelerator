package cert

import (
	"crypto/tls"
	"fmt"
	"os"
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
