//go:build windows

package desktop

import (
	"os"
	"sync"
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
	userDataDir := `C:\Test\Profile`

	normal := buildAppWindowArgs(appURL, userDataDir, 1200, 800, false)
	if len(normal) != 8 || normal[0] != "--app="+appURL || normal[1] != "--user-data-dir="+userDataDir || normal[7] != "--window-size=1200,800" {
		t.Fatalf("unexpected normal app args: %#v", normal)
	}

	maximized := buildAppWindowArgs(appURL, userDataDir, 1200, 800, true)
	if len(maximized) != 8 || maximized[0] != "--app="+appURL || maximized[1] != "--user-data-dir="+userDataDir || maximized[7] != "--start-maximized" {
		t.Fatalf("unexpected maximized app args: %#v", maximized)
	}

	fallback := buildAppWindowArgs(appURL, userDataDir, 100, 200, false)
	if len(fallback) != 8 || fallback[7] != "--window-size=880,640" {
		t.Fatalf("unexpected fallback app args: %#v", fallback)
	}
}

func TestAppModeUserDataDir(t *testing.T) {
	dir, err := appModeUserDataDir()
	if err != nil {
		t.Fatalf("appModeUserDataDir returned error: %v", err)
	}
	if dir == "" {
		t.Fatalf("appModeUserDataDir returned empty string")
	}
}

func TestEnumWindowsCallbackReuseNoExhaustion(t *testing.T) {
	// Go runtime limits syscall.NewCallback on Windows to 2000 slots across process lifetime.
	// Invoking enumExplorerWindows and findAppWindowInTree >2,500 times concurrently must not
	// exhaust callback slots or race on shared enumeration state.
	tree := map[uint32]struct{}{uint32(os.Getpid()): {}}

	const workers = 4
	const iterationsPerWorker = 700 // 2,800 calls per function (5,600 total > 2,000 limit)

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterationsPerWorker; i++ {
				m := enumExplorerWindows()
				if m == nil {
					t.Errorf("enumExplorerWindows returned nil map")
					return
				}
				_ = findAppWindowInTree(tree)
			}
		}()
	}
	wg.Wait()

	if enumExplorerCB == 0 || findAppWinCB == 0 {
		t.Fatalf("expected singleton callbacks to be initialized, got enumExplorerCB=%d findAppWinCB=%d", enumExplorerCB, findAppWinCB)
	}
	if enumExplorerCurrent != nil || findAppWinTree != nil {
		t.Fatalf("expected enumeration state to be cleared after calls")
	}
}

