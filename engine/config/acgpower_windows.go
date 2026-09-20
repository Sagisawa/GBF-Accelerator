//go:build windows
// +build windows

package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

var kernel32DLL = syscall.NewLazyDLL("kernel32.dll")
var getLogicalDrivesProc = kernel32DLL.NewProc("GetLogicalDrives")

func getRunningACGPowerPath() string {
	psCmd := exec.Command("powershell", "-NoProfile", "-Command",
		`(Get-CimInstance Win32_Process -Filter "Name LIKE '%acgpower%'").ExecutablePath`)
	psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := psCmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				dir := filepath.Dir(l)
				if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
					return dir
				}
			}
		}
	}
	return ""
}

func autoDetectACGPowerCache() string {
	// 1. Check if ACGPower is actively running in background
	if runningDir := getRunningACGPowerPath(); runningDir != "" {
		norm := NormalizeCacheDir(runningDir)
		if fi, err := os.Stat(filepath.Join(norm, "assets")); err == nil && fi.IsDir() {
			return norm
		}
		if fi, err := os.Stat(norm); err == nil && fi.IsDir() && strings.EqualFold(filepath.Base(norm), "https") {
			return norm
		}
	}

	// 2. Check base_dir parents and siblings
	candidates := []string{
		filepath.Join("cache", "gbf", "https"),
		filepath.Join("..", "cache", "gbf", "https"),
		filepath.Join("..", "..", "cache", "gbf", "https"),
		filepath.Join("..", "acgpower"),
		filepath.Join("..", "ACGPower"),
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "cache", "gbf", "https"),
			filepath.Join(exeDir, "..", "cache", "gbf", "https"),
			filepath.Join(exeDir, "..", "acgpower"),
			filepath.Join(exeDir, "..", "ACGPower"),
		)
	}

	for _, cand := range candidates {
		norm := NormalizeCacheDir(cand)
		if fi, err := os.Stat(filepath.Join(norm, "assets")); err == nil && fi.IsDir() {
			return norm
		}
	}

	// 3. Dynamic system drives scan
	var driveRoots []string
	ret, _, _ := getLogicalDrivesProc.Call()
	bitmask := uint32(ret)
	if bitmask != 0 {
		for i := 0; i < 26; i++ {
			if (bitmask & (1 << i)) != 0 {
				drive := fmt.Sprintf("%c:\\", 'A'+i)
				driveRoots = append(driveRoots, drive)
			}
		}
	} else {
		driveRoots = []string{`C:\`, `D:\`, `E:\`, `F:\`}
	}

	subDirs := []string{
		"acgpower",
		"ACGPower",
		filepath.Join("Games", "acgpower"),
		filepath.Join("Games", "ACGPower"),
		filepath.Join("Game", "acgpower"),
		filepath.Join("Program Files", "acgpower"),
		filepath.Join("Program Files (x86)", "acgpower"),
		filepath.Join("Software", "acgpower"),
		filepath.Join("Tools", "acgpower"),
	}

	for _, d := range driveRoots {
		for _, sub := range subDirs {
			root := filepath.Join(d, sub)
			if fi, err := os.Stat(root); err == nil && fi.IsDir() {
				norm := NormalizeCacheDir(root)
				if fi, err := os.Stat(filepath.Join(norm, "assets")); err == nil && fi.IsDir() {
					return norm
				}
			}
		}
	}

	return ""
}
