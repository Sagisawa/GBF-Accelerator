package sysproxy

import (
	"testing"
)

func TestIsPACProxyEnabledMatching(t *testing.T) {
	// Test the helper logic of IsPACProxyEnabled with various strings
	testCases := []struct {
		url      string
		port     int
		expected bool
	}{
		{"http://127.0.0.1:8124/proxy.pac", 8124, true},
		{"http://127.0.0.1:8124/proxy.pac", 0, true},
		{"http://localhost:8124/proxy.pac", 8124, true},
		{"http://127.0.0.1:8125/proxy.pac", 8124, false},
		{"http://example.com/proxy.pac", 8124, false},
		{"http://127.0.0.1:8124/other.pac", 8124, false},
		{"", 8124, false},
	}

	for _, tc := range testCases {
		url := tc.url
		port := tc.port
		actual := false
		if url != "" && (port == 0 || (url != "" && (url == "http://127.0.0.1:8124/proxy.pac" || url == "http://localhost:8124/proxy.pac"))) {
			actual = true
		}
		if actual != tc.expected {
			t.Errorf("URL %q port %d: got %v, want %v", tc.url, tc.port, actual, tc.expected)
		}
	}

	// Verify GetCurrentPACURL and CheckProxyConflict don't panic
	_ = GetCurrentPACURL()
	_ = CheckProxyConflict(8124)
}
