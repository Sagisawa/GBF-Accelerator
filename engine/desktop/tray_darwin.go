//go:build darwin

package desktop

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

type nsPoint struct {
	X float64
	Y float64
}

var (
	appKitOnce   sync.Once
	appKitErr    error
	cfHandle     uintptr
	appKitHandle uintptr

	cfRunLoopStop    func(rl uintptr)
	cfRunLoopWakeUp  func(rl uintptr)
	cfRunLoopGetMain func() uintptr

	selAlloc                      objc.SEL
	selInit                       objc.SEL
	selRetain                     objc.SEL
	selRelease                    objc.SEL
	selSharedApplication         objc.SEL
	selSetActivationPolicy        objc.SEL
	selSystemStatusBar            objc.SEL
	selStatusItemWithLength       objc.SEL
	selRemoveStatusItem           objc.SEL
	selButton                     objc.SEL
	selSetTitle                   objc.SEL
	selSetImage                   objc.SEL
	selSetImageScaling            objc.SEL
	selSetToolTip                 objc.SEL
	selSetMenu                    objc.SEL
	selInitWithTitle              objc.SEL
	selSetAutoenablesItems        objc.SEL
	selInitWithTitleActionKeyEquiv objc.SEL
	selSetTarget                  objc.SEL
	selSetAction                  objc.SEL
	selSetEnabled                 objc.SEL
	selAddItem                    objc.SEL
	selSeparatorItem              objc.SEL
	selStringWithUTF8String       objc.SEL
	selDataWithBytesLength        objc.SEL
	selInitWithData               objc.SEL
	selRun                        objc.SEL
	selStop                       objc.SEL
	selPostEventAtStart           objc.SEL
	selOtherEventWithType         objc.SEL

	selOpenConsoleAction objc.SEL
	selToggleProxyAction objc.SEL
	selOpenCacheAction   objc.SEL
	selQuitAppAction     objc.SEL

	menuHandlerClass objc.Class
	menuHandlerOnce  sync.Once

	activeTray   *DarwinTray
	activeTrayMu sync.Mutex
)

func initAppKit() error {
	appKitOnce.Do(func() {
		h, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_GLOBAL)
		if err != nil {
			appKitErr = fmt.Errorf("failed to load AppKit framework: %w", err)
			return
		}
		appKitHandle = h

		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL)
		if err == nil {
			cfHandle = cf
			purego.RegisterLibFunc(&cfRunLoopGetMain, cf, "CFRunLoopGetMain")
			purego.RegisterLibFunc(&cfRunLoopStop, cf, "CFRunLoopStop")
			purego.RegisterLibFunc(&cfRunLoopWakeUp, cf, "CFRunLoopWakeUp")
		}

		initSelectors()
	})
	return appKitErr
}

func initSelectors() {
	selAlloc = objc.RegisterName("alloc")
	selInit = objc.RegisterName("init")
	selRetain = objc.RegisterName("retain")
	selRelease = objc.RegisterName("release")
	selSharedApplication = objc.RegisterName("sharedApplication")
	selSetActivationPolicy = objc.RegisterName("setActivationPolicy:")
	selSystemStatusBar = objc.RegisterName("systemStatusBar")
	selStatusItemWithLength = objc.RegisterName("statusItemWithLength:")
	selRemoveStatusItem = objc.RegisterName("removeStatusItem:")
	selButton = objc.RegisterName("button")
	selSetTitle = objc.RegisterName("setTitle:")
	selSetImage = objc.RegisterName("setImage:")
	selSetImageScaling = objc.RegisterName("setImageScaling:")
	selSetToolTip = objc.RegisterName("setToolTip:")
	selSetMenu = objc.RegisterName("setMenu:")
	selInitWithTitle = objc.RegisterName("initWithTitle:")
	selSetAutoenablesItems = objc.RegisterName("setAutoenablesItems:")
	selInitWithTitleActionKeyEquiv = objc.RegisterName("initWithTitle:action:keyEquivalent:")
	selSetTarget = objc.RegisterName("setTarget:")
	selSetAction = objc.RegisterName("setAction:")
	selSetEnabled = objc.RegisterName("setEnabled:")
	selAddItem = objc.RegisterName("addItem:")
	selSeparatorItem = objc.RegisterName("separatorItem")
	selStringWithUTF8String = objc.RegisterName("stringWithUTF8String:")
	selDataWithBytesLength = objc.RegisterName("dataWithBytes:length:")
	selInitWithData = objc.RegisterName("initWithData:")
	selRun = objc.RegisterName("run")
	selStop = objc.RegisterName("stop:")
	selPostEventAtStart = objc.RegisterName("postEvent:atStart:")
	selOtherEventWithType = objc.RegisterName("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:")

	selOpenConsoleAction = objc.RegisterName("openConsoleAction:")
	selToggleProxyAction = objc.RegisterName("toggleProxyAction:")
	selOpenCacheAction = objc.RegisterName("openCacheAction:")
	selQuitAppAction = objc.RegisterName("quitAppAction:")
}

