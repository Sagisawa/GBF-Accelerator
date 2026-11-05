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
			t.Run(p, func(t *testing.T) {
				err := ValidateCacheDirForPlatform(p, targetGOOS, homeDir, nil)
				if !g.wantErr {
					if err != nil {
						t.Fatalf("expected valid path for %q, got: %v", p, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("expected error for path %q, got nil", p)
				}
				vErr, ok := err.(*CacheDirValidationError)
				if !ok {
					t.Fatalf("expected *CacheDirValidationError, got %T: %v", err, err)
				}
				if g.errCode != "" && vErr.Code != g.errCode {
					t.Errorf("expected error code %s for %q, got %s (msg: %s)", g.errCode, p, vErr.Code, vErr.Message)
				}
			})
		}
	}
}

func TestValidateCacheDir_WindowsPaths(t *testing.T) {
	home := `C:\Users\TestUser`
	runCacheDirTests(t, "windows", home, []cacheDirGroup{
		// 0. Filesystem root, relative current dir, traversal and extended prefix traps
		{paths: []string{`\`, `/`, `.`, `C:\..`, `\\?\`}, wantErr: true, errCode: ErrCodeRoot},
		// 1. Drive roots (standard, lowercase, slash, colon-only, secondary drive, extended)
		{paths: []string{`C:\`, `c:\`, `C:/`, `C:`, `D:\`, `\\?\C:\`, `\\?\C:/`}, wantErr: true, errCode: ErrCodeDriveRoot},
		// 2. UNC share roots (backslash, slash, extended, bare prefix/host) vs allowed subdirectories
		{paths: []string{
			`\\fileserver\share`, `\\fileserver\share\`, `//fileserver/share`, `\\?\UNC\fileserver\share`,
			`\\`, `//`, `\\?\UNC\`, `\\fileserver`, `\\fileserver\`,
		}, wantErr: true, errCode: ErrCodeUNCRoot},
		{paths: []string{`\\fileserver\share\gbf_cache`, `\\?\UNC\fileserver\share\gbf_cache`, `\\fileserver\share\games\cache`}, wantErr: false},
		// 3. System directories (case-insensitive, System32, Program Files, recycle bins, system volume info)
		{paths: []string{`C:\Windows`, `c:\windows`, `C:\Windows\System32`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\Program Files\Common Files`, `C:\ProgramData`, `C:\Recovery`, `C:\$Recycle.Bin`, `D:\$Recycle.Bin`, `C:\System Volume Information`, `D:\System Volume Information`}, wantErr: true, errCode: ErrCodeSystemDir},
		// 4. Protected user directories (exact match vs ancestor vs allowed descendants)
		{paths: []string{`C:\Users\TestUser`, `c:\users\testuser`, `C:\Users\TestUser\Downloads`, `C:\Users\TestUser\Desktop`, `C:\Users\TestUser\Documents`, `C:\Users\TestUser\Pictures`, `C:\Users\TestUser\Music`, `C:\Users\TestUser\Videos`}, wantErr: true, errCode: ErrCodeUserProtectedDir},
		{paths: []string{`C:\Users`}, wantErr: true, errCode: ErrCodeAncestorProtected},
		{paths: []string{`C:\Users\TestUser\Downloads\ACGPower\cache`, `C:\Users\TestUser\Desktop\my_cache`, `D:\Games\GBF\cache`, `cache\gbf\https`}, wantErr: false},
		// 5. Non-descendant prefix mismatches & special root checks
		{paths: []string{`C:\Users\TestUser\Downloads_temp`, `C:\Users\TestUser\Desktop2`, `C:\Users_backup`, `C:\Users\OtherUser\cache`}, wantErr: false},
		{paths: []string{`C:\Users`}, wantErr: true},
		{paths: []string{`D:\Users`}, wantErr: true, errCode: ErrCodeSystemDir},
	})
}

func TestValidateCacheDir_MacOSLinuxPaths(t *testing.T) {
	home := "/Users/testuser"
	runCacheDirTests(t, "darwin", home, []cacheDirGroup{
		// 1. Filesystem root
		{paths: []string{"/"}, wantErr: true, errCode: ErrCodeRoot},
		// 2. Strict system directories (/bin, /sbin, /usr, /etc, /System, /Library, /Applications)
		{paths: []string{"/bin", "/sbin", "/usr", "/usr/bin", "/usr/local/bin", "/etc", "/System", "/System/Library", "/Library", "/Applications"}, wantErr: true, errCode: ErrCodeSystemDir},
		// 3. System roots allowing subdirectories vs allowed temp / mount points
		{paths: []string{"/var", "/private/var", "/tmp", "/private/tmp", "/home", "/Volumes", "/mnt", "/media"}, wantErr: true, errCode: ErrCodeSystemDir},
		{paths: []string{"/Users"}, wantErr: true},
		{paths: []string{"/Volumes/SSD/cache", "/mnt/disk/cache", "/media/usb/cache", "/home/other/cache", "/tmp/gbf_cache", "/private/tmp/gbf_cache", "/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/test12345/cache", "/private/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/test12345/cache"}, wantErr: false},
		// 4. Protected user directories (home, standard folders, ancestors) vs allowed subdirectories
		{paths: []string{"/Users/testuser", "/Users/testuser/Downloads", "/Users/testuser/Desktop", "/Users/testuser/Documents"}, wantErr: true, errCode: ErrCodeUserProtectedDir},
		{paths: []string{"/Users"}, wantErr: true, errCode: ErrCodeAncestorProtected},
		{paths: []string{"/Users/testuser/Downloads/ACGPower/cache", "/Users/testuser/Library/Application Support/GBF-Accelerator/cache", "/Users/testuser/Downloads_other"}, wantErr: false},
	})
}

