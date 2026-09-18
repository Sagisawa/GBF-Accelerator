//go:build windows

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// prepareAppURL appends standalone=1 to the query string if not already present.
func prepareAppURL(url string) string {
	appURL := url
	if !strings.Contains(appURL, "standalone=1") {
		if strings.Contains(appURL, "?") {
			appURL += "&standalone=1"
		} else {
			appURL += "?standalone=1"
		}
	}
	return appURL
}

// openAppWindow launches Microsoft Edge, Google Chrome, or Chromium-based browsers in standalone App mode (--app=<url>).
// This opens a clean, borderless native window with Win32 title bar and no address bar or tabs.
func openAppWindow(url string) error {
	appURL := prepareAppURL(url)

	browserPath := findAppBrowserWindows()
	if browserPath != "" {
		cmd := exec.Command(browserPath, fmt.Sprintf("--app=%s", appURL), "--window-size=820,960")
		if err := cmd.Start(); err == nil {
			go func() {
				_ = cmd.Wait()
			}()
			return nil
		}
	}

	return OpenBrowser(url)
}

// findAppBrowserWindows searches standard install locations, system registry, and PATH for Edge, Chrome, or Brave.
func findAppBrowserWindows() string {
	var candidates []string

	progFiles86 := os.Getenv("ProgramFiles(x86)")
	progFiles := os.Getenv("ProgramFiles")
	localAppData := os.Getenv("LocalAppData")

	if progFiles86 != "" {
		candidates = append(candidates,
			filepath.Join(progFiles86, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(progFiles86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(progFiles86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	if progFiles != "" {
		candidates = append(candidates,
			filepath.Join(progFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(progFiles, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(progFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	if localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(localAppData, "Chromium", "Application", "chrome.exe"),
		)
	}

	// Hardcoded standard locations as fallback
	candidates = append(candidates,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
	)

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}

	// Query Windows registry as fallback with hidden window to prevent console flashes
	regKeys := []string{
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\brave.exe`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\brave.exe`,
	}
	for _, key := range regKeys {
		cmd := exec.Command("reg", "query", key, "/ve")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				if strings.Contains(line, "REG_SZ") {
					parts := strings.SplitN(line, "REG_SZ", 2)
					if len(parts) == 2 {
						p := strings.Trim(strings.TrimSpace(parts[1]), "\"")
						if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
							return p
						}
					}
				}
			}
		}
	}

	// Look in system PATH
	for _, bin := range []string{"msedge.exe", "chrome.exe", "brave.exe"} {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
	}

	return ""
}
