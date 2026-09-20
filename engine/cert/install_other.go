//go:build !windows && !darwin
// +build !windows,!darwin

package cert

func isCAInstalled(sha1 string, _ string) bool {
	return true
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
