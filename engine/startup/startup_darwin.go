//go:build darwin
// +build darwin

package startup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const macLabel = "com.sagisawa.gbf_accelerator"

func getPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", macLabel+".plist"), nil
}

func getMacStartupArgs() []string {
	exe, err := os.Executable()
	if err != nil {
		return []string{exe, "--minimized"}
	}
	exe, _ = filepath.EvalSymlinks(exe)

	// Check if running inside .app bundle
	parts := strings.Split(filepath.ToSlash(exe), "/")
	for i, part := range parts {
		if strings.HasSuffix(strings.ToLower(part), ".app") {
			appPath := "/" + strings.Join(parts[1:i+1], "/")
			return []string{"/usr/bin/open", "-a", appPath, "--args", "--minimized"}
		}
	}

	return []string{exe, "--minimized"}
}

func isStartupEnabled() bool {
	plistPath, err := getPlistPath()
	if err != nil {
		return false
	}
	fi, err := os.Stat(plistPath)
	return err == nil && !fi.IsDir()
}

func setStartupEnabled(enabled bool) error {
	plistPath, err := getPlistPath()
	if err != nil {
		return err
	}

	if enabled {
		_ = os.MkdirAll(filepath.Dir(plistPath), 0755)
		args := getMacStartupArgs()
		var argsXML strings.Builder
		for _, a := range args {
			argsXML.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", a))
		}

		plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
</dict>
</plist>
`, macLabel, argsXML.String())

		if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
			return err
		}
		// NOTE: Do NOT call launchctl load to prevent duplicate running process termination
		return nil
	}

	// Removing the plist disables future login launches. Do not treat an
	// unload failure as fatal because the job may not be loaded in the current
	// session (the app intentionally does not load it on enable).
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove macOS startup plist: %w", err)
	}
	return nil
}
