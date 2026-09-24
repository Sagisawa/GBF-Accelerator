//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle   = 0x0040
	bifEditBox          = 0x0010
)

type browseInfo struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

var (
	shell32              = syscall.NewLazyDLL("shell32.dll")
	shBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	shGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	coTaskMemFree        = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")

	user32                       = syscall.NewLazyDLL("user32.dll")
	getForegroundWindow          = user32.NewProc("GetForegroundWindow")
	getWindowThreadPID           = user32.NewProc("GetWindowThreadProcessId")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	getCurrentThreadID           = kernel32.NewProc("GetCurrentThreadId")
	attachThreadInput            = user32.NewProc("AttachThreadInput")
	setForegroundWindow          = user32.NewProc("SetForegroundWindow")
	showWindow                   = user32.NewProc("ShowWindow")
	bringWindowToTop             = user32.NewProc("BringWindowToTop")
	enumWindows                  = user32.NewProc("EnumWindows")
	getClassNameW                = user32.NewProc("GetClassNameW")
	setWindowPos                 = user32.NewProc("SetWindowPos")
	getWindowRect                = user32.NewProc("GetWindowRect")
	isWindowVisible              = user32.NewProc("IsWindowVisible")
	getDpiForWindow              = user32.NewProc("GetDpiForWindow")
	setThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
)

const (
	swShowNormal                         = 1
	swMaximize                           = 3
	swRestore                            = 9
	swpNoZOrder                          = 0x0004
	swpNoActivate                        = 0x0010
	dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3) // -4 in two's complement
)

type rect struct {
	left, top, right, bottom int32
}

func enumExplorerWindows() map[uintptr]struct{} {
	windows := make(map[uintptr]struct{})
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		classBuf := make([]uint16, 64)
		ret, _, _ := getClassNameW.Call(
			hwnd,
			uintptr(unsafe.Pointer(&classBuf[0])),
			uintptr(len(classBuf)),
		)
		if ret == 0 {
			return 1
		}
		className := syscall.UTF16ToString(classBuf[:ret])
		if className == "CabinetWClass" || className == "ExploreWClass" {
			windows[hwnd] = struct{}{}
		}
		return 1
	})
	_, _, _ = enumWindows.Call(cb, 0)
	return windows
}

func activateExplorerWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}

	foreground, _, _ := getForegroundWindow.Call()
	foregroundThread, _, _ := getWindowThreadPID.Call(foreground, 0)
	targetThread, _, _ := getWindowThreadPID.Call(hwnd, 0)
	currentThread, _, _ := getCurrentThreadID.Call()

	// Windows intentionally restricts background processes from stealing focus.
	// Link the caller's input queue to the foreground and target threads for
	// the duration of activation, then detach immediately.
	attachedForeground := false
	attachedTarget := false
	if foregroundThread != 0 && currentThread != 0 && foregroundThread != currentThread {
		ret, _, _ := attachThreadInput.Call(currentThread, foregroundThread, 1)
		attachedForeground = ret != 0
	}
	if targetThread != 0 && currentThread != 0 && targetThread != currentThread {
		ret, _, _ := attachThreadInput.Call(currentThread, targetThread, 1)
		attachedTarget = ret != 0
	}

	if attachedTarget {
		defer attachThreadInput.Call(currentThread, targetThread, 0)
	}
	if attachedForeground {
		defer attachThreadInput.Call(currentThread, foregroundThread, 0)
	}

	showWindow.Call(hwnd, swShowNormal)
	bringWindowToTop.Call(hwnd)
	ret, _, _ := setForegroundWindow.Call(hwnd)
	return ret != 0
}

