package res

import (
	_ "embed"
	"os"
	"path/filepath"

	"gbf-proxy/proxy"
)

//go:embed SwitchyOmega_GBF.bak
var SwitchyOmegaBak []byte

//go:embed 使用说明.txt
var InstructionsTxt []byte

// EnsureHelperFiles writes or updates proxy.pac and ensures SwitchyOmega_GBF.bak
// and 使用说明.txt are present in targetDir.
func EnsureHelperFiles(targetDir string, proxyPort int) error {
	if targetDir == "" {
		targetDir = "."
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	// 1. Always update proxy.pac with current port
	pacPath := filepath.Join(targetDir, "proxy.pac")
	pacContent := proxy.GetPAC("127.0.0.1", proxyPort)
	_ = os.WriteFile(pacPath, []byte(pacContent), 0644)

	// 2. Ensure SwitchyOmega_GBF.bak
	bakPath := filepath.Join(targetDir, "SwitchyOmega_GBF.bak")
	if fi, err := os.Stat(bakPath); err != nil || fi.Size() == 0 {
		_ = os.WriteFile(bakPath, SwitchyOmegaBak, 0644)
	}

	// 3. Ensure 使用说明.txt
	txtPath := filepath.Join(targetDir, "使用说明.txt")
	if fi, err := os.Stat(txtPath); err != nil || fi.Size() == 0 {
		_ = os.WriteFile(txtPath, InstructionsTxt, 0644)
	}

	return nil
}
