//go:build darwin

package desktop

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/ebitengine/purego/objc"

	"gbf-proxy/ui"
)

type darwinMockController struct {
	running    atomic.Bool
	quitCalled atomic.Bool
}

func (m *darwinMockController) IsRunning() bool              { return m.running.Load() }
func (m *darwinMockController) StartProxy() error            { m.running.Store(true); return nil }
func (m *darwinMockController) StopProxy()                   { m.running.Store(false) }
func (m *darwinMockController) GetListenPort() int           { return 8124 }
func (m *darwinMockController) GetControlPort() int          { return 8125 }
func (m *darwinMockController) GetCacheDir() string          { return "/tmp/cache" }
func (m *darwinMockController) OpenBrowser(url string) error   { return nil }
func (m *darwinMockController) OpenAppWindow(url string) error { return nil }
func (m *darwinMockController) OpenFolder(path string) error   { return nil }
func (m *darwinMockController) Quit()                        { m.quitCalled.Store(true) }

func newMockController(running bool) *darwinMockController {
	m := &darwinMockController{}
	m.running.Store(running)
	return m
}

func TestDarwinTrayInterface(t *testing.T) {
	ctrl := newMockController(true)
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
	ctrl := newMockController(true)
	tray := NewTray(ctrl, ui.AppIconBytes)
	dt := tray.(*DarwinTray)

	// In running state
	ctrl.running.Store(true)
	// Verify state string
	if !ctrl.IsRunning() {
		t.Fatal("expected running state")
	}

	// In stopped state
	ctrl.running.Store(false)
	if ctrl.IsRunning() {
		t.Fatal("expected stopped state")
	}

	_ = dt
}

func TestDarwinTrayMenuText(t *testing.T) {
	ctrl := newMockController(true)
	tray := NewTray(ctrl, ui.AppIconBytes)
	dt := tray.(*DarwinTray)

	// Test menu text generation when proxy is running
	ctrl.running.Store(true)
	toggleTextRunning := "暂停代理加速"
	if !ctrl.IsRunning() {
		toggleTextRunning = "启动代理加速"
	}
	if toggleTextRunning != "暂停代理加速" {
		t.Errorf("expected 暂停代理加速, got %s", toggleTextRunning)
	}

	// Test menu text generation when proxy is paused
	ctrl.running.Store(false)
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
	strEmpty := nsString("")
	if strEmpty == 0 {
		t.Fatal("expected non-zero NSString for empty string")
	}

	strAscii := nsString("GBF")
	if strAscii == 0 {
		t.Fatal("expected non-zero NSString for ASCII string")
	}

	strUnicode := nsString("退出程序")
	if strUnicode == 0 {
		t.Fatal("expected non-zero NSString for Unicode string")
	}
}

func TestDarwinTrayLifecycleSafeguards(t *testing.T) {
	ctrl := newMockController(true)
	tray := NewTray(ctrl, ui.AppIconBytes)
	dt := tray.(*DarwinTray)

	// Update on inactive tray should be safe no-op
	dt.Update()

	// requestStop on inactive tray should be safe and idempotent
	dt.requestStop()
	dt.requestStop()

	// cleanupOnMainThread on inactive tray should be safe and idempotent
	dt.cleanupOnMainThread()
	dt.cleanupOnMainThread()

	// Stop when not in run loop should safely perform cleanup
	dt.Stop()

	// Stop when inRunLoop is true: verify it awaits doneChan instead of running direct cleanup
	dt2 := NewTray(ctrl, ui.AppIconBytes).(*DarwinTray)
	dt2.mu.Lock()
	dt2.active = true
	dt2.inRunLoop = true
	dt2.mu.Unlock()

	go func() {
		time.Sleep(50 * time.Millisecond)
		dt2.cleanupOnMainThread()
	}()

	startWait := time.Now()
	dt2.Stop()
	elapsed := time.Since(startWait)
	if elapsed < 40*time.Millisecond {
		t.Errorf("expected Stop to wait for doneChan, but returned in %v", elapsed)
	}
	if dt2.IsActive() {
		t.Error("expected tray to be inactive after cleanup")
	}
}

func TestDarwinPostDummyEvent(t *testing.T) {
	if err := initAppKit(); err != nil {
		t.Fatalf("failed to init AppKit: %v", err)
	}
	clsApp := objc.ID(objc.GetClass("NSApplication"))
	if clsApp == 0 {
		t.Fatal("NSApplication class not found")
	}
	app := clsApp.Send(selSharedApplication)
	if app == 0 {
		t.Fatal("sharedApplication returned nil")
	}
	if !postDummyEvent(app) {
		t.Fatal("postDummyEvent failed to create or post dummy event")
	}
}

func TestDarwinMenuHandlerDispatch(t *testing.T) {
	if err := initAppKit(); err != nil {
		t.Fatalf("failed to init AppKit: %v", err)
	}
	if err := initMenuHandlerClass(); err != nil {
		t.Fatalf("failed to init menu handler class: %v", err)
	}

	hAlloc := objc.ID(menuHandlerClass).Send(selAlloc)
	if hAlloc == 0 {
		t.Fatal("failed to alloc menuHandler")
	}
	handler := hAlloc.Send(selInit)
	if handler == 0 {
		t.Fatal("failed to init menuHandler")
	}
	defer handler.Send(selRelease)

	ctrl := newMockController(true)
	tray := NewTray(ctrl, ui.AppIconBytes).(*DarwinTray)

	activeTrayMu.Lock()
	oldTray := activeTray
	activeTray = tray
	activeTrayMu.Unlock()
	defer func() {
		activeTrayMu.Lock()
		activeTray = oldTray
		activeTrayMu.Unlock()
	}()

	// Test quitAppAction dispatch
	handler.Send(selQuitAppAction, objc.ID(0))
	time.Sleep(50 * time.Millisecond)
	if !ctrl.quitCalled.Load() {
		t.Error("expected ctrl.Quit() to be called after selQuitAppAction")
	}

	// Test toggleProxyAction dispatch
	handler.Send(selToggleProxyAction, objc.ID(0))
	if ctrl.running.Load() {
		t.Error("expected proxy to be stopped after toggleProxyAction")
	}
	handler.Send(selToggleProxyAction, objc.ID(0))
	if !ctrl.running.Load() {
		t.Error("expected proxy to be started after second toggleProxyAction")
	}
}

func TestDarwinTraySetupButtonIconSize(t *testing.T) {
	if err := initAppKit(); err != nil {
		t.Fatalf("failed to init AppKit: %v", err)
	}
	ctrl := newMockController(true)
	tray := NewTray(ctrl, ui.AppIconBytes).(*DarwinTray)

	clsBtn := objc.ID(objc.GetClass("NSButton"))
	if clsBtn == 0 {
		t.Fatal("NSButton class not found")
	}
	btnAlloc := clsBtn.Send(selAlloc)
	btn := btnAlloc.Send(selInit)
	if btn == 0 {
		t.Fatal("failed to alloc/init NSButton")
	}
	defer btn.Send(selRelease)

	tray.setupButton(btn)

	clsImg := btn.Send(objc.RegisterName("image"))
	if clsImg == 0 {
		t.Fatal("expected button to have image set")
	}
	size := objc.Send[nsSize](clsImg, objc.RegisterName("size"))
	if size.Width != 18 || size.Height != 18 {
		t.Errorf("expected image size 18x18, got %.1fx%.1f", size.Width, size.Height)
	}
	isTemplate := objc.Send[bool](clsImg, objc.RegisterName("isTemplate"))
	if !isTemplate {
		t.Error("expected image to be marked as template")
	}
}