func openFolderNative(path string) error {
	before := enumExplorerWindows()

	cmd := exec.Command("explorer.exe", "/n,"+path)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()

	// Explorer commonly delegates the command to the existing shell process,
	// so the spawned PID is not the window we need to activate. Find the new
	// Explorer top-level window instead.
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := enumExplorerWindows()
		for hwnd := range after {
			if _, existed := before[hwnd]; existed {
				continue
			}
			if activateExplorerWindow(hwnd) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func showInFolderNative(path string) error {
	fi, err := os.Stat(path)
	if err == nil && !fi.IsDir() {
		before := enumExplorerWindows()
		cmd := exec.Command("explorer.exe", "/select,"+path)
		if startErr := cmd.Start(); startErr != nil {
			return startErr
		}
		go func() {
			_ = cmd.Wait()
		}()
		deadline := time.Now().Add(2 * time.Second)
		for {
			after := enumExplorerWindows()
			for hwnd := range after {
				if _, existed := before[hwnd]; existed {
					continue
				}
				if activateExplorerWindow(hwnd) {
					return nil
				}
			}
			if time.Now().After(deadline) {
				return nil
			}
			time.Sleep(40 * time.Millisecond)
		}
	}
	targetDir := path
	if err == nil && !fi.IsDir() {
		targetDir = filepath.Dir(path)
	} else if err != nil {
		targetDir = filepath.Dir(path)
	}
	return openFolderNative(targetDir)
}

// prepareAppURL appends standalone=1 to the query string if not already present.
func prepareAppURL(url string) string {
	appURL := url
	if !strings.Contains(appURL, "standalone=1") {
		if strings.Contains(appURL, "?") {
			appURL += "&standalone=1"
		} else {
			appURL += "?standalone=1"
		}
	}
	return appURL
}

// appModeUserDataDir returns a dedicated, stable user data directory for standalone App mode.
// This isolates the console from the user's daily Edge/Chrome browsing session, preventing
// ProcessSingleton from discarding window sizing command-line switches, and allows Chromium
// to naturally persist window placement in its profile.
func appModeUserDataDir() (string, error) {
	localAppData := os.Getenv("LocalAppData")
	if localAppData == "" {
		localAppData = os.TempDir()
	}
	dir := filepath.Join(localAppData, "GBF-Accelerator", "AppProfile")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	// Touch Chromium's "First Run" sentinel file to permanently suppress first-run wizards.
	sentinel := filepath.Join(dir, "First Run")
	if _, err := os.Stat(sentinel); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(sentinel, []byte{}, 0644)
	}
	return dir, nil
}

// buildAppWindowArgs builds the Chromium App-mode command-line arguments.
// Maximized and explicit window size are mutually exclusive so the saved state is deterministic.
func buildAppWindowArgs(appURL, userDataDir string, width, height int, maximized bool) []string {
	args := []string{
		fmt.Sprintf("--app=%s", appURL),
		fmt.Sprintf("--user-data-dir=%s", userDataDir),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-background-mode",
		"--disable-features=msFirstRunExperience,msEdgeWelcomeExperience,msSignInPrompt",
	}
	if maximized {
		return append(args, "--start-maximized")
	}
	if width < 400 {
		width = 880
	}
	if height < 400 {
		height = 640
	}
	return append(args, fmt.Sprintf("--window-size=%d,%d", width, height))
}

// findProcessTreePIDs returns all process IDs belonging to the process tree rooted at rootPID.
// Chromium uses a multi-process architecture where the main browser window might be created
// by a child process of the launcher.
func findProcessTreePIDs(rootPID uint32) map[uint32]struct{} {
	tree := map[uint32]struct{}{rootPID: {}}
	if rootPID == 0 {
		return tree
	}
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return tree
	}
	defer syscall.CloseHandle(snapshot)

	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return tree
	}
	for {
		if _, ok := tree[entry.ParentProcessID]; ok {
			tree[entry.ProcessID] = struct{}{}
		}
		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return tree
}

// findAppWindowInTree locates the primary Chrome_WidgetWin_1 window belonging to any PID in the process tree.
func findAppWindowInTree(tree map[uint32]struct{}) uintptr {
	if len(tree) == 0 {
		return 0
	}

	var best uintptr
	var bestArea int64

	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var windowPID uint32
		getWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
		if _, ok := tree[windowPID]; !ok {
			return 1
		}

		visible, _, _ := isWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}

		classBuf := make([]uint16, 64)
		ret, _, _ := getClassNameW.Call(
			hwnd,
			uintptr(unsafe.Pointer(&classBuf[0])),
			uintptr(len(classBuf)),
		)
		if ret == 0 || syscall.UTF16ToString(classBuf[:ret]) != "Chrome_WidgetWin_1" {
			return 1
		}

		var r rect
		if v, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); v == 0 {
			return 1
		}
		w := int64(r.right - r.left)
		h := int64(r.bottom - r.top)
		// Filter out auxiliary / IME / small indicator windows (e.g. 50x50)
		if w <= 200 || h <= 200 {
			return 1
		}

		area := w * h
		if area > bestArea {
			best = hwnd
			bestArea = area
		}
		return 1
	})
	_, _, _ = enumWindows.Call(cb, 0)
	return best
}

// applyWindowGeometry applies the persisted size or maximized state to the window HWND
// using the effective Per-Monitor V2 DPI scaling.
func applyWindowGeometry(hwnd uintptr, width, height int, maximized bool) bool {
	if hwnd == 0 {
		return false
	}

	if maximized {
		showWindow.Call(hwnd, swMaximize)
		return true
	}

	if width < 400 {
		width = 880
	}
	if height < 400 {
		height = 640
	}

	var r rect
	ret, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ret == 0 {
		return false
	}

	// Chromium outerWidth/outerHeight are CSS pixels. Convert to physical pixels via effective DPI.
	dpi := uint32(96)
	if getDpiForWindow.Find() == nil {
		if v, _, _ := getDpiForWindow.Call(hwnd); v >= 48 && v <= 768 {
			dpi = uint32(v)
		}
	}
	expectedW := int32((width*int(dpi) + 48) / 96)
	expectedH := int32((height*int(dpi) + 48) / 96)

	showWindow.Call(hwnd, swRestore)
	_, _, _ = setWindowPos.Call(
		hwnd,
		0,
		uintptr(r.left),
		uintptr(r.top),
		uintptr(expectedW),
		uintptr(expectedH),
		uintptr(swpNoZOrder|swpNoActivate),
	)

	// Short verification loop: verify HWND bounds via GetWindowRect
	for i := 0; i < 8; i++ {
		time.Sleep(50 * time.Millisecond)
		var cur rect
		if v, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&cur))); v == 0 {
			break
		}
		curW := cur.right - cur.left
		curH := cur.bottom - cur.top
		if absInt32(curW-expectedW) <= 4 && absInt32(curH-expectedH) <= 4 {
			return true
		}
		// Re-apply if Chromium layout reset the bounds
		_, _, _ = setWindowPos.Call(
			hwnd,
			0,
			uintptr(cur.left),
			uintptr(cur.top),
			uintptr(expectedW),
			uintptr(expectedH),
			uintptr(swpNoZOrder|swpNoActivate),
		)
	}
	return true
}

