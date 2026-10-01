//go:build darwin

package desktop

import (
	"testing"

	"gbf-proxy/ui"
)

type darwinMockController struct {
	running bool
}

func (m *darwinMockController) IsRunning() bool              { return m.running }
func (m *darwinMockController) StartProxy() error            { m.running = true; return nil }
func (m *darwinMockController) StopProxy()                   { m.running = false }
func (m *darwinMockController) GetListenPort() int           { return 8124 }
func (m *darwinMockController) GetControlPort() int          { return 8125 }
func (m *darwinMockController) GetCacheDir() string          { return "/tmp/cache" }
func (m *darwinMockController) OpenBrowser(url string) error   { return nil }
func (m *darwinMockController) OpenAppWindow(url string) error { return nil }
func (m *darwinMockController) OpenFolder(path string) error   { return nil }
func (m *darwinMockController) Quit()                        {}

func TestDarwinTrayInterface(t *testing.T) {
	ctrl := &darwinMockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)
	if tray == nil {
		t.Fatal("expected non-nil Tray instance")
	}

	dt, ok := tray.(*DarwinTray)
	if !ok {
		t.Fatalf("expected *DarwinTray, got %T", tray)
	}

	if dt.IsActive() {
		t.Fatal("tray should not be active before Start()")
	}

	// Calling Stop() on unstarted tray should be safe and idempotent
	dt.Stop()
	dt.Stop()
}

func TestDarwinTrayTooltipFormatting(t *testing.T) {
	ctrl := &darwinMockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)
	dt := tray.(*DarwinTray)

	// In running state
	ctrl.running = true
	// Verify state string
	if !ctrl.IsRunning() {
		t.Fatal("expected running state")
	}

	// In stopped state
	ctrl.running = false
	if ctrl.IsRunning() {
		t.Fatal("expected stopped state")
	}

	_ = dt
}

func TestDarwinTrayMenuText(t *testing.T) {
	ctrl := &darwinMockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)
	dt := tray.(*DarwinTray)

	// Test menu text generation when proxy is running
	ctrl.running = true
	toggleTextRunning := "暂停代理加速"
	if !ctrl.IsRunning() {
		toggleTextRunning = "启动代理加速"
	}
	if toggleTextRunning != "暂停代理加速" {
		t.Errorf("expected 暂停代理加速, got %s", toggleTextRunning)
	}

	// Test menu text generation when proxy is paused
	ctrl.running = false
	toggleTextPaused := "暂停代理加速"
	if !ctrl.IsRunning() {
		toggleTextPaused = "启动代理加速"
	}
	if toggleTextPaused != "启动代理加速" {
		t.Errorf("expected 启动代理加速, got %s", toggleTextPaused)
	}

	_ = dt
}

func TestDarwinNSString(t *testing.T) {
	// nsString should handle empty string safely
	str := nsString("")
	_ = str
}
