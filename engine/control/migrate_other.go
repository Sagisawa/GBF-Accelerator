//go:build !darwin
// +build !darwin

package control

import (
	"net/http"
)

func getStandardCacheDir() string {
	return ""
}

func isStandardCacheDir(cacheDir string) bool {
	// On non-macOS platforms, non-standard directory warnings do not apply.
	return true
}

func (c *ControlServer) handleMigrateStandardCache(w http.ResponseWriter, req *http.Request) {
	c.sendJSON(w, http.StatusBadRequest, map[string]string{
		"error": "仅在 macOS 平台支持迁移至 macOS 标准目录",
	})
}
