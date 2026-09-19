//go:build darwin
// +build darwin

package sysproxy

import (
	"fmt"
	"os/exec"
	"strings"
)

var macOriginalSettings = make(map[string]struct {
	url     string
	enabled bool
})

func macGetServices() []string {
	cmd := exec.Command("networksetup", "-listallnetworkservices")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var svcs []string
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "An asterisk") || strings.HasPrefix(l, "*") {
			continue
		}
		svcs = append(svcs, l)
	}
	return svcs
}

func macGetAutoProxyInfo(service string) (string, bool) {
	cmd := exec.Command("networksetup", "-getautoproxyurl", service)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	var url string
	var enabled bool
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "URL:") {
			url = strings.TrimSpace(strings.TrimPrefix(l, "URL:"))
		} else if strings.HasPrefix(l, "Enabled:") {
			val := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "Enabled:")))
			enabled = (val == "yes")
		}
	}
	return url, enabled
}

func getCurrentPACURL() string {
	svcs := macGetServices()
	for _, s := range svcs {
		url, enabled := macGetAutoProxyInfo(s)
		if enabled && url != "" {
			return url
		}
	}
	return ""
}

func enablePACProxy(pacURL string) error {
	svcs := macGetServices()
	if len(svcs) == 0 {
		return fmt.Errorf("no active network services found")
	}

	for _, s := range svcs {
		currURL, currEn := macGetAutoProxyInfo(s)
		isOur := strings.Contains(strings.ToLower(currURL), "/proxy.pac") &&
			(strings.Contains(currURL, "127.0.0.1") || strings.Contains(currURL, "localhost"))

		if !isOur && (currURL != "" || currEn) {
			if _, exists := macOriginalSettings[s]; !exists {
				macOriginalSettings[s] = struct {
					url     string
					enabled bool
				}{url: currURL, enabled: currEn}
			}
		}

		_ = exec.Command("networksetup", "-setautoproxyurl", s, pacURL).Run()
		_ = exec.Command("networksetup", "-setautoproxystate", s, "on").Run()
	}
	return nil
}

func disablePACProxy(force bool) error {
	svcs := macGetServices()
	for _, s := range svcs {
		currURL, _ := macGetAutoProxyInfo(s)
		isOur := strings.Contains(strings.ToLower(currURL), "/proxy.pac") &&
			(strings.Contains(currURL, "127.0.0.1") || strings.Contains(currURL, "localhost"))

		if !force && !isManagingProxy && !isOur {
			continue
		}

		if orig, ok := macOriginalSettings[s]; ok {
			if orig.url != "" {
				_ = exec.Command("networksetup", "-setautoproxyurl", s, orig.url).Run()
			}
			state := "off"
			if orig.enabled {
				state = "on"
			}
			_ = exec.Command("networksetup", "-setautoproxystate", s, state).Run()
			delete(macOriginalSettings, s)
		} else {
			_ = exec.Command("networksetup", "-setautoproxystate", s, "off").Run()
		}
	}
	return nil
}

func checkProxyConflict(port int) string {
	var conflicts []string
	pacURL := getCurrentPACURL()
	isOurPAC := IsPACProxyEnabled(port)
	if pacURL != "" && !isOurPAC {
		conflicts = append(conflicts, fmt.Sprintf("外部 PAC 脚本 (%s)", pacURL))
	} else {
		for _, orig := range macOriginalSettings {
			if orig.enabled && orig.url != "" {
				origLower := strings.ToLower(orig.url)
				isOrigOur := strings.Contains(origLower, "/proxy.pac") && (strings.Contains(origLower, "127.0.0.1") || strings.Contains(origLower, "localhost"))
				if !isOrigOur {
					conflicts = append(conflicts, fmt.Sprintf("外部 PAC 脚本 (%s)", orig.url))
					break
				}
			}
		}
	}

	svcs := macGetServices()
	if len(svcs) > 0 {
		primary := svcs[0]
		parseManualProxy := func(flag, label string) {
			cmd := exec.Command("networksetup", flag, primary)
			out, err := cmd.Output()
			if err != nil {
				return
			}
			var enabled bool
			var server, proxyPort string
			for _, l := range strings.Split(string(out), "\n") {
				l = strings.TrimSpace(l)
				if strings.HasPrefix(l, "Enabled:") {
					enabled = (strings.ToLower(strings.TrimSpace(strings.TrimPrefix(l, "Enabled:"))) == "yes")
				} else if strings.HasPrefix(l, "Server:") {
					server = strings.TrimSpace(strings.TrimPrefix(l, "Server:"))
				} else if strings.HasPrefix(l, "Port:") {
					proxyPort = strings.TrimSpace(strings.TrimPrefix(l, "Port:"))
				}
			}
			if enabled && server != "" {
				target := server
				if proxyPort != "" && proxyPort != "0" {
					target = fmt.Sprintf("%s:%s", server, proxyPort)
				}
				conflicts = append(conflicts, fmt.Sprintf("手动 %s (%s)", label, target))
			} else if enabled {
				conflicts = append(conflicts, fmt.Sprintf("手动 %s", label))
			}
		}

		parseManualProxy("-getwebproxy", "HTTP 代理")
		parseManualProxy("-getsecurewebproxy", "HTTPS 代理")
		parseManualProxy("-getsocksfirewallproxy", "SOCKS 代理")
	}

	return strings.Join(conflicts, " • ")
}
