package startup

import (
	"runtime"
	"testing"
)

func TestStartupSupported(t *testing.T) {
	supported := IsStartupSupported()
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if !supported {
			t.Errorf("expected IsStartupSupported() to be true on %s", runtime.GOOS)
		}
	} else {
		if supported {
			t.Errorf("expected IsStartupSupported() to be false on %s", runtime.GOOS)
		}
	}

	// Verify IsStartupEnabled does not panic
	_ = IsStartupEnabled()
}
