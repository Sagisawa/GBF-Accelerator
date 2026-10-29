package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCacheDir_WindowsPaths(t *testing.T) {
	home := `C:\Users\TestUser`

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errCode string
	}{
		// 1. Drive roots
		{"drive root upper C", `C:\`, true, ErrCodeDriveRoot},
		{"drive root lower c", `c:\`, true, ErrCodeDriveRoot},
		{"drive root slash", `C:/`, true, ErrCodeDriveRoot},
		{"drive root colon only", `C:`, true, ErrCodeDriveRoot},
		{"drive root D", `D:\`, true, ErrCodeDriveRoot},

		// 2. UNC share root vs subdirectories
		{"UNC share root backslash", `\\fileserver\share`, true, ErrCodeUNCRoot},
		{"UNC share root trailing backslash", `\\fileserver\share\`, true, ErrCodeUNCRoot},
		{"UNC share root slash", `//fileserver/share`, true, ErrCodeUNCRoot},
		{"UNC share subdirectory", `\\fileserver\share\gbf_cache`, false, ""},
		{"UNC share deep subdir", `\\fileserver\share\games\cache`, false, ""},

		// 3. System directories (case-insensitive)
		{"Windows directory exact", `C:\Windows`, true, ErrCodeSystemDir},
		{"Windows directory lower", `c:\windows`, true, ErrCodeSystemDir},
		{"Windows system32 subdir", `C:\Windows\System32`, true, ErrCodeSystemDir},
		{"Program Files exact", `C:\Program Files`, true, ErrCodeSystemDir},
		{"Program Files x86 exact", `C:\Program Files (x86)`, true, ErrCodeSystemDir},
		{"Program Files subdir", `C:\Program Files\Common Files`, true, ErrCodeSystemDir},
		{"ProgramData", `C:\ProgramData`, true, ErrCodeSystemDir},
		{"Recovery", `C:\Recovery`, true, ErrCodeSystemDir},
		{"C Recycle Bin", `C:\$Recycle.Bin`, true, ErrCodeSystemDir},
		{"D Recycle Bin", `D:\$Recycle.Bin`, true, ErrCodeSystemDir},
		{"System Volume Information", `C:\System Volume Information`, true, ErrCodeSystemDir},
		{"D System Volume Information", `D:\System Volume Information`, true, ErrCodeSystemDir},

		// 4. Protected user directories (exact match vs ancestor vs descendant)
		{"User home exact", `C:\Users\TestUser`, true, ErrCodeUserProtectedDir},
		{"User home lower", `c:\users\testuser`, true, ErrCodeUserProtectedDir},
		{"User home ancestor C:\\Users", `C:\Users`, true, ErrCodeAncestorProtected},
		{"Downloads folder exact", `C:\Users\TestUser\Downloads`, true, ErrCodeUserProtectedDir},
		{"Desktop folder exact", `C:\Users\TestUser\Desktop`, true, ErrCodeUserProtectedDir},
		{"Documents folder exact", `C:\Users\TestUser\Documents`, true, ErrCodeUserProtectedDir},
		{"Pictures folder exact", `C:\Users\TestUser\Pictures`, true, ErrCodeUserProtectedDir},
		{"Music folder exact", `C:\Users\TestUser\Music`, true, ErrCodeUserProtectedDir},
		{"Videos folder exact", `C:\Users\TestUser\Videos`, true, ErrCodeUserProtectedDir},

		// Descendants MUST be allowed
		{"Downloads subdir ACGPower cache", `C:\Users\TestUser\Downloads\ACGPower\cache`, false, ""},
		{"Desktop subdir my_cache", `C:\Users\TestUser\Desktop\my_cache`, false, ""},
		{"Dedicated game cache folder", `D:\Games\GBF\cache`, false, ""},
		{"Standard relative path", `cache\gbf\https`, false, ""},

		// "不要按前缀" - non-descendant prefixes must NOT be wrongly rejected
		{"Downloads_temp prefix mismatch", `C:\Users\TestUser\Downloads_temp`, false, ""},
		{"Desktop2 prefix mismatch", `C:\Users\TestUser\Desktop2`, false, ""},
		{"Users_backup prefix mismatch", `C:\Users_backup`, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCacheDirForPlatform(tt.path, "windows", home, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for path %q, got nil", tt.path)
				}
				vErr, ok := err.(*CacheDirValidationError)
				if !ok {
					t.Fatalf("expected *CacheDirValidationError, got %T: %v", err, err)
				}
				if tt.errCode != "" && vErr.Code != tt.errCode {
					t.Errorf("expected error code %s, got %s (msg: %s)", tt.errCode, vErr.Code, vErr.Message)
				}
			} else {
				if err != nil {
					t.Fatalf("expected valid path for %q, got err: %v", tt.path, err)
				}
			}
		})
	}
}

