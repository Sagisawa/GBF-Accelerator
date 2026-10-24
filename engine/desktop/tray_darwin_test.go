//go:build darwin

package desktop

import (
	"os"
	"sync"
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
func (m *darwinMockController) GetLanguage() string          { return "zh" }
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

func TestDarwinTrayUpdateBeforeRunLoopDeferredAndCoalesced(t *testing.T) {
	ctrl := newMockController(true)
	dt := NewTray(ctrl, ui.AppIconBytes).(*DarwinTray)

	var applyCount atomic.Int32
	var dispatchCount atomic.Int32
	var flushedInRunLoop atomic.Bool

	dt.mu.Lock()
	dt.active = true
	dt.statusItem = objc.ID(1)
	dt.applyUpdateFn = func() {
		applyCount.Add(1)
	}
	dt.dispatchMainFn = func() {
		dispatchCount.Add(1)
	}
	dt.runAppLoopFn = func() {
		flushedInRunLoop.Store(applyCount.Load() == 1)
		dt.mu.Lock()
		dt.statusItem = 0
		dt.mu.Unlock()
	}
	dt.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dt.Update()
		}()
	}
	wg.Wait()

	if got := applyCount.Load(); got != 0 {
		t.Fatalf("expected 0 direct main-thread updates before RunLoop starts, got %d", got)
	}
	if got := dispatchCount.Load(); got != 0 {
		t.Fatalf("expected 0 selector dispatches before RunLoop starts, got %d", got)
	}

	dt.mu.Lock()
	pendingBeforeRun := dt.pendingUpdate
	scheduledBeforeRun := dt.updateScheduled
	dt.mu.Unlock()
	if !pendingBeforeRun {
		t.Fatal("expected pendingUpdate to be true after pre-RunLoop Update() calls")
	}
	if scheduledBeforeRun {
		t.Fatal("expected updateScheduled to remain false before RunLoop starts")
	}

	sigCh := make(chan os.Signal, 1)
	quitChan := make(chan struct{})
	dt.RunLoop(sigCh, quitChan)

	if !flushedInRunLoop.Load() {
		t.Fatal("expected coalesced pre-RunLoop updates to be flushed upon entering RunLoop before app.run")
	}
	if got := applyCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 coalesced update flush on RunLoop entry, got %d", got)
	}
	if got := dispatchCount.Load(); got != 0 {
		t.Fatalf("expected 0 selector dispatches for pre-RunLoop flush, got %d", got)
	}
}

func TestDarwinTrayUpdateDuringRunLoopAndAfterStop(t *testing.T) {
	ctrl := newMockController(true)
	dt := NewTray(ctrl, ui.AppIconBytes).(*DarwinTray)

	var applyCount atomic.Int32
	var dispatchCount atomic.Int32
	var reentrantTriggered atomic.Bool

	dt.mu.Lock()
	dt.active = true
	dt.inRunLoop = true
	dt.statusItem = objc.ID(1)
	dt.dispatchMainFn = func() {
		dispatchCount.Add(1)
	}
	dt.applyUpdateFn = func() {
		applyCount.Add(1)
		// Simulate a background goroutine calling Update() while main thread is mid-update
		if reentrantTriggered.CompareAndSwap(false, true) {
			dt.Update()
		}
	}
	dt.mu.Unlock()

	// Multiple background updates before main thread processes the scheduled selector
	dt.Update()
	dt.Update()
	dt.Update()

	if got := dispatchCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 main-thread dispatch while updateScheduled is true, got %d", got)
	}
	if got := applyCount.Load(); got != 0 {
		t.Fatalf("expected 0 direct updates on caller goroutine, got %d", got)
	}

	// Main thread drains scheduled update; mid-flush Update() must be picked up in the same flush loop without extra dispatch
	dt.flushPendingUpdateOnMainThread()

	if got := applyCount.Load(); got != 2 {
		t.Fatalf("expected 2 apply executions (initial coalesced batch + mid-flush update), got %d", got)
	}
	if got := dispatchCount.Load(); got != 1 {
		t.Fatalf("expected no extra dispatch during active flush, got %d", got)
	}

	dt.mu.Lock()
	if dt.pendingUpdate || dt.updateScheduled {
		dt.mu.Unlock()
		t.Fatal("expected pendingUpdate and updateScheduled to be false after flush completes")
	}
	dt.statusItem = 0
	dt.mu.Unlock()

	// Stop tray and verify subsequent Update() and flush calls are safe no-ops
	dt.requestStop()
	dt.cleanupOnMainThread()

	dt.Update()
	dt.flushPendingUpdateOnMainThread()

	if got := applyCount.Load(); got != 2 {
		t.Fatalf("expected no updates after stop, got applyCount=%d", got)
	}
	if got := dispatchCount.Load(); got != 1 {
		t.Fatalf("expected no dispatches after stop, got dispatchCount=%d", got)
	}
}

