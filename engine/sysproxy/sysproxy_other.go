//go:build !windows && !darwin
// +build !windows,!darwin

package sysproxy

func getCurrentPACURL() string {
	return ""
}

func enablePACProxy(pacURL string) error {
	return nil
}

func disablePACProxy(force bool) error {
	return nil
}

func checkProxyConflict(port int) string {
	return ""
}
