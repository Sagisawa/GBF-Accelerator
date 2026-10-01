//go:build !windows && !darwin
// +build !windows,!darwin

package sysproxy

import (
	"fmt"
	"runtime"
)

func getCurrentPACURL() string {
	return ""
}

func enablePACProxy(pacURL string) error {
	return fmt.Errorf("automated system PAC proxy is not supported on %s; please configure proxy manually", runtime.GOOS)
}

func disablePACProxy(force bool) error {
	return nil
}

func checkProxyConflict(port int) string {
	return ""
}
