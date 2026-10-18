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
	expectedPrefix := fmt.Sprintf("https://github.com/%s/releases/download/%s/", AssetsRepo, ToolchainAssetsTag)
	if !strings.HasPrefix(url, expectedPrefix) {
		t.Errorf("unexpected primary URL: %s, expected prefix %s", url, expectedPrefix)
	}

	fallback := PlatformToolsGoogleFallbackURL()
	if !strings.HasPrefix(fallback, "https://dl.google.com/android/repository/platform-tools-latest-") {
		t.Errorf("unexpected fallback URL: %s", fallback)
	}
}

func TestBuildDeviceBrowserListIncludesTarouWhenInstalled(t *testing.T) {
	installed := []string{"com.watanuki.tarou", "mark.via.gp"}
	installedMap := make(map[string]bool, len(installed))
	for _, pkg := range installed {
		installedMap[pkg] = true
	}

	results := buildDeviceBrowserList(installedMap, installed)

	counts := make(map[string]int)
	var tarou *InstalledBrowserApp
	for i := range results {
		counts[results[i].PackageName]++
		if results[i].PackageName == "com.watanuki.tarou" {
			tarou = &results[i]
		}
	}

	if tarou == nil {
		t.Fatal("expected com.watanuki.tarou in the installed WebView app list")
	}
	if !tarou.IsInstalled || !tarou.IsSystemWebView || tarou.IsSkyLeap {
		t.Fatalf("unexpected Tarou classification: %+v", *tarou)
	}
	if counts["com.watanuki.tarou"] != 1 {
		t.Fatalf("expected Tarou exactly once, got %d", counts["com.watanuki.tarou"])
	}
	if counts["mark.via.gp"] != 1 {
		t.Fatalf("expected Via exactly once, got %d", counts["mark.via.gp"])
	}
}

func TestBuildDeviceBrowserListOmitsTarouWhenMissing(t *testing.T) {
	installed := []string{"mark.via.gp"}
	installedMap := map[string]bool{"mark.via.gp": true}

	results := buildDeviceBrowserList(installedMap, installed)

	var chrome *InstalledBrowserApp
	for i := range results {
		if results[i].PackageName == "com.watanuki.tarou" {
			t.Fatalf("Tarou must not be listed when not installed: %+v", results[i])
		}
		if results[i].PackageName == "com.android.chrome" {
			chrome = &results[i]
		}
	}

	if chrome == nil {
		t.Fatal("expected the not-installed Chrome entry to remain in the catalog")
	}
	if chrome.IsInstalled {
		t.Fatalf("expected Chrome to be marked as not installed: %+v", *chrome)
	}
}
