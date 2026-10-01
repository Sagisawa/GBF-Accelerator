//go:build !windows && !darwin
// +build !windows,!darwin

package cert

import (
	"fmt"
	"runtime"
)

func isCAInstalled(sha1 string, _ string) bool {
	return false
}

// detectCAStore cannot distinguish HKCU/HKLM on this platform.
func detectCAStore(sha1 string) string {
	return ""
}

func installCA(caPath string) error {
	return fmt.Errorf("automated system CA certificate installation is not supported on %s; please install ca.crt manually", runtime.GOOS)
}

func uninstallCA(sha1 string) error {
	return fmt.Errorf("automated system CA certificate removal is not supported on %s", runtime.GOOS)
}

func checkLegacyLeakedCAInstalled() bool {
	return false
}

func cleanLegacyLeakedCA() error {
	return nil
}
