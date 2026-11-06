package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type cacheDirGroup struct {
	paths   []string
	wantErr bool
	errCode string
}

func runCacheDirTests(t *testing.T, targetGOOS, homeDir string, groups []cacheDirGroup) {
	t.Helper()
	for _, g := range groups {
		for _, p := range g.paths {
			err := ValidateCacheDirForPlatform(p, targetGOOS, homeDir, nil)
			vErr, ok := err.(*CacheDirValidationError)
			if (!g.wantErr && err != nil) || (g.wantErr && (!ok || (g.errCode != "" && vErr.Code != g.errCode))) {
				t.Errorf("[%s] %q: wantErr=%v code=%s, got %v", targetGOOS, p, g.wantErr, g.errCode, err)
			}
		}
	}
}

func TestValidateCacheDir_Platforms(t *testing.T) {
	unixCommonSys := []string{"/bin", "/sbin", "/usr", "/usr/bin", "/usr/local/bin", "/etc", "/var", "/tmp", "/mnt", "/media"}
	linuxUserProtected := []string{"/home/testuser"}
	for _, d := range []string{"Downloads", "Desktop", "Documents", "Pictures", "Music", "Videos"} {
		linuxUserProtected = append(linuxUserProtected, "/home/testuser/"+d, "/home/testuser/"+strings.ToLower(d))
	}
	for _, tc := range []struct {
		goos   string
		home   string
		groups []cacheDirGroup
	}{
		{goos: "windows", home: `C:\Users\TestUser`, groups: []cacheDirGroup{
			{paths: []string{`\`, `/`, `.`, `C:\..`, `\\?\`}, wantErr: true, errCode: ErrCodeRoot},
			{paths: []string{`C:\`, `c:\`, `C:/`, `C:`, `D:\`, `\\?\C:\`, `\\?\C:/`}, wantErr: true, errCode: ErrCodeDriveRoot},
			{paths: []string{`\\fileserver\share`, `\\fileserver\share\`, `//fileserver/share`, `\\?\UNC\fileserver\share`, `\\`, `//`, `\\?\UNC\`, `\\fileserver`, `\\fileserver\`}, wantErr: true, errCode: ErrCodeUNCRoot},
			{paths: []string{`\\fileserver\share\gbf_cache`, `\\?\UNC\fileserver\share\gbf_cache`, `\\fileserver\share\games\cache`}, wantErr: false},
			{paths: []string{`C:\Windows`, `c:\windows`, `C:\Windows\System32`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\Program Files\Common Files`, `C:\ProgramData`, `C:\Recovery`, `C:\$Recycle.Bin`, `D:\$Recycle.Bin`, `C:\System Volume Information`, `D:\System Volume Information`}, wantErr: true, errCode: ErrCodeSystemDir},
			{paths: []string{`C:\Users\TestUser`, `c:\users\testuser`, `C:\Users\TestUser\Downloads`, `C:\Users\TestUser\Desktop`, `C:\Users\TestUser\Documents`, `C:\Users\TestUser\Pictures`, `C:\Users\TestUser\Music`, `C:\Users\TestUser\Videos`}, wantErr: true, errCode: ErrCodeUserProtectedDir},
			{paths: []string{`C:\Users`}, wantErr: true, errCode: ErrCodeAncestorProtected},
			{paths: []string{`C:\Users\TestUser\Downloads\ACGPower\cache`, `C:\Users\TestUser\Desktop\my_cache`, `D:\Games\GBF\cache`, `cache\gbf\https`, `C:\Users\TestUser\Downloads_temp`, `C:\Users\TestUser\Desktop2`, `C:\Users_backup`, `C:\Users\OtherUser\cache`}, wantErr: false},
			{paths: []string{`D:\Users`}, wantErr: true, errCode: ErrCodeSystemDir},
		}},
		{goos: "darwin", home: "/Users/testuser", groups: []cacheDirGroup{
			{paths: []string{"/"}, wantErr: true, errCode: ErrCodeRoot},
			{paths: append([]string{"/System", "/System/Library", "/Library", "/Applications", "/private/var", "/private/tmp", "/home", "/Volumes"}, unixCommonSys...), wantErr: true, errCode: ErrCodeSystemDir},
			{paths: []string{"/Users"}, wantErr: true, errCode: ErrCodeAncestorProtected},
			{paths: []string{"/Volumes/SSD/cache", "/mnt/disk/cache", "/media/usb/cache", "/home/other/cache", "/tmp/gbf_cache", "/private/tmp/gbf_cache", "/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/test12345/cache", "/private/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/test12345/cache"}, wantErr: false},
			{paths: []string{"/Users/testuser", "/Users/testuser/Downloads", "/Users/testuser/Desktop", "/Users/testuser/Documents"}, wantErr: true, errCode: ErrCodeUserProtectedDir},
			{paths: []string{"/Users/testuser/Downloads/ACGPower/cache", "/Users/testuser/Library/Application Support/GBF-Accelerator/cache", "/Users/testuser/Downloads_other"}, wantErr: false},
		}},
		{goos: "linux", home: "/home/testuser", groups: []cacheDirGroup{
			{paths: []string{"/", "."}, wantErr: true, errCode: ErrCodeRoot},
			{paths: unixCommonSys, wantErr: true, errCode: ErrCodeSystemDir},
			{paths: []string{"/mnt/disk/cache", "/media/usb/cache", "/home/other/cache", "/tmp/gbf_cache", "/home/testuser/Downloads/ACGPower/cache", "/home/testuser/Desktop/my_cache", "/home/testuser/Downloads_temp"}, wantErr: false},
			{paths: linuxUserProtected, wantErr: true, errCode: ErrCodeUserProtectedDir},
			{paths: []string{"/home"}, wantErr: true, errCode: ErrCodeAncestorProtected},
		}},
	} {
		t.Run(tc.goos, func(t *testing.T) { runCacheDirTests(t, tc.goos, tc.home, tc.groups) })
	}
}

func TestValidateCacheDir_SymlinkAncestorFallback(t *testing.T) {
	tmpDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil { tmpDir = resolved }
	parent := filepath.Join(tmpDir, "existing_parent")
	_ = os.MkdirAll(parent, 0755)
	child := filepath.Join(parent, "nonexistent", "sub", "cache")
	if resolved := resolveSymlinksWithAncestors(child); !strings.HasPrefix(resolved, parent) || ValidateCacheDir(child) != nil {
		t.Errorf("symlink ancestor fallback failed for %s (resolved=%s)", child, resolved)
	}
}

func TestIsSystemJunk(t *testing.T) {
	for name, want := range map[string]bool{
		".DS_Store": true, "._foo.png": true, "._bar.js": true, ".Spotlight-V100": true, ".spotlight-v100": true,
		".fseventsd": true, ".Trashes": true, "Thumbs.db": true, "thumbs.db": true, "desktop.ini": true,
		"DESKTOP.INI": true, "$RECYCLE.BIN": true, "$recycle.bin": true, "System Volume Information": true,
		"system volume information": true, "doc.txt": false, "notes.pdf": false, "btn.png": false,
		"app.js": false, "style.css": false, "data.csv": false, "Thumbs.db.bak": false,
	} {
		if IsSystemJunk(name) != want {
			t.Errorf("IsSystemJunk(%q) = %v, want %v", name, !want, want)
		}
	}
}

func TestIsDirClean(t *testing.T) {
	tmpDir := t.TempDir()
	if clean, err := IsDirClean(tmpDir); err != nil || !clean { t.Errorf("expected empty dir clean, got %v", err) }
	for _, name := range []string{".DS_Store", "Thumbs.db", "desktop.ini"} { _ = os.WriteFile(filepath.Join(tmpDir, name), []byte("junk"), 0644) }
	if clean, err := IsDirClean(tmpDir); err != nil || !clean { t.Errorf("expected dir with junk to be clean, got clean=%v, err=%v", clean, err) }
	_ = os.WriteFile(filepath.Join(tmpDir, "user_document.txt"), []byte("hello"), 0644)
	if clean, err := IsDirClean(tmpDir); err != nil || clean { t.Errorf("expected dir with user file NOT to be clean, got clean=%v, err=%v", clean, err) }
}
