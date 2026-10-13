//go:build windows

package desktop

import (
	"os"
	"testing"
)

// uiTestsEnvVar opts in to tests that drive the real Windows desktop.
const uiTestsEnvVar = "GBF_ACCELERATOR_UI_TESTS"

// requireUIEnvironment skips tests that touch the real Windows desktop UI.
//
// Those tests open Explorer windows, register a notification-area icon or display real Windows
// notifications. They are therefore visible to whoever is using the machine and can steal window
// focus, so they are opt-in. Set GBF_ACCELERATOR_UI_TESTS=1 to run them:
//
//	GBF_ACCELERATOR_UI_TESTS=1 go test ./desktop/
//
// Tests that only enumerate windows, probe filesystem paths or exercise pure logic are not
// gated here and keep running by default.
func requireUIEnvironment(t *testing.T) {
	t.Helper()
	if os.Getenv(uiTestsEnvVar) != "1" {
		t.Skipf("skipped: drives real desktop UI (Explorer windows / notification-area icon / Windows notifications); set %s=1 to run", uiTestsEnvVar)
	}
}
