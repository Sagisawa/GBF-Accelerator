package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Manager struct {
	mu        sync.RWMutex
	caCert    *x509.Certificate
	caKey     *rsa.PrivateKey
	caPEM     []byte
	serverKey *rsa.PrivateKey
	certCache map[string]*tls.Certificate
}

func NewManager(certsDir string) (*Manager, error) {
	if certsDir == "" {
		certsDir = "certs"
	}
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create certs dir: %w", err)
	}

	caCertPath := filepath.Join(certsDir, "ca.crt")
	caKeyPath := filepath.Join(certsDir, "ca.key")

	var caCert *x509.Certificate
	var caKey *rsa.PrivateKey
	var caPEM []byte

	// Check if existing CA exists
	if certBytes, err := os.ReadFile(caCertPath); err == nil {
		if keyBytes, err := os.ReadFile(caKeyPath); err == nil {
			parsedCert, parsedKey, err := parseCA(certBytes, keyBytes)
			if err == nil {
				caCert = parsedCert
				caKey = parsedKey
				caPEM = certBytes
			}
		}
	}

	// If no valid CA loaded, generate a new one
	if caCert == nil || caKey == nil {
		var err error
		caCert, caKey, caPEM, err = generateCA(caCertPath, caKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to generate Root CA: %w", err)
		}
	}

	// Generate a reusable server private key for fast leaf cert signing
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	return &Manager{
		caCert:    caCert,
		caKey:     caKey,
		caPEM:     caPEM,
		serverKey: serverKey,
		certCache: make(map[string]*tls.Certificate),
	}, nil
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, fmt.Errorf("failed to decode ca cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	kBlock, _ := pem.Decode(keyPEM)
	if kBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode ca key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(kBlock.Bytes)
	if err != nil {
		// Try PKCS8
		k8, err2 := x509.ParsePKCS8PrivateKey(kBlock.Bytes)
		if err2 != nil {
			return nil, nil, fmt.Errorf("parse key failed: pkcs1=%v, pkcs8=%v", err, err2)
		}
		rsaKey, ok := k8.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("not an RSA private key")
		}
		key = rsaKey
	}
	return cert, key, nil
}

func generateCA(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, err
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"GBF Local Accelerator"},
			CommonName:   "GBF Local Accelerator Root CA",
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	_ = os.WriteFile(certPath, certPEM, 0644)
	_ = os.WriteFile(keyPath, keyPEM, 0600)

	parsedCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, nil, err
	}
	return parsedCert, key, certPEM, nil
}

func (m *Manager) GetCAPEM() []byte {
	return m.caPEM
}

func (m *Manager) GetOrCreateCert(host string) (*tls.Certificate, error) {
	m.mu.RLock()
	if cert, ok := m.certCache[host]; ok {
		m.mu.RUnlock()
		return cert, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double check
	if cert, ok := m.certCache[host]; ok {
		return cert, nil
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"GBF Local Accelerator"},
		},
		NotBefore:   time.Now().Add(-24 * time.Hour),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, m.caCert, &m.serverKey.PublicKey, m.caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign server cert: %w", err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER, m.caCert.Raw},
		PrivateKey:  m.serverKey,
	}

	m.certCache[host] = tlsCert
	return tlsCert, nil
}

func (m *Manager) GetTLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := hello.ServerName
			if host == "" {
				host = "game.granbluefantasy.jp"
			}
			return m.GetOrCreateCert(host)
		},
	}
}
