//go:build !windows

package desktop

// AttachConsole is a no-op on non-Windows platforms.
func AttachConsole() {}
