package sysproxy

import (
	"fmt"
	"strings"
	"sync"
)

var (
	mu              sync.Mutex
	isManagingProxy bool
	originalPACURL  string
	managedPACURL   string
)

// GetCurrentPACURL retrieves the current system AutoConfigURL if configured.
func GetCurrentPACURL() string {
	return getCurrentPACURL()
}

// IsPACProxyEnabled checks whether the system proxy PAC currently points to our local accelerator.
func IsPACProxyEnabled(port int) bool {
	url := strings.ToLower(strings.TrimSpace(GetCurrentPACURL()))
	if url == "" {
		return false
	}
	if !strings.Contains(url, "/proxy.pac") {
		return false
	}
	if !strings.Contains(url, "127.0.0.1") && !strings.Contains(url, "localhost") {
		return false
	}
	if port > 0 {
		return strings.Contains(url, fmt.Sprintf(":%d/proxy.pac", port))
	}
	return true
}

// CheckProxyConflict checks if an external system proxy or external PAC script is configured.
// Returns a descriptive conflict message if detected, or empty string if clear.
func CheckProxyConflict(port int) string {
	mu.Lock()
	defer mu.Unlock()
	return checkProxyConflict(port)
}

// EnablePACProxy sets the system PAC proxy pointing to local accelerator.
func EnablePACProxy(pacURL string) error {
	mu.Lock()
	defer mu.Unlock()
	if pacURL == "" {
		pacURL = "http://127.0.0.1:8124/proxy.pac"
	}
	err := enablePACProxy(pacURL)
	if err == nil {
		isManagingProxy = true
		managedPACURL = pacURL
	}
	return err
}

// DisablePACProxy restores or disables the system PAC proxy.
func DisablePACProxy(force bool) error {
	mu.Lock()
	defer mu.Unlock()

	currentPAC := strings.TrimSpace(GetCurrentPACURL())
	isManaged := isManagingProxy && managedPACURL != "" && strings.EqualFold(currentPAC, managedPACURL)
	isOur := IsPACProxyEnabled(0)

	// Never overwrite a PAC that was changed by another application/user after
	// we mounted ours. Only force=true may override an externally changed PAC.
	if !force {
		if isManagingProxy && !isManaged {
			isManagingProxy = false
			managedPACURL = ""
			originalPACURL = ""
			return nil
		}
		if !isManagingProxy && !isOur {
			return nil
		}
	}

	err := disablePACProxy(force)
	if err == nil {
		isManagingProxy = false
		managedPACURL = ""
	}
	return err
}

// CleanupOnExit safely restores system proxy settings on application shutdown.
func CleanupOnExit() {
	mu.Lock()
	managing := isManagingProxy
	mu.Unlock()
	if managing {
		_ = DisablePACProxy(false)
	}
}
