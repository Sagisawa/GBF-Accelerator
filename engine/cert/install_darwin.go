//go:build darwin
// +build darwin

package cert

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const rootCAName = "GBF Local Accelerator Root CA"

func getLoginKeychainPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	if fi, err := os.Stat(keychain); err == nil && !fi.IsDir() {
		return keychain
	}
	legacy := filepath.Join(home, "Library", "Keychains", "login.keychain")
	if fi, err := os.Stat(legacy); err == nil && !fi.IsDir() {
		return legacy
	}
	return keychain
}

func deleteCertificateBySHA1(sha1 string) {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return
	}
	keychain := getLoginKeychainPath()
	if keychain != "" {
		cmd := exec.Command("security", "delete-certificate", "-Z", cleanSHA1, "-t", keychain)
		_ = cmd.Run()
	}
	cmdAll := exec.Command("security", "delete-certificate", "-Z", cleanSHA1, "-t")
	_ = cmdAll.Run()
}

func findInstalledCASHAs(caName string) []string {
	if caName == "" {
		caName = rootCAName
	}
	cmd := exec.Command("security", "find-certificate", "-a", "-c", caName, "-Z")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseSHA1sFromOutput(string(out))
}

func parseSHA1sFromOutput(output string) []string {
	var shas []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SHA-1 hash:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				sha := strings.ToUpper(strings.TrimSpace(parts[1]))
				if len(sha) == 40 && !seen[sha] {
					seen[sha] = true
					shas = append(shas, sha)
				}
			}
		}
	}
	return shas
}

func cleanupStaleCAs(keepSHA1 string) {
	cleanKeep := strings.ToUpper(strings.TrimSpace(keepSHA1))
	existingSHAs := findInstalledCASHAs(rootCAName)
	for _, sha := range existingSHAs {
		if cleanKeep == "" || sha != cleanKeep {
			deleteCertificateBySHA1(sha)
		}
	}
}

func parseTrustSettingsOutput(output string, targetName string) bool {
	lines := strings.Split(output, "\n")
	matchedCert := false
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "Cert ") && strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && strings.Contains(strings.TrimSpace(parts[1]), targetName) {
				matchedCert = true
			} else {
				matchedCert = false
			}
			continue
		}
		if matchedCert {
			// Check if explicit trust result is present
			if strings.Contains(line, "kSecTrustSettingsResultTrustRoot") ||
				strings.Contains(line, "kSecTrustSettingsResultTrustAsRoot") {
				return true
			}
			// Check number of trust settings: non-zero means trust configured
			if strings.HasPrefix(line, "Number of trust settings :") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cntStr := strings.TrimSpace(parts[1])
					if cntStr != "0" && cntStr != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

func checkTrustSettings(caName string) bool {
	// Check user trust domain (default)
	cmdUser := exec.Command("security", "dump-trust-settings")
	if out, err := cmdUser.Output(); err == nil && len(out) > 0 {
		if parseTrustSettingsOutput(string(out), caName) {
			return true
		}
	}
	// Check admin trust domain (-d)
	cmdAdmin := exec.Command("security", "dump-trust-settings", "-d")
	if out, err := cmdAdmin.Output(); err == nil && len(out) > 0 {
		if parseTrustSettingsOutput(string(out), caName) {
			return true
		}
	}
	return false
}

func verifyCertFile(caPath string) bool {
	if caPath == "" {
		return false
	}
	fi, err := os.Stat(caPath)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return false
	}
	cmd := exec.Command("security", "verify-cert", "-c", caPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		outStr := strings.ToLower(string(out))
		if strings.Contains(outStr, "successful") || len(out) == 0 {
			return true
		}
	}
	return false
}

func isCAInstalled(sha1 string, caPath string) bool {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return false
	}

	// 1. Check if the certificate exists in keychain matching the specific SHA-1
	shas := findInstalledCASHAs(rootCAName)
	found := false
	for _, s := range shas {
		if s == cleanSHA1 {
			found = true
			break
		}
	}
	if !found {
		cmdAll := exec.Command("security", "find-certificate", "-a", "-Z")
		if outAll, err := cmdAll.Output(); err == nil {
			outUpper := strings.ToUpper(strings.ReplaceAll(string(outAll), " ", ""))
			if !strings.Contains(outUpper, cleanSHA1) {
				return false
			}
		} else {
			return false
		}
	}

	// 2. Trust evaluation to prevent false positive (0 trust settings)
	// Verification path A: evaluate via macOS Security Framework verify-cert
	if verifyCertFile(caPath) {
		return true
	}

	// Verification path B: check explicit trust settings in user/admin domains
	return checkTrustSettings(rootCAName)
}

// detectCAStore cannot distinguish HKCU/HKLM on macOS (Keychain-based).
func detectCAStore(sha1 string) string {
	return ""
}

func installCA(caPath string) error {
	cleanPath, err := filepath.Abs(caPath)
	if err != nil {
		cleanPath = caPath
	}
	if fi, err := os.Stat(cleanPath); err != nil || fi.IsDir() || fi.Size() == 0 {
		return fmt.Errorf("ca certificate not found at %s", cleanPath)
	}

	keychain := getLoginKeychainPath()
	if keychain == "" {
		return fmt.Errorf("login keychain not found")
	}

	var currentSHA1 string
	if certBytes, err := os.ReadFile(cleanPath); err == nil {
		currentSHA1 = GetCAFingerprintSHA1(certBytes)
	}

	// CA Hygiene: Clean up stale/duplicate or 0-trust certificates with the same CA name
	cleanupStaleCAs("")

	// Import into user login keychain with trustRoot policy (User domain, no -d)
	cmd := exec.Command("security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, cleanPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("security add-trusted-cert failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// Verify that trust is actually effective
	if currentSHA1 != "" && !isCAInstalled(currentSHA1, cleanPath) {
		return fmt.Errorf("certificate was imported into keychain but trust verification failed; please verify trust settings in Keychain Access")
	}

	return nil
}

func uninstallCA(sha1 string) error {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return fmt.Errorf("empty sha1 thumbprint")
	}

	keychain := getLoginKeychainPath()

	// 1. Delete by exact SHA-1 from login keychain
	if keychain != "" {
		cmd := exec.Command("security", "delete-certificate", "-Z", cleanSHA1, "-t", keychain)
		_ = cmd.Run()
	}
	// 2. Delete by exact SHA-1 from default keychain list
	cmdAll := exec.Command("security", "delete-certificate", "-Z", cleanSHA1, "-t")
	_ = cmdAll.Run()

	// 3. Clean up any remaining/duplicate CAs with the root CA name
	cleanupStaleCAs("")

	// 4. Delete by name as well to guarantee keychain hygiene
	if keychain != "" {
		cmdName := exec.Command("security", "delete-certificate", "-c", rootCAName, "-t", keychain)
		_ = cmdName.Run()
	}
	cmdNameAll := exec.Command("security", "delete-certificate", "-c", rootCAName, "-t")
	_ = cmdNameAll.Run()

	return nil
}

func checkLegacyLeakedCAInstalled() bool {
	cmd := exec.Command("security", "find-certificate", "-c", "GBF Speed CA")
	return cmd.Run() == nil
}

func cleanLegacyLeakedCA() error {
	_ = uninstallCA(LegacyLeakedCASHA1)
	cmd := exec.Command("security", "delete-certificate", "-c", "GBF Speed CA", "-t")
	_ = cmd.Run()
	return nil
}
