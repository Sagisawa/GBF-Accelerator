//go:build darwin

package desktop

// DarwinTray provides a platform-isolated stub for macOS builds.
type DarwinTray struct {
	ctrl Controller
}

func NewTray(ctrl Controller, iconBytes []byte) Tray {
	return &DarwinTray{ctrl: ctrl}
}

func (t *DarwinTray) Start() error {
	return nil
}

func (t *DarwinTray) Update() {}

func (t *DarwinTray) Stop() {}
