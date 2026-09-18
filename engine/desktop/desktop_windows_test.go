//go:build windows

package desktop

import (
	"os"
	"testing"
)

func TestFindAppBrowserWindows(t *testing.T) {
	p := findAppBrowserWindows()
	t.Logf("findAppBrowserWindows returned: %q", p)
	if p != "" {
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			t.Fatalf("Returned browser path %q is not a valid executable file: %v", p, err)
		}
	}
}

func TestPrepareAppURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://127.0.0.1:8125/", "http://127.0.0.1:8125/?standalone=1"},
		{"http://127.0.0.1:8125/?mode=test", "http://127.0.0.1:8125/?mode=test&standalone=1"},
		{"http://127.0.0.1:8125/?standalone=1", "http://127.0.0.1:8125/?standalone=1"},
		{"http://127.0.0.1:8125/?foo=bar&standalone=1", "http://127.0.0.1:8125/?foo=bar&standalone=1"},
	}

	for _, tc := range tests {
		actual := prepareAppURL(tc.input)
		if actual != tc.expected {
			t.Errorf("prepareAppURL(%q) = %q, expected %q", tc.input, actual, tc.expected)
		}
	}
}
