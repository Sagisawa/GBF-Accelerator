//go:build !windows && !darwin
// +build !windows,!darwin

package startup

func isStartupEnabled() bool {
	return false
}

func setStartupEnabled(enabled bool) error {
	return nil
}