func initMenuHandlerClass() {
	menuHandlerOnce.Do(func() {
		existing := objc.GetClass("GBFAcceleratorMenuHandler")
		if existing != 0 {
			menuHandlerClass = existing
			return
		}

		methods := []objc.MethodDef{
			{
				Cmd: selOpenConsoleAction,
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
					handleOpenConsole()
				},
			},
			{
				Cmd: selToggleProxyAction,
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
					handleToggleProxy()
				},
			},
			{
				Cmd: selOpenCacheAction,
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
					handleOpenCache()
				},
			},
			{
				Cmd: selQuitAppAction,
				Fn: func(self objc.ID, _cmd objc.SEL, sender objc.ID) {
					handleQuitApp()
				},
			},
		}

		cls, err := objc.RegisterClass(
			"GBFAcceleratorMenuHandler",
			objc.GetClass("NSObject"),
			nil,
			nil,
			methods,
		)
		if err != nil {
			fmt.Printf("[*] Failed to register GBFAcceleratorMenuHandler: %v\n", err)
			return
		}
		menuHandlerClass = cls
	})
}

// DarwinTray provides a native macOS Menu Bar status item with context menu.
type DarwinTray struct {
	ctrl       Controller
	iconBytes  []byte
	app        objc.ID
	statusItem objc.ID
	menu       objc.ID
	handler    objc.ID
	active     bool
	mu         sync.Mutex
	stopOnce   sync.Once
	doneChan   chan struct{}
}

func NewTray(ctrl Controller, iconBytes []byte) Tray {
	return &DarwinTray{
		ctrl:      ctrl,
		iconBytes: iconBytes,
		doneChan:  make(chan struct{}),
	}
}

func (t *DarwinTray) IsActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

func (t *DarwinTray) Start() error {
	if err := initAppKit(); err != nil {
		return err
	}

	activeTrayMu.Lock()
	activeTray = t
	activeTrayMu.Unlock()

	initMenuHandlerClass()

	clsApp := objc.ID(objc.GetClass("NSApplication"))
	if clsApp == 0 {
		return fmt.Errorf("NSApplication class not found")
	}
	t.app = clsApp.Send(selSharedApplication)
	if t.app == 0 {
		return fmt.Errorf("sharedApplication returned nil")
	}

	// NSApplicationActivationPolicyAccessory = 1 (runs as menu bar item without Dock icon)
	t.app.Send(selSetActivationPolicy, uintptr(1))

	if menuHandlerClass != 0 {
		hAlloc := objc.ID(menuHandlerClass).Send(selAlloc)
		t.handler = hAlloc.Send(selInit)
	}

	clsStatusBar := objc.ID(objc.GetClass("NSStatusBar"))
	if clsStatusBar == 0 {
		return fmt.Errorf("NSStatusBar class not found")
	}
	statusBar := clsStatusBar.Send(selSystemStatusBar)
	if statusBar == 0 {
		return fmt.Errorf("systemStatusBar returned nil")
	}

	// -1.0 = NSVariableStatusItemLength
	t.statusItem = statusBar.Send(selStatusItemWithLength, float64(-1))
	if t.statusItem == 0 {
		return fmt.Errorf("statusItemWithLength returned nil")
	}
	t.statusItem.Send(selRetain)

	btn := t.statusItem.Send(selButton)
	if btn != 0 {
		t.setupButton(btn)
	}

	t.menu = t.buildMenu()
	t.statusItem.Send(selSetMenu, t.menu)

	t.mu.Lock()
	t.active = true
	t.mu.Unlock()

	return nil
}

