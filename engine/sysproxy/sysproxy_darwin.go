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

func parseServicesOrdered(output string) []string {
	var svcs []string
	lines := strings.Split(output, "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		// Format: "(1) Wi-Fi" or "(2) Ethernet" or "(3) *Bluetooth PAN"
		if strings.HasPrefix(l, "(") && strings.Contains(l, ") ") {
			parts := strings.SplitN(l, ") ", 2)
			if len(parts) == 2 {
				name := strings.TrimSpace(parts[1])
				// Skip disabled services prefixed with asterisk
				if strings.HasPrefix(name, "*") {
					continue
				}
				if name != "" {
					svcs = append(svcs, name)
				}
			}
		}
	}
	return svcs
}

func macGetServicesOrdered() []string {
	cmd := exec.Command("networksetup", "-listnetworkserviceorder")
	out, err := cmd.Output()
	if err == nil && len(out) > 0 {
		svcs := parseServicesOrdered(string(out))
		if len(svcs) > 0 {
			return svcs
		}
	}
	return macGetServices()
}

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
	svcs := macGetServicesOrdered()
	for _, s := range svcs {
		url, enabled := macGetAutoProxyInfo(s)
		if enabled && url != "" {
			return url
		}
	}
	return ""
}

func enablePACProxy(pacURL string) error {
	svcs := macGetServicesOrdered()
	if len(svcs) == 0 {
		return fmt.Errorf("no active network services found")
	}

	successCount := 0
	var lastErr error
	var lastErrOutput string

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

		cmdURL := exec.Command("networksetup", "-setautoproxyurl", s, pacURL)
		if out, err := cmdURL.CombinedOutput(); err != nil {
			lastErr = err
			lastErrOutput = strings.TrimSpace(string(out))
			continue
		}
		cmdState := exec.Command("networksetup", "-setautoproxystate", s, "on")
		if out, err := cmdState.CombinedOutput(); err != nil {
			lastErr = err
			lastErrOutput = strings.TrimSpace(string(out))
			continue
		}

		url, enabled := macGetAutoProxyInfo(s)
		if enabled && url != "" {
			successCount++
		}
	}

	if successCount == 0 {
		if lastErr != nil {
			return fmt.Errorf("failed to set system PAC proxy via networksetup: %w (output: %s)", lastErr, lastErrOutput)
		}
		return fmt.Errorf("failed to enable PAC proxy on any network service; administrator privileges may be required")
	}

	return nil
}

func disablePACProxy(force bool) error {
	svcs := macGetServicesOrdered()
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
				isOrigOur := strings.Contains(origLower, "/proxy.pac") &&
					(strings.Contains(origLower, "127.0.0.1") || strings.Contains(origLower, "localhost"))
				if !isOrigOur {
					conflicts = append(conflicts, fmt.Sprintf("外部 PAC 脚本 (%s)", orig.url))
					break
				}
			}
		}
	}

	svcs := macGetServicesOrdered()
	checked := make(map[string]bool)

	parseManualProxy := func(flag, label, svc string) {
		cmd := exec.Command("networksetup", flag, svc)
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
			conflictKey := fmt.Sprintf("%s:%s", label, target)
			if !checked[conflictKey] {
				checked[conflictKey] = true
				conflicts = append(conflicts, fmt.Sprintf("手动 %s [%s] (%s)", label, svc, target))
			}
		} else if enabled {
			conflictKey := fmt.Sprintf("%s:%s", label, svc)
			if !checked[conflictKey] {
				checked[conflictKey] = true
				conflicts = append(conflicts, fmt.Sprintf("手动 %s [%s]", label, svc))
			}
		}
	}

	// Check up to top 3 active services by priority order
	limit := 3
	if len(svcs) < limit {
		limit = len(svcs)
	}
	for i := 0; i < limit; i++ {
		s := svcs[i]
		parseManualProxy("-getwebproxy", "HTTP 代理", s)
		parseManualProxy("-getsecurewebproxy", "HTTPS 代理", s)
		parseManualProxy("-getsocksfirewallproxy", "SOCKS 代理", s)
	}

	return strings.Join(conflicts, " • ")
}
