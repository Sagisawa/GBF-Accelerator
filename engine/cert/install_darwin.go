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

func isCAInstalled(sha1 string) bool {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return false
	}

	cmd := exec.Command("security", "find-certificate", "-a", "-c", "GBF Local Accelerator Root CA", "-Z")
	out, err := cmd.Output()
	if err == nil {
		outUpper := strings.ToUpper(strings.ReplaceAll(string(out), " ", ""))
		if strings.Contains(outUpper, cleanSHA1) {
			return true
		}
	}

	cmdAll := exec.Command("security", "find-certificate", "-a", "-Z")
	if outAll, err := cmdAll.Output(); err == nil {
		outUpper := strings.ToUpper(strings.ReplaceAll(string(outAll), " ", ""))
		return strings.Contains(outUpper, cleanSHA1)
	}
	return false
}

func installCA(caPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	keychain := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	if fi, err := os.Stat(keychain); err != nil || fi.IsDir() {
		keychain = filepath.Join(home, "Library", "Keychains", "login.keychain")
	}

	cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", keychain, caPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("security add-trusted-cert failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstallCA(sha1 string) error {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return fmt.Errorf("empty sha1 thumbprint")
	}
	cmd := exec.Command("security", "delete-certificate", "-Z", cleanSHA1, "-t")
	_ = cmd.Run()
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
