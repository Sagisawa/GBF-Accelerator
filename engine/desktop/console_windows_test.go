//go:build windows

package desktop

import (
	"testing"
)

func TestAttachConsole(t *testing.T) {
	// Calling AttachConsole should be safe and non-panicking in any environment
	AttachConsole()
}