func restoreAppWindowGeometry(pid uint32, width, height int, maximized bool) {
	if pid == 0 {
		return
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if setThreadDpiAwarenessContext.Find() == nil {
		oldCtx, _, _ := setThreadDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2)
		if oldCtx != 0 {
			defer setThreadDpiAwarenessContext.Call(oldCtx)
		}
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		tree := findProcessTreePIDs(pid)
		hwnd := findAppWindowInTree(tree)
		if hwnd != 0 {
			if applyWindowGeometry(hwnd, width, height, maximized) {
				return
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// openAppWindow launches Microsoft Edge, Google Chrome, or Chromium-based browsers in standalone App mode (--app=<url>).
// This opens a clean, borderless native window with Win32 title bar and no address bar or tabs.
func openAppWindow(url string) error {
	return openAppWindowWithGeometry(url, 880, 640, false)
}

// openAppWindowWithGeometry launches the standalone App window using persisted geometry.
func openAppWindowWithGeometry(url string, width, height int, maximized bool) error {
	appURL := prepareAppURL(url)

	browserPath := findAppBrowserWindows()
	if browserPath != "" {
		userDataDir, err := appModeUserDataDir()
		if err != nil {
			return fmt.Errorf("failed to create standalone browser profile: %w", err)
		}
		args := buildAppWindowArgs(appURL, userDataDir, width, height, maximized)
		cmd := exec.Command(browserPath, args...)
		if err := cmd.Start(); err == nil {
			pid := uint32(cmd.Process.Pid)
			go func() {
				_ = cmd.Wait()
			}()
			go restoreAppWindowGeometry(pid, width, height, maximized)
			return nil
		}
	}

	return OpenBrowser(url)
}

// findAppBrowserWindows searches standard install locations, system registry, and PATH for Edge, Chrome, or Brave.
func findAppBrowserWindows() string {
	var candidates []string

	progFiles86 := os.Getenv("ProgramFiles(x86)")
	progFiles := os.Getenv("ProgramFiles")
	localAppData := os.Getenv("LocalAppData")

	if progFiles86 != "" {
		candidates = append(candidates,
			filepath.Join(progFiles86, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(progFiles86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(progFiles86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	if progFiles != "" {
		candidates = append(candidates,
			filepath.Join(progFiles, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(progFiles, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(progFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	if localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(localAppData, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(localAppData, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(localAppData, "Chromium", "Application", "chrome.exe"),
		)
	}

	// Hardcoded standard locations as fallback
	candidates = append(candidates,
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
	)

	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}

	// Query Windows registry as fallback with hidden window to prevent console flashes
	regKeys := []string{
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`,
		`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\brave.exe`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`,
		`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\brave.exe`,
	}
	for _, key := range regKeys {
		cmd := exec.Command("reg", "query", key, "/ve")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				if strings.Contains(line, "REG_SZ") {
					parts := strings.SplitN(line, "REG_SZ", 2)
					if len(parts) == 2 {
						p := strings.Trim(strings.TrimSpace(parts[1]), "\"")
						if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
							return p
						}
					}
				}
			}
		}
	}

	// Look in system PATH
	for _, bin := range []string{"msedge.exe", "chrome.exe", "brave.exe"} {
		if p, err := exec.LookPath(bin); err == nil {
			return p
		}
	}

	return ""
}

// ChooseFolder displays the native Windows shell folder picker.
// The active foreground window is used as the dialog owner so the picker
// stays in front of the GUI that initiated the request.
func ChooseFolder(prompt string) (string, error) {
	if prompt == "" {
		prompt = "选择保存目录"
	}

	title, err := syscall.UTF16PtrFromString(prompt)
	if err != nil {
		return "", err
	}

	displayName := make([]uint16, syscall.MAX_PATH)
	pathBuffer := make([]uint16, syscall.MAX_PATH)

	owner, _, _ := getForegroundWindow.Call()
	bi := browseInfo{
		hwndOwner:      owner,
		pszDisplayName: &displayName[0],
		lpszTitle:      title,
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle | bifEditBox,
	}

	pidl, _, _ := shBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer coTaskMemFree.Call(pidl)

	ok, _, callErr := shGetPathFromIDListW.Call(
		pidl,
		uintptr(unsafe.Pointer(&pathBuffer[0])),
	)
	if ok == 0 {
		if callErr != syscall.Errno(0) {
			return "", callErr
		}
		return "", errors.New("failed to resolve selected folder path")
	}

	return strings.TrimSpace(syscall.UTF16ToString(pathBuffer)), nil
}
