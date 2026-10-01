//go:build !windows && !darwin
// +build !windows,!darwin

package startup

import (
	"fmt"
	"runtime"
)

func isStartupEnabled() bool {
	return false
}

func setStartupEnabled(enabled bool) error {
	if enabled {
		return fmt.Errorf("auto-startup is not supported on %s", runtime.GOOS)
	}
	return nil
}
