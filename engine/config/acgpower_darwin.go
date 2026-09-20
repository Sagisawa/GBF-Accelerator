//go:build darwin
// +build darwin

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func autoDetectACGPowerCache() string {
	home, _ := os.UserHomeDir()

	// 1. Process check
	if cmdOut, err := exec.Command("ps", "-ax", "-o", "command=").Output(); err == nil {
		lines := strings.Split(string(cmdOut), "\n")
		for _, l := range lines {
			lower := strings.ToLower(l)
			if strings.Contains(lower, "acgpower") {
				if appIdx := strings.Index(lower, ".app"); appIdx != -1 {
					appPath := strings.Trim(strings.TrimSpace(l[:appIdx+4]), `"'`)
					if fi, err := os.Stat(filepath.Join(appPath, "cache")); err == nil && fi.IsDir() {
						norm := NormalizeCacheDir(filepath.Join(appPath, "cache"))
						return norm
					}
					norm := NormalizeCacheDir(appPath)
					return norm
				}
			}
		}
	}

	// 2. Common directories
	searchDirs := []string{
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "Documents"),
		home,
		"/Applications",
		filepath.Join(home, "Applications"),
		filepath.Join(home, "Library", "Application Support"),
		filepath.Join(home, "Library", "Caches"),
	}

	if volumes, err := os.ReadDir("/Volumes"); err == nil {
		for _, v := range volumes {
			if v.IsDir() && v.Name() != "Macintosh HD" {
				searchDirs = append(searchDirs, filepath.Join("/Volumes", v.Name()))
			}
		}
	}

	subNames := []string{
		"ACGPOWER-MAC-2",
		"ACGPOWER-MAC",
		"acgpower",
		"ACGPower",
	}

	for _, base := range searchDirs {
		for _, sub := range subNames {
			target := filepath.Join(base, sub)
			if fi, err := os.Stat(target); err == nil && fi.IsDir() {
				norm := NormalizeCacheDir(target)
				if fi, err := os.Stat(filepath.Join(norm, "assets")); err == nil && fi.IsDir() {
					return norm
				}
			}
		}
	}

	gbfAccCache := filepath.Join(home, "Library", "Application Support", "GBF-Accelerator", "cache")
	if fi, err := os.Stat(gbfAccCache); err == nil && fi.IsDir() {
		return NormalizeCacheDir(gbfAccCache)
	}

	return ""
}
