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

// ChooseFolder displays a native folder picker dialog on macOS using osascript.
func ChooseFolder(prompt string) (string, error) {
	if prompt == "" {
		prompt = "选择保存目录"
	}
	escapedPrompt := strings.ReplaceAll(prompt, "\"", "\\\"")
	script := fmt.Sprintf(`POSIX path of (choose folder with prompt "%s")`, escapedPrompt)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		// User cancellation in AppleScript returns non-zero exit code
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// ShowNotification displays a native macOS user notification banner using osascript.
func ShowNotification(title, message string) {
	escapedTitle := strings.ReplaceAll(title, `"`, `\"`)
	escapedMsg := strings.ReplaceAll(message, `"`, `\"`)
	script := fmt.Sprintf(`display notification "%s" with title "%s"`, escapedMsg, escapedTitle)
	_ = exec.Command("osascript", "-e", script).Start()
}
