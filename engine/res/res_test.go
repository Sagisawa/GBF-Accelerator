package res

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureHelperFiles(t *testing.T) {
	tempDir := t.TempDir()

	err := EnsureHelperFiles(tempDir, 8124)
	if err != nil {
		t.Fatalf("EnsureHelperFiles failed: %v", err)
	}

	pacPath := filepath.Join(tempDir, "proxy.pac")
	pacData, err := os.ReadFile(pacPath)
	if err != nil {
		t.Fatalf("proxy.pac not written: %v", err)
	}
	if !strings.Contains(string(pacData), "127.0.0.1:8124") {
		t.Errorf("expected 127.0.0.1:8124 in proxy.pac, got: %s", string(pacData))
	}

	bakPath := filepath.Join(tempDir, "SwitchyOmega_GBF.bak")
	bakData, err := os.ReadFile(bakPath)
	if err != nil || len(bakData) == 0 {
		t.Fatalf("SwitchyOmega_GBF.bak not written or empty")
	}

	for _, name := range []string{"使用说明.txt", "INSTRUCTIONS_EN.txt", "INSTRUCTIONS_JA.txt"} {
		txtPath := filepath.Join(tempDir, name)
		txtData, err := os.ReadFile(txtPath)
		if err != nil || len(txtData) == 0 {
			t.Fatalf("%s not written or empty", name)
		}
	}

	// Re-run with different port (e.g. 8126): proxy.pac must update!
	err = EnsureHelperFiles(tempDir, 8126)
	if err != nil {
		t.Fatalf("EnsureHelperFiles second run failed: %v", err)
	}
	pacData2, _ := os.ReadFile(pacPath)
	if !strings.Contains(string(pacData2), "127.0.0.1:8126") {
		t.Errorf("expected updated 127.0.0.1:8126 in proxy.pac")
	}
}

func TestEnsureHelperFilesPreservesCustomPAC(t *testing.T) {
	tempDir := t.TempDir()
	pacPath := filepath.Join(tempDir, "proxy.pac")
	custom := "function FindProxyForURL(url, host) { return \"DIRECT\"; }"
	if err := os.WriteFile(pacPath, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureHelperFiles(tempDir, 9000); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(pacPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Fatalf("custom PAC was overwritten")
	}
}

func TestInstructionFilesParity(t *testing.T) {
	rootDir := filepath.Join("..", "..")
	pairs := []struct {
		name       string
		embedded   []byte
		boosterHdr string
	}{
		{"使用说明.txt", InstructionsTxt, "chrome.exe"},
		{"INSTRUCTIONS_EN.txt", InstructionsEnTxt, "chrome.exe"},
		{"INSTRUCTIONS_JA.txt", InstructionsJaTxt, "chrome.exe"},
		{"SwitchyOmega_GBF.bak", SwitchyOmegaBak, ""},
	}

	for _, p := range pairs {
		rootFile := filepath.Join(rootDir, p.name)
		data, err := os.ReadFile(rootFile)
		if err != nil {
			if os.IsNotExist(err) {
				t.Skipf("skipping root parity test in isolated build environment: %s not found", rootFile)
				return
			}
			t.Fatalf("failed to read root file %s: %v", p.name, err)
		}
		if string(data) != string(p.embedded) {
			t.Errorf("file %s is out of sync between repository root and engine/res embedded asset", p.name)
		}
		if p.boosterHdr != "" {
			text := string(p.embedded)
			if !strings.Contains(text, p.boosterHdr) || !strings.Contains(text, "chrome.app") {
				t.Errorf("file %s is missing commercial booster rename tips (chrome.exe / chrome.app)", p.name)
			}
		}
	}
}
