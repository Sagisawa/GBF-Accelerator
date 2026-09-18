//go:build !windows && !darwin

package desktop

import (
	"fmt"
	"os/exec"
	"strings"
)

// openAppWindow launches Chrome or Edge in standalone App mode (--app=<url>) on Linux.
func openAppWindow(url string) error {
	appURL := url
	if !strings.Contains(appURL, "standalone=1") {
		if strings.Contains(appURL, "?") {
			appURL += "&standalone=1"
		} else {
			appURL += "?standalone=1"
		}
	}

	for _, bin := range []string{"google-chrome", "chromium", "microsoft-edge", "brave-browser"} {
		if path, err := exec.LookPath(bin); err == nil {
			cmd := exec.Command(path, fmt.Sprintf("--app=%s", appURL), "--window-size=820,960")
			if err := cmd.Start(); err == nil {
				go func() {
					_ = cmd.Wait()
				}()
				return nil
			}
		}
	}

	return OpenBrowser(url)
}
