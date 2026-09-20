//go:build !windows && !darwin

package desktop

// OtherTray provides a platform-isolated stub for Linux and other Unix builds.
type OtherTray struct {
	ctrl Controller
}

func NewTray(ctrl Controller, iconBytes []byte) Tray {
	return &OtherTray{ctrl: ctrl}
}

func (t *OtherTray) Start() error {
	return nil
}

func (t *OtherTray) Update() {}

func (t *OtherTray) Stop() {}
