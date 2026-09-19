//go:build windows

package desktop

import (
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")
	modUser32   = syscall.NewLazyDLL("user32.dll")
	modShell32  = syscall.NewLazyDLL("shell32.dll")

	procGetModuleHandleW         = modKernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW        = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW         = modUser32.NewProc("CreateWindowExW")
	procDestroyWindow           = modUser32.NewProc("DestroyWindow")
	procDefWindowProcW          = modUser32.NewProc("DefWindowProcW")
	procPostQuitMessage         = modUser32.NewProc("PostQuitMessage")
	procGetMessageW             = modUser32.NewProc("GetMessageW")
	procTranslateMessage        = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW        = modUser32.NewProc("DispatchMessageW")
	procPostMessageW            = modUser32.NewProc("PostMessageW")
	procCreatePopupMenu         = modUser32.NewProc("CreatePopupMenu")
	procDestroyMenu             = modUser32.NewProc("DestroyMenu")
	procAppendMenuW             = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenuEx        = modUser32.NewProc("TrackPopupMenuEx")
	procGetCursorPos            = modUser32.NewProc("GetCursorPos")
	procSetForegroundWindow     = modUser32.NewProc("SetForegroundWindow")
	procCreateIconFromResourceEx = modUser32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon             = modUser32.NewProc("DestroyIcon")
	procLoadIconW               = modUser32.NewProc("LoadIconW")
	procRegisterWindowMessageW  = modUser32.NewProc("RegisterWindowMessageW")

	procShell_NotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
)

const (
	wmUser        = 0x0400
	wmTrayIcon    = wmUser + 101
	wmClose       = 0x0010
	wmDestroy     = 0x0002
	wmCommand     = 0x0111
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfDisabled  = 0x00000002
	mfSeparator = 0x00000800

	tpmRightButton = 0x0002
	tpmBottomAlign = 0x0020

	cmdOpenConsole = 1001
	cmdStatusText  = 1002
	cmdToggleProxy = 1003
	cmdOpenCache   = 1004
	cmdQuitApp     = 1005
)

type point struct {
	x, y int32
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type WindowsTray struct {
	ctrl      Controller
	iconBytes []byte
	hwnd      uintptr
	hIcon     uintptr
	nid       notifyIconDataW
	mu        sync.Mutex
	readyChan chan error
	doneChan  chan struct{}
	stopOnce  sync.Once
}

var (
	activeTray       *WindowsTray
	activeTrayMu     sync.Mutex
	wmTaskbarCreated uint32
	lastClickMu      sync.Mutex
	lastClickTime    time.Time
)

// NewTray creates a Windows system tray instance.
func NewTray(ctrl Controller, iconBytes []byte) Tray {
	return &WindowsTray{
		ctrl:      ctrl,
		iconBytes: iconBytes,
		readyChan: make(chan error, 1),
		doneChan:  make(chan struct{}),
	}
}

func (t *WindowsTray) Start() error {
	activeTrayMu.Lock()
	activeTray = t
	activeTrayMu.Unlock()

	go t.runLoop()
	return <-t.readyChan
}

func (t *WindowsTray) runLoop() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("GBFAcceleratorTrayClass")

	wcex := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hInstance,
		lpszClassName: className,
	}

	ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcex)))
	if ret == 0 && err != nil && err.(syscall.Errno) != 1410 { // 1410 = ERROR_CLASS_ALREADY_EXISTS
		t.readyChan <- fmt.Errorf("failed to register tray window class: %w", err)
		return
	}

	windowName, _ := syscall.UTF16PtrFromString("GBFAcceleratorTrayWindow")
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 0, 0,
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		t.readyChan <- fmt.Errorf("failed to create tray window: %w", err)
		return
	}

	t.hwnd = hwnd
	t.hIcon = t.loadIcon()

	t.nid = notifyIconDataW{
		cbSize:           uint32(unsafe.Sizeof(notifyIconDataW{})),
		hWnd:             hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTrayIcon,
		hIcon:            t.hIcon,
	}

	// Register TaskbarCreated message to re-add icon if Windows Explorer restarts
	taskbarStr, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	rMsg, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(taskbarStr)))
	wmTaskbarCreated = uint32(rMsg)

	t.setTooltip(t.getTooltipText())
	procShell_NotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid)))

	t.readyChan <- nil

	// Win32 message pump
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	// Cleanup on exit: remove tray icon and release resources
	procShell_NotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&t.nid)))
	if t.hIcon != 0 {
		procDestroyIcon.Call(t.hIcon)
		t.hIcon = 0
	}

	activeTrayMu.Lock()
	if activeTray == t {
		activeTray = nil
	}
	activeTrayMu.Unlock()

	close(t.doneChan)
}