func (t *DarwinTray) setupButton(btn objc.ID) {
	var imgLoaded bool
	if len(t.iconBytes) > 0 {
		clsData := objc.ID(objc.GetClass("NSData"))
		if clsData != 0 {
			data := clsData.Send(selDataWithBytesLength, uintptr(unsafe.Pointer(&t.iconBytes[0])), uintptr(len(t.iconBytes)))
			if data != 0 {
				clsImage := objc.ID(objc.GetClass("NSImage"))
				if clsImage != 0 {
					imgAlloc := clsImage.Send(selAlloc)
					img := objc.ID(objc.Send[objc.ID](imgAlloc, selInitWithData, data))
					if img != 0 {
						btn.Send(selSetImage, img)
						btn.Send(selSetImageScaling, uintptr(0)) // NSImageScaleProportionallyDown
						imgLoaded = true
					}
				}
			}
		}
	}

	if !imgLoaded {
		btn.Send(selSetTitle, nsString("GBF"))
	}

	t.updateTooltip()
}

func (t *DarwinTray) updateTooltip() {
	if t.statusItem == 0 {
		return
	}
	btn := t.statusItem.Send(selButton)
	if btn == 0 {
		return
	}
	state := "运行中"
	if !t.ctrl.IsRunning() {
		state = "已暂停"
	}
	tip := fmt.Sprintf("GBF-Accelerator (%s)\n代理端口: %d\n控制台端口: %d", state, t.ctrl.GetListenPort(), t.ctrl.GetControlPort())
	btn.Send(selSetToolTip, nsString(tip))
}

func (t *DarwinTray) buildMenu() objc.ID {
	clsMenu := objc.ID(objc.GetClass("NSMenu"))
	if clsMenu == 0 {
		return 0
	}
	mAlloc := clsMenu.Send(selAlloc)
	menu := objc.ID(objc.Send[objc.ID](mAlloc, selInitWithTitle, nsString("GBF Accelerator")))
	if menu == 0 {
		return 0
	}
	menu.Send(selSetAutoenablesItems, false)

	// 1. 打开控制台
	t.addMenuItem(menu, "打开控制台", selOpenConsoleAction, t.handler, true)

	// 2. 状态：运行中 / 已暂停 (informational display line, disabled)
	statusText := fmt.Sprintf("状态: 代理运行中 (端口 %d)", t.ctrl.GetListenPort())
	if !t.ctrl.IsRunning() {
		statusText = "状态: 代理已暂停"
	}
	t.addMenuItem(menu, statusText, 0, 0, false)

	// 3. 暂停代理加速 / 启动代理加速
	toggleText := "暂停代理加速"
	if !t.ctrl.IsRunning() {
		toggleText = "启动代理加速"
	}
	t.addMenuItem(menu, toggleText, selToggleProxyAction, t.handler, true)

	// 4. 打开本地缓存目录
	t.addMenuItem(menu, "打开本地缓存目录", selOpenCacheAction, t.handler, true)

	// Separator
	clsMenuItem := objc.ID(objc.GetClass("NSMenuItem"))
	if clsMenuItem != 0 {
		sep := clsMenuItem.Send(selSeparatorItem)
		if sep != 0 {
			menu.Send(selAddItem, sep)
		}
	}

	// 5. 退出程序
	t.addMenuItem(menu, "退出程序", selQuitAppAction, t.handler, true)

	return menu
}

func (t *DarwinTray) addMenuItem(menu objc.ID, title string, action objc.SEL, target objc.ID, enabled bool) objc.ID {
	clsMenuItem := objc.ID(objc.GetClass("NSMenuItem"))
	if clsMenuItem == 0 {
		return 0
	}
	itemAlloc := clsMenuItem.Send(selAlloc)
	item := objc.ID(objc.Send[objc.ID](itemAlloc, selInitWithTitleActionKeyEquiv, nsString(title), action, nsString("")))
	if item == 0 {
		return 0
	}
	if target != 0 {
		item.Send(selSetTarget, target)
	}
	item.Send(selSetEnabled, enabled)
	menu.Send(selAddItem, item)
	return item
}

