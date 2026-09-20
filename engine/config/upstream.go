package config

import (
	"fmt"
	"net"
	"runtime"
	"time"
)

type ProxyCandidate struct {
	URL  string `json:"url"`
	Name string `json:"name"`
}

var probePorts = []struct {
	port int
	name string
}{
	{7897, "Clash Verge (Mixed Port)"},
	{7891, "Clash Verge / Mihomo (Mixed Port)"},
	{7890, "Clash Default (HTTP)"},
	{10809, "v2rayN (HTTP)"},
	{10808, "v2rayN (SOCKS5)"},
	{8099, "岛风 GO (HTTP)"},
}

// IsPortOpen checks if a TCP port on host is open and reachable within timeout.
func IsPortOpen(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// DetectUpstreamProxies returns every reachable local proxy candidate as a list of (url, display name).
func DetectUpstreamProxies() []ProxyCandidate {
	var candidates []struct {
		port int
		name string
	}
	candidates = append(candidates, probePorts...)
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, struct {
			port int
			name string
		}{6152, "Surge (HTTP)"})
	}

	var detected []ProxyCandidate
	for _, cand := range candidates {
		if !IsPortOpen("127.0.0.1", cand.port, 300*time.Millisecond) {
			continue
		}

		scheme := "http"
		if cand.port != 8099 && cand.port != 7890 && cand.port != 7897 && cand.port != 7891 {
			// v2rayN commonly uses either 10808 or 10809 for HTTP/SOCKS5 depending
			// on configuration; probe the listener instead of hard-coding the scheme.
			if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", cand.port), 300*time.Millisecond); err == nil {
				_ = conn.SetDeadline(time.Now().Add(300 * time.Millisecond))
				_, _ = conn.Write([]byte{0x05, 0x01, 0x00})
				buf := make([]byte, 2)
				if n, err := conn.Read(buf); err == nil && n == 2 && buf[0] == 0x05 && buf[1] == 0x00 {
					scheme = "socks5"
				}
				_ = conn.Close()
			}
		}

		detected = append(detected, ProxyCandidate{
			URL:  fmt.Sprintf("%s://127.0.0.1:%d", scheme, cand.port),
			Name: cand.name,
		})
	}
	return detected
}

// AutoDetectUpstreamProxy probes common local proxy ports and returns the first active proxy URL,
// falling back to "http://127.0.0.1:7897" if none are reachable.
func AutoDetectUpstreamProxy() string {
	detected := DetectUpstreamProxies()
	if len(detected) > 0 {
		return detected[0].URL
	}
	return "http://127.0.0.1:7897"
}
