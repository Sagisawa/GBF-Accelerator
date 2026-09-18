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