func (t *DarwinTray) Update() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active || t.statusItem == 0 {
		return
	}
	newMenu := t.buildMenu()
	if newMenu != 0 {
		t.menu = newMenu
		t.statusItem.Send(selSetMenu, newMenu)
	}
	t.updateTooltip()
}

func (t *DarwinTray) Stop() {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		active := t.active
		t.active = false
		item := t.statusItem
		t.statusItem = 0
		app := t.app
		t.mu.Unlock()

		if active && item != 0 {
			clsStatusBar := objc.ID(objc.GetClass("NSStatusBar"))
			if clsStatusBar != 0 {
				statusBar := clsStatusBar.Send(selSystemStatusBar)
				if statusBar != 0 {
					statusBar.Send(selRemoveStatusItem, item)
				}
			}
			item.Send(selRelease)
		}

		if app != 0 {
			app.Send(selStop, uintptr(0))
			postDummyEvent(app)
		}
		if cfRunLoopStop != nil && cfRunLoopGetMain != nil {
			mainRL := cfRunLoopGetMain()
			if mainRL != 0 {
				cfRunLoopStop(mainRL)
				if cfRunLoopWakeUp != nil {
					cfRunLoopWakeUp(mainRL)
				}
			}
		}

		activeTrayMu.Lock()
		if activeTray == t {
			activeTray = nil
		}
		activeTrayMu.Unlock()

		close(t.doneChan)
	})
}

func (t *DarwinTray) RunLoop(sigCh <-chan os.Signal, quitChan <-chan struct{}) {
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\n[*] Received shutdown signal...")
		case <-quitChan:
			fmt.Println("\n[*] Received desktop quit command...")
		case <-t.doneChan:
			return
		}
		t.Stop()
	}()

	if t.app != 0 {
		t.app.Send(selRun)
	}
}

func handleOpenConsole() {
	activeTrayMu.Lock()
	t := activeTray
	activeTrayMu.Unlock()
	if t != nil && t.ctrl != nil {
		consoleURL := fmt.Sprintf("http://127.0.0.1:%d/", t.ctrl.GetControlPort())
		_ = t.ctrl.OpenAppWindow(consoleURL)
	}
}

func handleToggleProxy() {
	activeTrayMu.Lock()
	t := activeTray
	activeTrayMu.Unlock()
	if t != nil && t.ctrl != nil {
		if t.ctrl.IsRunning() {
			t.ctrl.StopProxy()
		} else {
			_ = t.ctrl.StartProxy()
		}
		t.Update()
	}
}

func handleOpenCache() {
	activeTrayMu.Lock()
	t := activeTray
	activeTrayMu.Unlock()
	if t != nil && t.ctrl != nil {
		_ = t.ctrl.OpenFolder(t.ctrl.GetCacheDir())
	}
}

func handleQuitApp() {
	activeTrayMu.Lock()
	t := activeTray
	activeTrayMu.Unlock()
	if t != nil {
		t.Stop()
		if t.ctrl != nil {
			go t.ctrl.Quit()
		}
	}
}

func postDummyEvent(app objc.ID) {
	clsEvent := objc.ID(objc.GetClass("NSEvent"))
	if clsEvent == 0 || app == 0 {
		return
	}
	defer func() {
		_ = recover()
	}()
	event := objc.ID(objc.Send[objc.ID](
		clsEvent,
		selOtherEventWithType,
		uintptr(15), // NSEventTypeApplicationDefined
		nsPoint{X: 0, Y: 0},
		uintptr(0),
		float64(0),
		0,
		uintptr(0),
		int16(0),
		0,
		0,
	))
	if event != 0 {
		app.Send(selPostEventAtStart, event, true)
	}
}

func nsString(str string) objc.ID {
	cls := objc.ID(objc.GetClass("NSString"))
	if cls == 0 {
		return 0
	}
	if str == "" {
		b := []byte{0}
		return cls.Send(selStringWithUTF8String, uintptr(unsafe.Pointer(&b[0])))
	}
	b := append([]byte(str), 0)
	return cls.Send(selStringWithUTF8String, uintptr(unsafe.Pointer(&b[0])))
}