func TestValidateCacheDir_LinuxPaths(t *testing.T) {
	home := "/home/testuser"
	runCacheDirTests(t, "linux", home, []cacheDirGroup{
		// 1. Filesystem root and relative current directory
		{paths: []string{"/", "."}, wantErr: true, errCode: ErrCodeRoot},
		// 2. Strict system directories (/bin, /sbin, /usr, /etc)
		{paths: []string{"/bin", "/sbin", "/usr", "/usr/bin", "/usr/local/bin", "/etc"}, wantErr: true, errCode: ErrCodeSystemDir},
		// 3. System roots allowing subdirectories vs allowed temp / mount points
		{paths: []string{"/var", "/tmp", "/mnt", "/media"}, wantErr: true, errCode: ErrCodeSystemDir},
		{paths: []string{"/mnt/disk/cache", "/media/usb/cache", "/home/other/cache", "/tmp/gbf_cache"}, wantErr: false},
		// 4. Protected user directories: home, standard capitalized and lowercase folders, ancestors vs allowed subdirectories
		{paths: []string{
			"/home/testuser",
			"/home/testuser/Downloads", "/home/testuser/downloads",
			"/home/testuser/Desktop", "/home/testuser/desktop",
			"/home/testuser/Documents", "/home/testuser/documents",
			"/home/testuser/Pictures", "/home/testuser/pictures",
			"/home/testuser/Music", "/home/testuser/music",
			"/home/testuser/Videos", "/home/testuser/videos",
		}, wantErr: true, errCode: ErrCodeUserProtectedDir},
		{paths: []string{"/home"}, wantErr: true, errCode: ErrCodeAncestorProtected},
		{paths: []string{"/home/testuser/Downloads/ACGPower/cache", "/home/testuser/Desktop/my_cache", "/home/testuser/Downloads_temp"}, wantErr: false},
	})
}

func TestValidateCacheDir_SymlinkAncestorFallback(t *testing.T) {
	tmpDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil {
		tmpDir = resolved
	}
	existingParent := filepath.Join(tmpDir, "existing_parent")
	if err := os.MkdirAll(existingParent, 0755); err != nil {
		t.Fatalf("failed to create existing parent: %v", err)
	}
	nonexistentChild := filepath.Join(existingParent, "nonexistent", "sub", "cache")
	if resolved := resolveSymlinksWithAncestors(nonexistentChild); !strings.HasPrefix(resolved, existingParent) {
		t.Errorf("expected resolved path to be based on %q, got %q", existingParent, resolved)
	}
	if err := ValidateCacheDir(nonexistentChild); err != nil {
		t.Errorf("expected ValidateCacheDir to succeed for temp child dir, got: %v", err)
	}
}

func TestIsSystemJunk(t *testing.T) {
	junkFiles := []string{
		".DS_Store", "._foo.png", "._bar.js", ".Spotlight-V100", ".spotlight-v100",
		".fseventsd", ".Trashes", "Thumbs.db", "thumbs.db", "desktop.ini",
		"DESKTOP.INI", "$RECYCLE.BIN", "$recycle.bin", "System Volume Information", "system volume information",
	}
	for _, name := range junkFiles {
		if !IsSystemJunk(name) {
			t.Errorf("expected %q to be recognized as system junk", name)
		}
	}
	for _, name := range []string{"doc.txt", "notes.pdf", "btn.png", "app.js", "style.css", "data.csv", "Thumbs.db.bak"} {
		if IsSystemJunk(name) {
			t.Errorf("expected %q NOT to be recognized as system junk", name)
		}
	}
}

func TestIsDirClean(t *testing.T) {
	tmpDir := t.TempDir()
	if clean, err := IsDirClean(tmpDir); err != nil || !clean {
		t.Errorf("expected empty dir to be clean, got clean=%v, err=%v", clean, err)
	}
	for _, name := range []string{".DS_Store", "Thumbs.db", "desktop.ini"} {
		_ = os.WriteFile(filepath.Join(tmpDir, name), []byte("junk"), 0644)
	}
	if clean, err := IsDirClean(tmpDir); err != nil || !clean {
		t.Errorf("expected dir with only system junk to be clean, got clean=%v, err=%v", clean, err)
	}
	_ = os.WriteFile(filepath.Join(tmpDir, "user_document.txt"), []byte("hello"), 0644)
	if clean, err := IsDirClean(tmpDir); err != nil || clean {
		t.Errorf("expected dir with user file NOT to be clean, got clean=%v, err=%v", clean, err)
	}
}