func (t *WindowsTray) loadIcon() uintptr {
	if len(t.iconBytes) >= 22 && t.iconBytes[0] == 0 && t.iconBytes[1] == 0 && t.iconBytes[2] == 1 && t.iconBytes[3] == 0 {
		count := int(binary.LittleEndian.Uint16(t.iconBytes[4:6]))
		bestOffset := uint32(0)
		bestSize := uint32(0)

		for i := 0; i < count; i++ {
			entryOffset := 6 + i*16
			if entryOffset+16 > len(t.iconBytes) {
				break
			}
			w := int(t.iconBytes[entryOffset])
			h := int(t.iconBytes[entryOffset+1])
			bytesInRes := binary.LittleEndian.Uint32(t.iconBytes[entryOffset+8 : entryOffset+12])
			imgOffset := binary.LittleEndian.Uint32(t.iconBytes[entryOffset+12 : entryOffset+16])

			if w == 16 && h == 16 {
				bestOffset = imgOffset
				bestSize = bytesInRes
				break
			}
			if w == 32 && h == 32 && bestOffset == 0 {
				bestOffset = imgOffset
				bestSize = bytesInRes
			}
			if bestOffset == 0 {
				bestOffset = imgOffset
				bestSize = bytesInRes
			}
		}

		if bestOffset > 0 && int(bestOffset+bestSize) <= len(t.iconBytes) {
			hIcon, _, _ := procCreateIconFromResourceEx.Call(
				uintptr(unsafe.Pointer(&t.iconBytes[bestOffset])),
				uintptr(bestSize),
				1,          // TRUE = icon
				0x00030000, // dwVersion
				16,
				16,
				0,
			)
			if hIcon != 0 {
				return hIcon
			}
		}
	}

	// Fallback to standard application icon
	hIcon, _, _ := procLoadIconW.Call(0, 32512) // IDI_APPLICATION
	return hIcon
}

func (t *WindowsTray) getTooltipText() string {
	state := "运行中"
	if !t.ctrl.IsRunning() {
		state = "已暂停"
	}
	return fmt.Sprintf("GBF-Accelerator (%s)\n代理端口: %d\n控制台端口: %d", state, t.ctrl.GetListenPort(), t.ctrl.GetControlPort())
}

func (t *WindowsTray) setTooltip(text string) {
	utf16, _ := syscall.UTF16FromString(text)
	for i := range t.nid.szTip {
		if i < len(utf16) {
			t.nid.szTip[i] = utf16[i]
		} else {
			t.nid.szTip[i] = 0
		}
	}
}

func (t *WindowsTray) Update() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.hwnd == 0 {
		return
	}
	t.setTooltip(t.getTooltipText())
	procShell_NotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&t.nid)))
}

func (t *WindowsTray) Stop() {
	t.stopOnce.Do(func() {
		t.mu.Lock()
		hwnd := t.hwnd
		t.mu.Unlock()
		if hwnd != 0 {
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
			select {
			case <-t.doneChan:
			case <-time.After(2 * time.Second):
			}
		}
	})
}

func wndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	uMsg := uint32(msg)

	activeTrayMu.Lock()
	t := activeTray
	activeTrayMu.Unlock()

	if t == nil {
		ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
		return ret
	}

	if wmTaskbarCreated != 0 && uMsg == wmTaskbarCreated {
		t.mu.Lock()
		procShell_NotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&t.nid)))
		t.mu.Unlock()
		return 0
	}

	switch uMsg {
	case wmTrayIcon:
		switch lParam {
		case wmLButtonUp, wmLButtonDbl:
			lastClickMu.Lock()
			now := time.Now()
			if now.Sub(lastClickTime) < 500*time.Millisecond {
				lastClickMu.Unlock()
				return 0
			}
			lastClickTime = now
			lastClickMu.Unlock()

			consoleURL := fmt.Sprintf("http://127.0.0.1:%d/", t.ctrl.GetControlPort())
			_ = t.ctrl.OpenAppWindow(consoleURL)
			return 0

		case wmRButtonUp:
			t.showContextMenu()
			return 0
		}

	case wmCommand:
		cmdID := int(wParam & 0xFFFF)
		switch cmdID {
		case cmdOpenConsole:
			consoleURL := fmt.Sprintf("http://127.0.0.1:%d/", t.ctrl.GetControlPort())
			_ = t.ctrl.OpenAppWindow(consoleURL)
		case cmdToggleProxy:
			if t.ctrl.IsRunning() {
				t.ctrl.StopProxy()
			} else {
				_ = t.ctrl.StartProxy()
			}
			t.Update()
		case cmdOpenCache:
			_ = t.ctrl.OpenFolder(t.ctrl.GetCacheDir())
		case cmdQuitApp:
			procDestroyWindow.Call(hwnd)
			go t.ctrl.Quit()
		}
		return 0

	case wmClose:
		procDestroyWindow.Call(hwnd)
		return 0

	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return ret
}

func (t *WindowsTray) showContextMenu() {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	titleStr, _ := syscall.UTF16PtrFromString("GBF-Accelerator 控制台")
	procAppendMenuW.Call(hMenu, mfString, cmdOpenConsole, uintptr(unsafe.Pointer(titleStr)))

	// Status informational line
	statusText := fmt.Sprintf("状态: 代理运行中 (端口 %d)", t.ctrl.GetListenPort())
	if !t.ctrl.IsRunning() {
		statusText = "状态: 代理已暂停"
	}
	statStr, _ := syscall.UTF16PtrFromString(statusText)
	procAppendMenuW.Call(hMenu, mfString|mfDisabled|mfGrayed, cmdStatusText, uintptr(unsafe.Pointer(statStr)))

	// Toggle proxy
	toggleText := "暂停代理加速"
	if !t.ctrl.IsRunning() {
		toggleText = "启动代理加速"
	}
	togStr, _ := syscall.UTF16PtrFromString(toggleText)
	procAppendMenuW.Call(hMenu, mfString, cmdToggleProxy, uintptr(unsafe.Pointer(togStr)))

	// Open cache folder
	cacheStr, _ := syscall.UTF16PtrFromString("打开本地缓存目录")
	procAppendMenuW.Call(hMenu, mfString, cmdOpenCache, uintptr(unsafe.Pointer(cacheStr)))

	// Separator
	procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)

	// Quit
	quitStr, _ := syscall.UTF16PtrFromString("退出程序")
	procAppendMenuW.Call(hMenu, mfString, cmdQuitApp, uintptr(unsafe.Pointer(quitStr)))

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(t.hwnd)
	procTrackPopupMenuEx.Call(hMenu, tpmRightButton|tpmBottomAlign, uintptr(pt.x), uintptr(pt.y), t.hwnd, 0)
	// KB135788: send WM_NULL to dismiss menu when user clicks outside
	procPostMessageW.Call(t.hwnd, 0, 0, 0)
}
