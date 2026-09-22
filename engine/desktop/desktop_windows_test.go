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

func TestOpenFolder(t *testing.T) {
	err := OpenFolder(t.TempDir())
	if err != nil {
		t.Errorf("OpenFolder returned error: %v", err)
	}
}

func TestBuildAppWindowArgs(t *testing.T) {
	appURL := "http://127.0.0.1:8125/?standalone=1"

	normal := buildAppWindowArgs(appURL, 1200, 800, false)
	if len(normal) != 2 || normal[0] != "--app="+appURL || normal[1] != "--window-size=1200,800" {
		t.Fatalf("unexpected normal app args: %#v", normal)
	}

	maximized := buildAppWindowArgs(appURL, 1200, 800, true)
	if len(maximized) != 2 || maximized[0] != "--app="+appURL || maximized[1] != "--start-maximized" {
		t.Fatalf("unexpected maximized app args: %#v", maximized)
	}

	fallback := buildAppWindowArgs(appURL, 100, 200, false)
	if len(fallback) != 2 || fallback[1] != "--window-size=880,640" {
		t.Fatalf("unexpected fallback app args: %#v", fallback)
	}
}
