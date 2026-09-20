package res

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"

	"gbf-proxy/proxy"
)

//go:embed SwitchyOmega_GBF.bak
var SwitchyOmegaBak []byte

//go:embed 使用说明.txt
var InstructionsTxt []byte

// EnsureHelperFiles writes the generated proxy.pac when it is absent or still
// appears to be a previously generated helper, while preserving user-customized
// PAC files. SwitchyOmega_GBF.bak and 使用说明.txt are created only when absent.
func EnsureHelperFiles(targetDir string, proxyPort int) error {
	if targetDir == "" {
		targetDir = "."
	}
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	// 1. Create/update the generated PAC helper without overwriting a custom PAC.
	pacPath := filepath.Join(targetDir, "proxy.pac")
	pacContent := proxy.GetPAC("127.0.0.1", proxyPort)
	writePAC := false
	if existing, err := os.ReadFile(pacPath); err != nil {
		if os.IsNotExist(err) {
			writePAC = true
		} else {
			return err
		}
	} else {
		// Generated helper PACs contain the proxy.pac routing target. If the file
		// does not match that shape, treat it as user-owned and preserve it.
		text := string(existing)
		writePAC = strings.Contains(text, "FindProxyForURL") &&
			(strings.Contains(text, "PROXY 127.0.0.1:") ||
				strings.Contains(text, "PROXY localhost:"))
	}
	if writePAC {
		if err := os.WriteFile(pacPath, []byte(pacContent), 0644); err != nil {
			return err
		}
	}

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