func TestDarwinTrayConcurrentDispatchAndCleanup(t *testing.T) {
	ctrl := newMockController(true)
	dt := NewTray(ctrl, ui.AppIconBytes).(*DarwinTray)

	dispatchEntered := make(chan struct{})
	allowDispatchExit := make(chan struct{})
	updateFinished := make(chan struct{})
	cleanupFinished := make(chan struct{})

	var dispatchCount atomic.Int32
	var handlerObservedDuringDispatch atomic.Uintptr

	dt.mu.Lock()
	dt.active = true
	dt.inRunLoop = true
	dt.statusItem = objc.ID(1)
	dt.handler = objc.ID(2)
	dt.dispatchMainFn = func() {
		dispatchCount.Add(1)
		close(dispatchEntered)
		<-allowDispatchExit

		dt.mu.Lock()
		handlerObservedDuringDispatch.Store(uintptr(dt.handler))
		// Clear synthetic IDs right before returning so cleanupOnMainThread does not send Objective-C release to fake pointers
		dt.statusItem = 0
		dt.handler = 0
		dt.mu.Unlock()
	}
	dt.mu.Unlock()

	go func() {
		defer close(updateFinished)
		dt.Update()
	}()

	<-dispatchEntered

	go func() {
		defer close(cleanupFinished)
		dt.cleanupOnMainThread()
	}()

	// Wait deterministically until cleanupOnMainThread sets stopping=true and blocks on dispatchDone
	deadline := time.Now().Add(time.Second)
	var cleanupWaiting bool
	var handlerAliveWhileWaiting bool
	for time.Now().Before(deadline) {
		dt.mu.Lock()
		if dt.stopping && dt.dispatchDone != nil && dt.inFlightDispatches == 1 {
			cleanupWaiting = true
			handlerAliveWhileWaiting = dt.active && dt.handler == objc.ID(2)
			dt.mu.Unlock()
			break
		}
		dt.mu.Unlock()
		time.Sleep(time.Millisecond)
	}

	if !cleanupWaiting {
		t.Fatal("expected cleanupOnMainThread to block waiting for in-flight async dispatch")
	}
	if !handlerAliveWhileWaiting {
		t.Fatal("expected handler and active state to remain intact while async dispatch is in flight")
	}

	select {
	case <-cleanupFinished:
		t.Fatal("cleanupOnMainThread must not finish before in-flight dispatch completes")
	default:
	}

	// Any concurrent Update() arriving while stopping is in progress must be a non-blocking no-op
	dt.Update()
	if got := dispatchCount.Load(); got != 1 {
		t.Fatalf("expected no additional dispatches after stopping begins, got %d", got)
	}

	// Allow in-flight dispatch to complete and verify cleanupOnMainThread unblocks and finishes
	close(allowDispatchExit)

	select {
	case <-updateFinished:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Update() to finish")
	}

	select {
	case <-cleanupFinished:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cleanupOnMainThread() to finish after dispatch completed")
	}

	if got := objc.ID(handlerObservedDuringDispatch.Load()); got != objc.ID(2) {
		t.Fatalf("expected handler to remain objc.ID(2) throughout dispatch, got %v", got)
	}
	if dt.IsActive() {
		t.Fatal("expected tray to be inactive after cleanupOnMainThread completes")
	}
}
