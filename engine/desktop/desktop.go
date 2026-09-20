package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Controller provides the callback interface for desktop and tray actions.
type Controller interface {
	IsRunning() bool
	StartProxy() error
	StopProxy()
	GetListenPort() int
	GetControlPort() int
	GetCacheDir() string
	OpenBrowser(url string) error
	OpenAppWindow(url string) error
	OpenFolder(path string) error
	Quit()
}

// Tray defines the system tray lifecycle interface.
type Tray interface {
	Start() error
	Update()
	Stop()
}

// OpenAppWindow opens the specified URL in a dedicated standalone application window without browser chrome.
// On Windows/macOS, it attempts to launch Edge/Chrome in --app mode or platform webview.
// If standalone app mode is unavailable, it gracefully falls back to OpenBrowser.
func OpenAppWindow(url string) error {
	return openAppWindow(url)
}

// OpenBrowser opens the specified URL in the system default web browser.
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

// OpenFolder opens the specified directory path in the OS file explorer.
func OpenFolder(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty path")
	}
	cleanPath := filepath.Clean(path)
	_ = os.MkdirAll(cleanPath, 0755)
	switch runtime.GOOS {
	case "windows":
		// Use Explorer's shell open semantics instead of a bare child-process
		// launch. /n requests a new Explorer window, which also makes a
		// user-initiated folder click reliably surface the window.
		return exec.Command("explorer.exe", "/n,"+cleanPath).Start()
	case "darwin":
		return exec.Command("open", cleanPath).Start()
	default:
		return exec.Command("xdg-open", cleanPath).Start()
	}
}

