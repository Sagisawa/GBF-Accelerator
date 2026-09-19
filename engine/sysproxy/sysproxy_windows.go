//go:build windows
// +build windows

package sysproxy

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

const (
	internetOptionSettingsChanged = 39
	internetOptionRefresh         = 37
	internetSettingsRegKey        = `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`
)

var (
	wininetDLL             = syscall.NewLazyDLL("wininet.dll")
	internetSetOptionWProc = wininetDLL.NewProc("InternetSetOptionW")
)

func notifyWinINet() {
	_, _, _ = internetSetOptionWProc.Call(0, uintptr(internetOptionSettingsChanged), 0, 0)
	_, _, _ = internetSetOptionWProc.Call(0, uintptr(internetOptionRefresh), 0, 0)
}

func getCurrentPACURL() string {
	cmd := exec.Command("reg", "query", internetSettingsRegKey, "/v", "AutoConfigURL")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "AutoConfigURL") {
			parts := strings.SplitN(l, "REG_SZ", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func enablePACProxy(pacURL string) error {
	current := getCurrentPACURL()
	isOur := strings.Contains(strings.ToLower(current), "/proxy.pac") &&
		(strings.Contains(current, "127.0.0.1") || strings.Contains(current, "localhost"))

	if current != "" && !isOur && originalPACURL == "" {
		originalPACURL = current
	}

	cmd := exec.Command("reg", "add", internetSettingsRegKey, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", pacURL, "/f")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to set AutoConfigURL: %w", err)
	}

	notifyWinINet()
	return nil
}

func disablePACProxy(force bool) error {
	if originalPACURL != "" {
		cmd := exec.Command("reg", "add", internetSettingsRegKey, "/v", "AutoConfigURL", "/t", "REG_SZ", "/d", originalPACURL, "/f")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Run()
		originalPACURL = ""
	} else {
		cmd := exec.Command("reg", "delete", internetSettingsRegKey, "/v", "AutoConfigURL", "/f")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Run()
	}

	notifyWinINet()
	return nil
}

func checkProxyConflict(port int) string {
	var conflicts []string

	pacURL := getCurrentPACURL()
	isOurPAC := IsPACProxyEnabled(port)
	if pacURL != "" && !isOurPAC {
		conflicts = append(conflicts, fmt.Sprintf("外部 PAC 脚本 (%s)", pacURL))
	} else if originalPACURL != "" {
		origLower := strings.ToLower(originalPACURL)
		isOrigOur := strings.Contains(origLower, "/proxy.pac") && (strings.Contains(origLower, "127.0.0.1") || strings.Contains(origLower, "localhost"))
		if !isOrigOur {
			conflicts = append(conflicts, fmt.Sprintf("外部 PAC 脚本 (%s)", originalPACURL))
		}
	}

	// Check manual system proxy (ProxyEnable)
	cmd := exec.Command("reg", "query", internetSettingsRegKey, "/v", "ProxyEnable")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.Output(); err == nil {
		strOut := string(out)
		if strings.Contains(strOut, "0x1") || strings.Contains(strOut, "1") {
			srvCmd := exec.Command("reg", "query", internetSettingsRegKey, "/v", "ProxyServer")
			srvCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if srvOut, err := srvCmd.Output(); err == nil {
				lines := strings.Split(string(srvOut), "\n")
				var proxySrv string
				for _, l := range lines {
					l = strings.TrimSpace(l)
					if strings.HasPrefix(l, "ProxyServer") {
						parts := strings.SplitN(l, "REG_SZ", 2)
						if len(parts) == 2 {
							proxySrv = strings.TrimSpace(parts[1])
						}
					}
				}
				if proxySrv != "" {
					conflicts = append(conflicts, fmt.Sprintf("手动系统代理 (%s)", proxySrv))
				} else {
					conflicts = append(conflicts, "手动系统代理")
				}
			} else {
				conflicts = append(conflicts, "手动系统代理")
			}
		}
	}

	return strings.Join(conflicts, " • ")
}