func TestValidateCacheDir_MacOSLinuxPaths(t *testing.T) {
	home := "/Users/testuser"

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errCode string
	}{
		// 1. Filesystem root
		{"root dir", "/", true, ErrCodeRoot},

		// 2. Strict system directories
		{"/bin", "/bin", true, ErrCodeSystemDir},
		{"/sbin", "/sbin", true, ErrCodeSystemDir},
		{"/usr", "/usr", true, ErrCodeSystemDir},
		{"/usr/bin", "/usr/bin", true, ErrCodeSystemDir},
		{"/usr/local/bin", "/usr/local/bin", true, ErrCodeSystemDir},
		{"/etc", "/etc", true, ErrCodeSystemDir},
		{"/System", "/System", true, ErrCodeSystemDir},
		{"/System/Library", "/System/Library", true, ErrCodeSystemDir},
		{"/Library", "/Library", true, ErrCodeSystemDir},
		{"/Applications", "/Applications", true, ErrCodeSystemDir},

		// 3. System roots that reject exact match but ALLOW subdirectories
		{"/var exact", "/var", true, ErrCodeSystemDir},
		{"/private/var exact", "/private/var", true, ErrCodeSystemDir},
		{"/tmp exact", "/tmp", true, ErrCodeSystemDir},
		{"/private/tmp exact", "/private/tmp", true, ErrCodeSystemDir},

		// Subdirectories of /tmp and /var/folders (t.TempDir on macOS) MUST be allowed!
		{"/tmp/gbf_cache subdir", "/tmp/gbf_cache", false, ""},
		{"/private/tmp/gbf_cache subdir", "/private/tmp/gbf_cache", false, ""},
		{"macOS t.TempDir style /var/folders", "/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/test12345/cache", false, ""},
		{"macOS resolved /private/var/folders", "/private/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/test12345/cache", false, ""},

		// 4. Protected user directories
		{"User home /Users/testuser exact", "/Users/testuser", true, ErrCodeUserProtectedDir},
		{"Ancestor /Users", "/Users", true, ErrCodeAncestorProtected},
		{"Downloads exact", "/Users/testuser/Downloads", true, ErrCodeUserProtectedDir},
		{"Desktop exact", "/Users/testuser/Desktop", true, ErrCodeUserProtectedDir},
		{"Documents exact", "/Users/testuser/Documents", true, ErrCodeUserProtectedDir},

		// Allowed user subdirectories
		{"Downloads subdir ACGPower cache", "/Users/testuser/Downloads/ACGPower/cache", false, ""},
		{"User Library Application Support", "/Users/testuser/Library/Application Support/GBF-Accelerator/cache", false, ""},

		// "不要按前缀"
		{"Downloads_other prefix mismatch", "/Users/testuser/Downloads_other", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCacheDirForPlatform(tt.path, "darwin", home, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for path %q, got nil", tt.path)
				}
				vErr, ok := err.(*CacheDirValidationError)
				if !ok {
					t.Fatalf("expected *CacheDirValidationError, got %T: %v", err, err)
				}
				if tt.errCode != "" && vErr.Code != tt.errCode {
					t.Errorf("expected error code %s, got %s (msg: %s)", tt.errCode, vErr.Code, vErr.Message)
				}
			} else {
				if err != nil {
					t.Fatalf("expected valid path for %q, got err: %v", tt.path, err)
				}
			}
		})
	}
}

func TestValidateCacheDir_SymlinkAncestorFallback(t *testing.T) {
	tmpDir := t.TempDir()
	existingParent := filepath.Join(tmpDir, "existing_parent")
	if err := os.MkdirAll(existingParent, 0755); err != nil {
		t.Fatalf("failed to create existing parent: %v", err)
	}

	// Path with nonexistent child segments
	nonexistentChild := filepath.Join(existingParent, "nonexistent", "sub", "cache")
	resolved := resolveSymlinksWithAncestors(nonexistentChild)
	if !strings.HasPrefix(resolved, existingParent) {
		t.Errorf("expected resolved path to be based on %q, got %q", existingParent, resolved)
	}

	// Validating this path should succeed as it is a dedicated temp subdirectory
	if err := ValidateCacheDir(nonexistentChild); err != nil {
		t.Errorf("expected ValidateCacheDir to succeed for temp child dir, got: %v", err)
	}
}

func TestIsSystemJunk(t *testing.T) {
	junkFiles := []string{
		".DS_Store",
		"._foo.png",
		"._bar.js",
		".Spotlight-V100",
		".spotlight-v100",
		".fseventsd",
		".Trashes",
		"Thumbs.db",
		"thumbs.db",
		"desktop.ini",
		"DESKTOP.INI",
		"$RECYCLE.BIN",
		"$recycle.bin",
		"System Volume Information",
		"system volume information",
	}

	for _, name := range junkFiles {
		if !IsSystemJunk(name) {
			t.Errorf("expected %q to be recognized as system junk", name)
		}
	}

	validFiles := []string{
		"doc.txt",
		"notes.pdf",
		"btn.png",
		"app.js",
		"style.css",
		"data.csv",
		"Thumbs.db.bak",
	}

	for _, name := range validFiles {
		if IsSystemJunk(name) {
			t.Errorf("expected %q NOT to be recognized as system junk", name)
		}
	}
}

func TestIsDirClean(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty dir is clean
	clean, err := IsDirClean(tmpDir)
	if err != nil || !clean {
		t.Errorf("expected empty dir to be clean, got clean=%v, err=%v", clean, err)
	}

	// Dir with only system junk files is clean
	_ = os.WriteFile(filepath.Join(tmpDir, ".DS_Store"), []byte("junk"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "Thumbs.db"), []byte("junk"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "desktop.ini"), []byte("junk"), 0644)

	clean, err = IsDirClean(tmpDir)
	if err != nil || !clean {
		t.Errorf("expected dir with only system junk to be clean, got clean=%v, err=%v", clean, err)
	}

	// Adding a user file makes it NOT clean
	_ = os.WriteFile(filepath.Join(tmpDir, "user_document.txt"), []byte("hello"), 0644)
	clean, err = IsDirClean(tmpDir)
	if err != nil || clean {
		t.Errorf("expected dir with user file NOT to be clean, got clean=%v, err=%v", clean, err)
	}
}
