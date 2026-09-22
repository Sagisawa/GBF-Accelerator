//go:build windows
// +build windows

package cert

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

func isCAInstalled(sha1 string, _ string) bool {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return false
	}

	// 1. Fast path: check Windows Registry (HKCU / HKLM) via reg query
	for _, hive := range []string{"HKCU", "HKLM"} {
		regPath := fmt.Sprintf(`%s\Software\Microsoft\SystemCertificates\Root\Certificates\%s`, hive, cleanSHA1)
		cmd := exec.Command("reg", "query", regPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			return true
		}
	}

	// 2. Fallback: certutil -user -verifystore Root <sha1>
	cmd := exec.Command("certutil", "-user", "-verifystore", "Root", cleanSHA1)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmd.Run() == nil {
		return true
	}

	// 3. Fallback: certutil -verifystore Root <sha1> (machine-level store)
	cmdMachine := exec.Command("certutil", "-verifystore", "Root", cleanSHA1)
	cmdMachine.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmdMachine.Run() == nil
}

// detectCAStore reports which Root store hive contains the given SHA-1
// thumbprint: "HKCU" (current user) or "HKLM" (local machine). Returns ""
// when the thumbprint is not found in either store.
func detectCAStore(sha1 string) string {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return ""
	}

	// 1. Registry fast path: HKCU first, then HKLM
	for _, hive := range []string{"HKCU", "HKLM"} {
		regPath := fmt.Sprintf(`%s\Software\Microsoft\SystemCertificates\Root\Certificates\%s`, hive, cleanSHA1)
		cmd := exec.Command("reg", "query", regPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			return hive
		}
	}

	// 2. certutil fallback: user store then machine store
	cmdUser := exec.Command("certutil", "-user", "-verifystore", "Root", cleanSHA1)
	cmdUser.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmdUser.Run() == nil {
		return "HKCU"
	}
	cmdMachine := exec.Command("certutil", "-verifystore", "Root", cleanSHA1)
	cmdMachine.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if cmdMachine.Run() == nil {
		return "HKLM"
	}
	return ""
}

func installCA(caPath string) error {
	cmd := exec.Command("certutil", "-addstore", "-user", "Root", caPath)
	// NOTE: do NOT set SysProcAttr.HideWindow here. certutil must be allowed
	// to show the native Windows certificate security confirmation dialog so
	// the user can explicitly approve installing the Root CA (v1.6 parity).
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certutil addstore failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uninstallCA(sha1 string) error {
	cleanSHA1 := strings.ToUpper(strings.TrimSpace(sha1))
	if cleanSHA1 == "" {
		return fmt.Errorf("empty sha1 thumbprint")
	}

	// 1. Remove through the certificate store API first (certutil -delstore).
	//    The native Windows confirmation dialog must stay visible here (no
	//    SysProcAttr.HideWindow), matching v1.6 behavior. Deleting only via a
	//    raw registry write instead makes CryptoAPI treat the next addstore of
	//    the same certificate as a silent re-add, which suppresses the Root CA
	//    security warning on the following install.
	cmd := exec.Command("certutil", "-f", "-user", "-delstore", "Root", cleanSHA1)
	cmd.Stdin = bytes.NewReader([]byte("y\r\n"))
	delErr := cmd.Run()

	// 2. Registry cleanup as secondary fallback (HKCU / HKLM). Only runs after
	//    a successful store-level deletion, so a dialog dismissed by the user
	//    never leaves the store bypassed in a half-removed state.
	if delErr == nil {
		for _, hive := range []string{"HKCU", "HKLM"} {
			regPath := fmt.Sprintf(`%s\Software\Microsoft\SystemCertificates\Root\Certificates\%s`, hive, cleanSHA1)
			delCmd := exec.Command("reg", "delete", regPath, "/f")
			delCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			_ = delCmd.Run()
		}
	}

	if delErr != nil {
		return fmt.Errorf("certutil delstore failed: %w", delErr)
	}
	return nil
}

func checkLegacyLeakedCAInstalled() bool {
	// Check registry
	for _, hive := range []string{"HKCU", "HKLM"} {
		regPath := fmt.Sprintf(`%s\Software\Microsoft\SystemCertificates\Root\Certificates\%s`, hive, LegacyLeakedCASHA1)
		cmd := exec.Command("reg", "query", regPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			return true
		}
	}

	// Check certutil with hash
	cmd := exec.Command("certutil", "-user", "-verifystore", "Root", LegacyLeakedCASHA1)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err == nil {
		return true
	}

	// Check certutil with old name "GBF Speed CA"
	cmdName := exec.Command("certutil", "-user", "-verifystore", "Root", "GBF Speed CA")
	cmdName.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmdName.Run() == nil
}

func cleanLegacyLeakedCA() error {
	_ = uninstallCA(LegacyLeakedCASHA1)

	// Also delete by name "GBF Speed CA"
	cmd := exec.Command("certutil", "-f", "-user", "-delstore", "Root", "GBF Speed CA")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdin = bytes.NewReader([]byte("y\r\n"))
	_ = cmd.Run()

	return nil
}
