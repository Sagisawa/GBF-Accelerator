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
	return OpenAppWindowWithGeometry(url, 880, 640, false)
}

// OpenAppWindowWithGeometry opens a standalone application window with the requested size/state.
// Platform implementations may ignore geometry where the desktop backend does not expose it.
func OpenAppWindowWithGeometry(url string, width, height int, maximized bool) error {
	return openAppWindowWithGeometry(url, width, height, maximized)
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
	return openFolderNative(cleanPath)
}

// ShowInFolder opens the system file explorer revealing the target file or opening its parent directory.
func ShowInFolder(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("empty path")
	}
	cleanPath := filepath.Clean(path)
	return showInFolderNative(cleanPath)
}


