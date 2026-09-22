//go:build !windows && !darwin
// +build !windows,!darwin

package cert

func isCAInstalled(sha1 string, _ string) bool {
	return true
}

// detectCAStore cannot distinguish HKCU/HKLM on this platform.
func detectCAStore(sha1 string) string {
	return ""
}

func installCA(caPath string) error {
	return nil
}

func uninstallCA(sha1 string) error {
	return nil
}

func checkLegacyLeakedCAInstalled() bool {
	return false
}

func cleanLegacyLeakedCA() error {
	return nil
}
