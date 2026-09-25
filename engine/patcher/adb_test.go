package patcher

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCleanAdbOutput(t *testing.T) {
	in := []byte("line1\r\nline2\r\nline3\r\n")
	cleaned := CleanAdbOutput(in)
	if strings.Contains(cleaned, "\r") {
		t.Errorf("CleanAdbOutput left carriage return: %q", cleaned)
	}
	expected := "line1\nline2\nline3"
	if cleaned != expected {
		t.Errorf("expected %q, got %q", expected, cleaned)
	}
}

func TestFindAdbPath(t *testing.T) {
	tempDir := t.TempDir()
	adbExe := "adb"
	if runtime.GOOS == "windows" {
		adbExe = "adb.exe"
	}

	fakeAdb := filepath.Join(tempDir, "platform-tools", adbExe)
	if err := os.MkdirAll(filepath.Dir(fakeAdb), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(fakeAdb, []byte("fake-adb"), 0755); err != nil {
		t.Fatalf("failed to create fake adb: %v", err)
	}

	found, err := FindAdb(tempDir, "")
	if err != nil {
		t.Fatalf("FindAdb failed: %v", err)
	}
	if found != fakeAdb {
		t.Errorf("expected %s, got %s", fakeAdb, found)
	}
}

func TestPlatformToolsDownloadURL(t *testing.T) {
	url := PlatformToolsDownloadURL()
	expectedPrefix := fmt.Sprintf("https://github.com/%s/releases/download/%s/", AssetsRepo, AssetsTag)
	if !strings.HasPrefix(url, expectedPrefix) {
		t.Errorf("unexpected primary URL: %s, expected prefix %s", url, expectedPrefix)
	}

	fallback := PlatformToolsGoogleFallbackURL()
	if !strings.HasPrefix(fallback, "https://dl.google.com/android/repository/platform-tools-latest-") {
		t.Errorf("unexpected fallback URL: %s", fallback)
	}
}
