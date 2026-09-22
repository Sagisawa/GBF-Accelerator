//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	shell32               = syscall.NewLazyDLL("shell32.dll")
	shBrowseForFolderW    = shell32.NewProc("SHBrowseForFolderW")
	shGetPathFromIDListW  = shell32.NewProc("SHGetPathFromIDListW")
	coTaskMemFree         = syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree")

	user32               = syscall.NewLazyDLL("user32.dll")
	getForegroundWindow  = user32.NewProc("GetForegroundWindow")
	getWindowThreadPID   = user32.NewProc("GetWindowThreadProcessId")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	getCurrentThreadID   = kernel32.NewProc("GetCurrentThreadId")
	attachThreadInput    = user32.NewProc("AttachThreadInput")
	setForegroundWindow  = user32.NewProc("SetForegroundWindow")
	showWindow           = user32.NewProc("ShowWindow")
	bringWindowToTop     = user32.NewProc("BringWindowToTop")
	enumWindows          = user32.NewProc("EnumWindows")
	getClassNameW        = user32.NewProc("GetClassNameW")
)

const swShowNormal = 1

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

// openAppWindow launches Microsoft Edge, Google Chrome, or Chromium-based browsers in standalone App mode (--app=<url>).
// This opens a clean, borderless native window with Win32 title bar and no address bar or tabs.
func openAppWindow(url string) error {
	appURL := prepareAppURL(url)

	browserPath := findAppBrowserWindows()
	if browserPath != "" {
		cmd := exec.Command(browserPath, fmt.Sprintf("--app=%s", appURL), "--window-size=880,640")
		if err := cmd.Start(); err == nil {
			go func() {
				_ = cmd.Wait()
			}()
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
