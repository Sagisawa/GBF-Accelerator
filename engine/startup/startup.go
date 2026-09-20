package startup

import "runtime"

const AppName = "GBF_Accelerator"

func IsStartupSupported() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func IsStartupEnabled() bool {
	if !IsStartupSupported() {
		return false
	}
	return isStartupEnabled()
}

func SetStartupEnabled(enabled bool) error {
	if !IsStartupSupported() {
		return nil
	}
	return setStartupEnabled(enabled)
}
