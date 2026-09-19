//go:build darwin

package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// openAppWindow launches Chrome, Edge, or Brave in standalone App mode (--app=<url>) on macOS.
func openAppWindow(url string) error {
	appURL := url
	if !strings.Contains(appURL, "standalone=1") {
		if strings.Contains(appURL, "?") {
			appURL += "&standalone=1"
		} else {
			appURL += "?standalone=1"
		}
	}

	homeDir, _ := os.UserHomeDir()
	for _, appName := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser"} {
		appPath := fmt.Sprintf("/Applications/%s.app", appName)
		var exists bool
		if fi, err := os.Stat(appPath); err == nil && fi.IsDir() {
			exists = true
		} else if homeDir != "" {
			userAppPath := filepath.Join(homeDir, "Applications", fmt.Sprintf("%s.app", appName))
			if ufi, uerr := os.Stat(userAppPath); uerr == nil && ufi.IsDir() {
				exists = true
			}
		}

		if !exists {
			continue
		}

		cmd := exec.Command("open", "-na", appName, "--args", fmt.Sprintf("--app=%s", appURL), "--window-size=880,640")
		if err := cmd.Run(); err == nil {
			return nil
		}
	}

	return OpenBrowser(url)
}
