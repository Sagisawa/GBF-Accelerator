package ui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed dist
var distFS embed.FS

//go:embed icon.ico
var AppIconBytes []byte

var subFS fs.FS

func init() {
	var err error
	subFS, err = fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}

	// Register necessary MIME types for SPA and modern ESM modules
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
	_ = mime.AddExtensionType(".ico", "image/x-icon")
	_ = mime.AddExtensionType(".html", "text/html; charset=utf-8")
	_ = mime.AddExtensionType(".png", "image/png")
	_ = mime.AddExtensionType(".jpg", "image/jpeg")
	_ = mime.AddExtensionType(".jpeg", "image/jpeg")
	_ = mime.AddExtensionType(".webp", "image/webp")
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
}

// GetFS returns the http.FileSystem for serving embedded dashboard assets.
func GetFS() http.FileSystem {
	return http.FS(subFS)
}

// GetSubFS returns the underlying fs.FS.
func GetSubFS() fs.FS {
	return subFS
}

// ReadFile reads a named file from the embedded assets filesystem.
func ReadFile(name string) ([]byte, error) {
	cleanName := strings.TrimPrefix(filepath.ToSlash(name), "/")
	return fs.ReadFile(subFS, cleanName)
}

// HasEmbedded returns true if embedded assets are present.
func HasEmbedded() bool {
	if subFS == nil {
		return false
	}
	f, err := subFS.Open("index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// MIMEType returns the appropriate Content-Type header value for a path.
func MIMEType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	default:
		t := mime.TypeByExtension(ext)
		if t != "" {
			return t
		}
		return "application/octet-stream"
	}
}
