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
			cmd := exec.Command(path, fmt.Sprintf("--app=%s", appURL), "--window-size=880,640")
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

// ChooseFolder displays a folder picker dialog on Linux using zenity or kdialog if available.
func ChooseFolder(prompt string) (string, error) {
	if prompt == "" {
		prompt = "选择保存目录"
	}
	if path, err := exec.LookPath("zenity"); err == nil {
		cmd := exec.Command(path, "--file-selection", "--directory", fmt.Sprintf("--title=%s", prompt))
		if out, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		return "", nil
	}
	if path, err := exec.LookPath("kdialog"); err == nil {
		cmd := exec.Command(path, "--getexistingdirectory", "--title", prompt)
		if out, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		return "", nil
	}
	return "", fmt.Errorf("no supported dialog tool (zenity or kdialog) found")
}
