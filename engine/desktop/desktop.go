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
	OpenFolder(path string) error
	Quit()
}

// Tray defines the system tray lifecycle interface.
type Tray interface {
	Start() error
	Update()
	Stop()
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
		return exec.Command("explorer", cleanPath).Start()
	case "darwin":
		return exec.Command("open", cleanPath).Start()
	default:
		return exec.Command("xdg-open", cleanPath).Start()
	}
}

