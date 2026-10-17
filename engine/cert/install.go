package cert

import (
	"bytes"
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
	return GetCAFingerprintSHA256(m.GetCAPEM())
}

func (m *Manager) GetFingerprintSHA1() string {
	return GetCAFingerprintSHA1(m.GetCAPEM())
}

func (m *Manager) IsInstalled() bool {
	sha1 := m.GetFingerprintSHA1()
	if sha1 == "" {
		return false
	}
	caPath := filepath.Join(m.GetCertsDir(), "ca.crt")
	return isCAInstalled(sha1, caPath)
}

// GetInstallStore returns which Windows Root store hive currently contains
// the CA ("HKCU" or "HKLM"). It returns "" when the CA is not detected or
// when the platform cannot distinguish the store (e.g. macOS/Linux).
func (m *Manager) GetInstallStore() string {
	sha1 := m.GetFingerprintSHA1()
	if sha1 == "" {
		return ""
	}
	return detectCAStore(sha1)
}

func (m *Manager) Install(certsDir string) error {
	explicitDir := certsDir != ""
	if certsDir == "" {
		if dir := m.GetCertsDir(); dir != "" {
			certsDir = dir
		} else {
			certsDir = "certs"
		}
	}

	caPath := filepath.Join(certsDir, "ca.crt")
	caPEM := m.GetCAPEM()

	// An explicitly supplied directory must already contain a certificate;
	// never create one there implicitly.
	if explicitDir {
		if fi, err := os.Stat(caPath); err != nil || fi.Size() == 0 {
			return fmt.Errorf("ca.crt not found at %s", caPath)
		}
	}

	// Keep the on-disk CA in sync with the in-memory signing CA. Installing a
	// stale certificate would make leaf certificates unverifiable.
	if len(caPEM) > 0 {
		existing, readErr := os.ReadFile(caPath)
		if readErr != nil || !bytes.Equal(existing, caPEM) {
			if err := os.MkdirAll(certsDir, 0755); err != nil {
				return fmt.Errorf("failed to create certs dir: %w", err)
			}
			if err := os.WriteFile(caPath, caPEM, 0644); err != nil {
				return fmt.Errorf("failed to write ca.crt: %w", err)
			}
		}
	}

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
