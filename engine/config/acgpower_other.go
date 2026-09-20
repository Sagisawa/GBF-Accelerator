//go:build !windows && !darwin
// +build !windows,!darwin

package config

import (
	"os"
	"path/filepath"
)

func autoDetectACGPowerCache() string {
	candidates := []string{
		filepath.Join("cache", "gbf", "https"),
		filepath.Join("..", "cache", "gbf", "https"),
	}
	for _, cand := range candidates {
		norm := NormalizeCacheDir(cand)
		if fi, err := os.Stat(filepath.Join(norm, "assets")); err == nil && fi.IsDir() {
			return norm
		}
	}
	return ""
}
