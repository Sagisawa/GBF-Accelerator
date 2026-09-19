package cert

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const LegacyLeakedCASHA1 = "51E9AA40A64FB8DC63F18F4B1A11B98D1CF8D3FF"

// GetCAFingerprintSHA256 returns colon-separated uppercase hex SHA-256 fingerprint.
func GetCAFingerprintSHA256(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	hexStr := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(hexStr); i += 2 {
		parts = append(parts, hexStr[i:i+2])
	}
	return strings.Join(parts, ":")
}

// GetCAFingerprintSHA1 returns unseparated uppercase hex SHA-1 thumbprint.
func GetCAFingerprintSHA1(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	sum := sha1.Sum(block.Bytes)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func (m *Manager) GetFingerprintSHA256() string {
	return GetCAFingerprintSHA256(m.caPEM)
}

func (m *Manager) GetFingerprintSHA1() string {
	return GetCAFingerprintSHA1(m.caPEM)
}

func (m *Manager) IsInstalled() bool {
	sha1 := m.GetFingerprintSHA1()
	if sha1 == "" {
		return false
	}
	return isCAInstalled(sha1)
}

func (m *Manager) Install(certsDir string) error {
	if certsDir == "" {
		if m.certsDir != "" {
			certsDir = m.certsDir
		} else {
			certsDir = "certs"
		}
		caPath := filepath.Join(certsDir, "ca.crt")
		if fi, err := os.Stat(caPath); err != nil || fi.Size() == 0 {
			if len(m.caPEM) > 0 {
				_ = os.MkdirAll(certsDir, 0755)
				_ = os.WriteFile(caPath, m.caPEM, 0644)
			}
		}
	}
	caPath := filepath.Join(certsDir, "ca.crt")
	if fi, err := os.Stat(caPath); err != nil || fi.Size() == 0 {
		return fmt.Errorf("ca.crt not found at %s", caPath)
	}
	absPath, err := filepath.Abs(caPath)
	if err != nil {
		absPath = caPath
	}
	return installCA(absPath)
}

func (m *Manager) Uninstall() error {
	sha1 := m.GetFingerprintSHA1()
	if sha1 == "" {
		return fmt.Errorf("no valid CA certificate loaded")
	}
	return uninstallCA(sha1)
}

func CheckLegacyLeakedCAInstalled() bool {
	return checkLegacyLeakedCAInstalled()
}

func CleanLegacyLeakedCA() error {
	return cleanLegacyLeakedCA()
}
