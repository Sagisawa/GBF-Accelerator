//go:build windows

package desktop

import (
	"encoding/binary"
	"gbf-proxy/ui"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockController struct {
	running bool
}

func (m *mockController) IsRunning() bool              { return m.running }
func (m *mockController) StartProxy() error            { m.running = true; return nil }
func (m *mockController) StopProxy()                   { m.running = false }
func (m *mockController) GetListenPort() int           { return 8124 }
func (m *mockController) GetControlPort() int          { return 8125 }
func (m *mockController) GetCacheDir() string          { return "C:\\cache" }
func (m *mockController) OpenBrowser(url string) error   { return nil }
func (m *mockController) OpenAppWindow(url string) error { return nil }
func (m *mockController) OpenFolder(path string) error   { return nil }
func (m *mockController) Quit()                        {}

func TestAppIconBytes(t *testing.T) {
	data := ui.AppIconBytes
	if len(data) < 6 {
		t.Fatalf("AppIconBytes too short: %d", len(data))
	}
	count := binary.LittleEndian.Uint16(data[4:6])
	if count == 0 {
		t.Fatalf("expected at least 1 icon entry, got %d", count)
	}
	for i := 0; i < int(count); i++ {
		offset := 6 + i*16
		w := int(data[offset])
		h := int(data[offset+1])
		bpp := binary.LittleEndian.Uint16(data[offset+6 : offset+8])
		size := binary.LittleEndian.Uint32(data[offset+8 : offset+12])
		imgOff := binary.LittleEndian.Uint32(data[offset+12 : offset+16])
		t.Logf("Icon entry %d: %dx%d, bpp=%d, size=%d, offset=%d", i, w, h, bpp, size, imgOff)
	}
}

func TestWindowsTrayLifecycle(t *testing.T) {
	ctrl := &mockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)

	err := tray.Start()
	if err != nil {
		t.Logf("tray.Start() returned error (expected in headless sandbox): %v", err)
		return
	}

	tray.Update()
	time.Sleep(50 * time.Millisecond)

	// Clean stop
	tray.Stop()
}

func TestWindowsTray_ShowNotification_UninitializedNoPanic(t *testing.T) {
	ctrl := &mockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)
	// hwnd == 0, not started
	tray.ShowNotification("title", "message")
}

func TestWindowsTray_ShowNotification_LongBoundarySafety(t *testing.T) {
	ctrl := &mockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)

	// Strings far exceeding the buffer lengths (64 title, 256 info)
	veryLongTitle := "GBF-Accelerator 线路切换 " + strings.Repeat("VeryLongTitleHeaderOverFlowSafetyCheck", 10)
	veryLongMessage := "检测到主上游连接故障，已自动切换至备用上游。" + strings.Repeat("SuperLongNotificationBodyBufferOverflowPreventionVerification", 20)

	// Verify it does not panic on long strings even if uninitialized or initialized
	tray.ShowNotification(veryLongTitle, veryLongMessage)

	// If tray starts in current environment, test with active tray as well
	err := tray.Start()
	if err == nil {
		defer tray.Stop()
		tray.ShowNotification(veryLongTitle, veryLongMessage)
		tray.ShowNotification("GBF-Accelerator 线路切换", "检测到主上游连接故障，已自动切换至备用上游。当前页面如出现加载或通信异常，请按 F5 刷新。")
	}
}

func TestWindowsTray_ShowNotification_AfterStop(t *testing.T) {
	ctrl := &mockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)

	err := tray.Start()
	if err != nil {
		t.Logf("tray.Start() error: %v", err)
		return
	}

	tray.Stop()
	time.Sleep(50 * time.Millisecond)

	// Must safely no-op without panic after stop
	tray.ShowNotification("title after stop", "message after stop")
}

func TestWindowsTray_ConcurrentNotificationAndStop(t *testing.T) {
	ctrl := &mockController{running: true}
	tray := NewTray(ctrl, ui.AppIconBytes)

	err := tray.Start()
	if err != nil {
		t.Logf("tray.Start() error: %v", err)
		return
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tray.ShowNotification("title", "message")
				tray.Update()
			}
		}(i)
	}

	time.Sleep(10 * time.Millisecond)
	tray.Stop()
	wg.Wait()
}
