//go:build darwin

package desktop

import (
	"fmt"
)

// DarwinTray provides a platform-isolated stub with native notification for macOS builds.
type DarwinTray struct {
	ctrl Controller
}

func NewTray(ctrl Controller, iconBytes []byte) Tray {
	return &DarwinTray{ctrl: ctrl}
}

func (t *DarwinTray) Start() error {
	ctrlPort := t.ctrl.GetControlPort()
	ShowNotification("GBF-Accelerator 已就绪", fmt.Sprintf("控制台已在后台运行 (http://127.0.0.1:%d)", ctrlPort))
	return nil
}

func (t *DarwinTray) Update() {}

func (t *DarwinTray) Stop() {}
