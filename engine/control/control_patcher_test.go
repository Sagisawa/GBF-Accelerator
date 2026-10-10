package control

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/patcher"
	"gbf-proxy/telemetry"
)

type testControlServer struct {
	*ControlServer
}

func (s *testControlServer) handleRoute(w http.ResponseWriter, req *http.Request) {
	if req.RemoteAddr == "" || req.RemoteAddr == "192.0.2.1:1234" {
		req.RemoteAddr = "127.0.0.1:12345"
	}
	s.ControlServer.handleRoute(w, req)
}

func setupTestControlServer(t *testing.T) (*testControlServer, func()) {
	t.Helper()
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "config.json")
	cfgMgr := config.NewManager(cfgPath)
	cacheMgr := cache.NewManager(tempDir, 16)
	stats := telemetry.NewStats()

	ctrl := NewControlServer(cfgMgr, nil, cacheMgr, nil, stats)
	cleanup := func() {
		cacheMgr.Close()
	}
	return &testControlServer{ControlServer: ctrl}, cleanup
}

func TestControlAndroidEnv(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/android/env", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		OK      bool                   `json:"ok"`
		Ready   bool                   `json:"ready"`
		Java    map[string]interface{} `json:"java"`
		LSPatch map[string]interface{} `json:"lspatch"`
		Module  map[string]interface{} `json:"module"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !res.OK {
		t.Errorf("expected ok=true, got false")
	}
	if res.Java == nil || res.LSPatch == nil || res.Module == nil {
		t.Errorf("expected java, lspatch, and module objects in env response")
	}
}

func TestControlAndroidInspect(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Invalid JSON body
	reqBad := httptest.NewRequest(http.MethodPost, "/api/android/inspect", bytes.NewBufferString("not-json"))
	reqBad.Host = "127.0.0.1:8125"
	wBad := httptest.NewRecorder()
	ctrl.handleRoute(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", wBad.Code)
	}

	// 2. Empty file_path
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/android/inspect", bytes.NewBufferString(`{"file_path":""}`))
	reqEmpty.Host = "127.0.0.1:8125"
	wEmpty := httptest.NewRecorder()
	ctrl.handleRoute(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for empty path, got %d", wEmpty.Code)
	}

	// 3. Nonexistent file
	reqNonExist := httptest.NewRequest(http.MethodPost, "/api/android/inspect", bytes.NewBufferString(`{"file_path":"C:\\nonexistent\\package.apk"}`))
	reqNonExist.Host = "127.0.0.1:8125"
	wNonExist := httptest.NewRecorder()
	ctrl.handleRoute(wNonExist, reqNonExist)
	if wNonExist.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for nonexistent file, got %d", wNonExist.Code)
	}

	// 4. Directory instead of file
	tempDir := t.TempDir()
	reqDirBody, _ := json.Marshal(map[string]string{"file_path": tempDir})
	reqDir := httptest.NewRequest(http.MethodPost, "/api/android/inspect", bytes.NewBuffer(reqDirBody))
	reqDir.Host = "127.0.0.1:8125"
	wDir := httptest.NewRecorder()
	ctrl.handleRoute(wDir, reqDir)
	if wDir.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for directory, got %d", wDir.Code)
	}

	// 5. Inspect real sample base-patched.apk if present, verifying patch detection fields
	basePatchedPath := filepath.Join("..", "..", "bin", "output_patched", "base-patched.apk")
	if fi, err := os.Stat(basePatchedPath); err == nil && !fi.IsDir() {
		absPath, _ := filepath.Abs(basePatchedPath)
		reqValidBody, _ := json.Marshal(map[string]string{"file_path": absPath})
		reqValid := httptest.NewRequest(http.MethodPost, "/api/android/inspect", bytes.NewBuffer(reqValidBody))
		reqValid.Host = "127.0.0.1:8125"
		wValid := httptest.NewRecorder()
		ctrl.handleRoute(wValid, reqValid)
		if wValid.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for valid patched apk, got %d: %s", wValid.Code, wValid.Body.String())
		}
		var inspectResp struct {
			OK                  bool   `json:"ok"`
			IsAlreadyPatched    bool   `json:"is_already_patched"`
			PatchType           string `json:"patch_type"`
			OriginalVersionName string `json:"original_version_name"`
			PackageName         string `json:"package_name"`
		}
		if err := json.Unmarshal(wValid.Body.Bytes(), &inspectResp); err != nil {
			t.Fatalf("failed to decode inspect response: %v", err)
		}
		if !inspectResp.OK {
			t.Errorf("expected ok=true")
		}
		if !inspectResp.IsAlreadyPatched {
			t.Errorf("expected is_already_patched=true for base-patched.apk")
		}
		if inspectResp.PatchType != "lspatch" {
			t.Errorf("expected patch_type='lspatch', got %q", inspectResp.PatchType)
		}
	}
}

func TestControlAndroidUpload(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Non-multipart request
	reqBad := httptest.NewRequest(http.MethodPost, "/api/android/upload", bytes.NewBufferString("text data"))
	reqBad.Host = "127.0.0.1:8125"
	wBad := httptest.NewRecorder()
	ctrl.handleRoute(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-multipart, got %d", wBad.Code)
	}

	// 2. Disallowed file extension (.txt)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = part.Write([]byte("not an apk"))
	_ = writer.Close()

	reqExt := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
	reqExt.Host = "127.0.0.1:8125"
	reqExt.Header.Set("Content-Type", writer.FormDataContentType())
	wExt := httptest.NewRecorder()
	ctrl.handleRoute(wExt, reqExt)
	if wExt.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for .txt extension, got %d: %s", wExt.Code, wExt.Body.String())
	}
}

func TestControlAndroidPatch_ConcurrencyConflict(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Create dummy test file
	tempDir := t.TempDir()
	dummyFile := filepath.Join(tempDir, "test.apk")
	_ = os.WriteFile(dummyFile, []byte("fake"), 0644)

	// Simulate an active running patch job
	ctrl.androidPatchMu.Lock()
	ctrl.androidPatchStatus.Running = true
	ctrl.androidPatchMu.Unlock()

	reqBody, _ := json.Marshal(map[string]string{
		"file_path": dummyFile,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(reqBody))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict when job is running, got %d: %s", w.Code, w.Body.String())
	}
}

func TestControlAndroidPatchStatus(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	ctrl.androidPatchMu.Lock()
	ctrl.androidPatchStatus = AndroidPatchStatus{
		Running:   true,
		Stage:     2,
		StageText: "Inspecting input package...",
		Progress:  0.4,
		Logs:      []string{"log 1", "log 2"},
		Error:     "",
		Done:      false,
	}
	ctrl.androidPatchMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/android/patch/status", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var status AndroidPatchStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to parse json status: %v", err)
	}

	if !status.Running || status.Stage != 2 || status.Progress != 0.4 || len(status.Logs) != 2 {
		t.Errorf("unexpected status content: %+v", status)
	}
}

func TestResolveAndroidPatchOutputDir(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "Application Support", "GBF-Accelerator")

	absCustomPath, err := filepath.Abs(filepath.Join(string(filepath.Separator), "tmp", "output_patched"))
	if err != nil {
		t.Fatalf("failed to resolve absolute test path: %v", err)
	}

	tests := []struct {
		name     string
		rawPath  string
		platform string
		want     string
	}{
		{
			name:     "macOS relative path uses writable base dir",
			rawPath:  "output_patched",
			platform: "darwin",
			want:     filepath.Join(baseDir, "output_patched"),
		},
		{
			name:     "macOS empty path uses writable default",
			rawPath:  "",
			platform: "darwin",
			want:     filepath.Join(baseDir, "output_patched"),
		},
		{
			name:     "macOS absolute path remains unchanged",
			rawPath:  absCustomPath,
			platform: "darwin",
			want:     absCustomPath,
		},
		{
			name:     "Windows relative path resolves under base dir",
			rawPath:  "output_patched",
			platform: "windows",
			want:     filepath.Join(baseDir, "output_patched"),
		},
		{
			name:     "Windows empty path resolves to default under base dir",
			rawPath:  "",
			platform: "windows",
			want:     filepath.Join(baseDir, "output_patched"),
		},
		{
			name:     "Linux relative path resolves under base dir",
			rawPath:  "output_patched",
			platform: "linux",
			want:     filepath.Join(baseDir, "output_patched"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAndroidPatchOutputDirFor(tt.rawPath, tt.platform, baseDir)
			want, err := filepath.Abs(tt.want)
			if err != nil {
				t.Fatalf("failed to resolve expected path: %v", err)
			}
			if got != want {
				t.Fatalf("expected %q, got %q", want, got)
			}
		})
	}
}

func TestControlAndroidPatchOpenOutput(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	tempDir := t.TempDir()
	reqBody, _ := json.Marshal(map[string]string{
		"path": tempDir,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/android/patch/open-output", bytes.NewBuffer(reqBody))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}
}

func TestControlAndroidUploadAndInspect(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Check for real base.apk in build/skyleap_splits/original/base.apk
	repoRoot := filepath.Join("..", "..")
	realBaseApk := filepath.Join(repoRoot, "build", "skyleap_splits", "original", "base.apk")

	if baseData, err := os.ReadFile(realBaseApk); err == nil {
		// Test upload of real APK
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "base.apk")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		_, _ = part.Write(baseData)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
		req.Host = "127.0.0.1:8125"
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for base.apk upload, got %d: %s", w.Code, w.Body.String())
		}

		var res map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if res["ok"] != true {
			t.Fatalf("expected ok=true, got %+v", res)
		}
		if res["package_name"] != "com.dena.skyleap" {
			t.Errorf("expected com.dena.skyleap, got %v", res["package_name"])
		}
		if res["is_official_skyleap"] != true {
			t.Errorf("expected is_official_skyleap=true")
		}
	} else {
		// Without real APK, verify that uploading empty zip is rejected with 400
		tempDir := t.TempDir()
		dummyApk := filepath.Join(tempDir, "empty.apk")
		f, _ := os.Create(dummyApk)
		zw := zip.NewWriter(f)
		_ = zw.Close()
		_ = f.Close()

		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "empty.apk")
		data, _ := os.ReadFile(dummyApk)
		_, _ = part.Write(data)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
		req.Host = "127.0.0.1:8125"
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty apk, got %d", w.Code)
		}
	}
}

func TestControlAndroidPatch_MissingComponentsRejected(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Create dummy test file
	tempDir := t.TempDir()
	dummyFile := filepath.Join(tempDir, "test.apk")
	_ = os.WriteFile(dummyFile, []byte("fake"), 0644)

	reqBody, _ := json.Marshal(map[string]string{
		"file_path": dummyFile,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(reqBody))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	// Since components are not installed in the test environment, patch execution MUST be rejected
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when components are missing, got %d: %s", w.Code, w.Body.String())
	}
	var res map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res["ok"] != false {
		t.Errorf("expected ok=false")
	}
	errStr, _ := res["error"].(string)
	if !strings.Contains(errStr, "Android Patch 组件尚未就绪") {
		t.Errorf("expected error message mentioning missing components, got: %s", errStr)
	}
}

func TestControlAndroidComponentsDownload_Endpoints(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Initial status when idle
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/android/components/download-status", nil)
	reqStatus.Host = "127.0.0.1:8125"
	wStatus := httptest.NewRecorder()
	ctrl.handleRoute(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for download-status, got %d", wStatus.Code)
	}
	var statusRes map[string]interface{}
	_ = json.Unmarshal(wStatus.Body.Bytes(), &statusRes)
	if statusRes["active"] != false {
		t.Errorf("expected active=false initially")
	}

	// 2. Cancel when idle
	reqCancelIdle := httptest.NewRequest(http.MethodPost, "/api/android/components/download-cancel", nil)
	reqCancelIdle.Host = "127.0.0.1:8125"
	wCancelIdle := httptest.NewRecorder()
	ctrl.handleRoute(wCancelIdle, reqCancelIdle)

	if wCancelIdle.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for cancel when idle, got %d", wCancelIdle.Code)
	}

	// 3. Concurrency conflict test (simulate active download)
	ctrl.componentDlMu.Lock()
	ctrl.componentDlActive = true
	ctrl.componentDlCancelFn = func() {}
	ctrl.componentDlMu.Unlock()

	reqStart := httptest.NewRequest(http.MethodPost, "/api/android/components/download", bytes.NewBuffer([]byte("{}")))
	reqStart.Host = "127.0.0.1:8125"
	wStart := httptest.NewRecorder()
	ctrl.handleRoute(wStart, reqStart)

	if wStart.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict during active download, got %d: %s", wStart.Code, wStart.Body.String())
	}

	// 4. Cancel active download
	reqCancelActive := httptest.NewRequest(http.MethodPost, "/api/android/components/download-cancel", nil)
	reqCancelActive.Host = "127.0.0.1:8125"
	wCancelActive := httptest.NewRecorder()
	ctrl.handleRoute(wCancelActive, reqCancelActive)

	if wCancelActive.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for cancel, got %d", wCancelActive.Code)
	}
}

func TestControlAndroidAdbEndpoints(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. GET /api/android/adb/devices
	reqDevs := httptest.NewRequest(http.MethodGet, "/api/android/adb/devices", nil)
	reqDevs.Host = "127.0.0.1:8125"
	wDevs := httptest.NewRecorder()
	ctrl.handleRoute(wDevs, reqDevs)

	if wDevs.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for adb devices, got %d: %s", wDevs.Code, wDevs.Body.String())
	}

	var resDevs struct {
		OK       bool                `json:"ok"`
		AdbFound bool                `json:"adb_found"`
		Devices  []patcher.AdbDevice `json:"devices"`
	}
	if err := json.Unmarshal(wDevs.Body.Bytes(), &resDevs); err != nil {
		t.Fatalf("failed to decode devices response: %v", err)
	}
	if !resDevs.OK {
		t.Errorf("expected ok=true, got false")
	}

	// 2. POST /api/android/adb/install with no patch result
	reqInstall := httptest.NewRequest(http.MethodPost, "/api/android/adb/install", bytes.NewBuffer([]byte(`{"serial":"fake123"}`)))
	reqInstall.Host = "127.0.0.1:8125"
	wInstall := httptest.NewRecorder()
	ctrl.handleRoute(wInstall, reqInstall)

	if wInstall.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when no patch result, got %d: %s", wInstall.Code, wInstall.Body.String())
	}
}

func TestControlAndroidPatchStatusSerialization(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	ctrl.androidPatchMu.Lock()
	ctrl.androidPatchStatus.Done = true
	ctrl.androidPatchStatus.Running = false
	ctrl.androidPatchStatus.Result = &patcher.BundleResult{
		IsSplit:      true,
		SingleApk:    "",
		SplitDir:     "C:\\path\\to\\splits",
		ApksArchive:  "C:\\path\\to\\skyleap-patched.apks",
		TotalApks:    5,
		TotalBytes:   1024 * 1024 * 110,
		PackageFiles: []string{"base.apk", "split_config.arm64_v8a.apk"},
	}
	ctrl.androidPatchMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/android/patch/status", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &rawMap); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	resObj, ok := rawMap["result"].(map[string]interface{})
	if !ok || resObj == nil {
		t.Fatalf("expected result object in json response, got: %v", rawMap["result"])
	}

	// Verify exact snake_case fields expected by frontend
	if isSplit, ok := resObj["is_split"].(bool); !ok || !isSplit {
		t.Errorf("expected is_split=true in json response, got: %v", resObj["is_split"])
	}
	if totalBytes, ok := resObj["total_bytes"].(float64); !ok || totalBytes <= 0 {
		t.Errorf("expected numeric total_bytes > 0, got: %v", resObj["total_bytes"])
	}
	if totalApks, ok := resObj["total_apks"].(float64); !ok || totalApks != 5 {
		t.Errorf("expected total_apks=5, got: %v", resObj["total_apks"])
	}
	if apksArchive, ok := resObj["apks_archive"].(string); !ok || !strings.Contains(apksArchive, "skyleap-patched.apks") {
		t.Errorf("expected apks_archive to contain 'skyleap-patched.apks', got: %v", resObj["apks_archive"])
	}
}

func TestControlAndroidAdbInstallHostApp(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Invalid JSON body
	reqBad := httptest.NewRequest(http.MethodPost, "/api/android/adb/install-host-app", bytes.NewBufferString("not-json"))
	reqBad.Host = "127.0.0.1:8125"
	wBad := httptest.NewRecorder()
	ctrl.handleRoute(wBad, reqBad)
	if wBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for bad json, got %d", wBad.Code)
	}

	// 2. Empty serial
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/android/adb/install-host-app", bytes.NewBufferString(`{"serial":""}`))
	reqEmpty.Host = "127.0.0.1:8125"
	wEmpty := httptest.NewRecorder()
	ctrl.handleRoute(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for empty serial, got %d", wEmpty.Code)
	}
}

func TestControlAndroidPatchOpenBackup(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/android/patch/open-backup", nil)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !res.OK {
		t.Errorf("expected ok=true, got false")
	}
	if res.Path == "" {
		t.Errorf("expected non-empty path in response")
	}
}

func TestControlAndroidEnvInstallAndUninstallAll(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Conflict when active
	ctrl.componentDlMu.Lock()
	ctrl.componentDlActive = true
	ctrl.componentDlMu.Unlock()

	reqInstallConflict := httptest.NewRequest(http.MethodPost, "/api/android/env/install-all", nil)
	reqInstallConflict.Host = "127.0.0.1:8125"
	wInstallConflict := httptest.NewRecorder()
	ctrl.handleRoute(wInstallConflict, reqInstallConflict)
	if wInstallConflict.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict when active, got %d", wInstallConflict.Code)
	}

	reqUninstallConflict := httptest.NewRequest(http.MethodPost, "/api/android/env/uninstall-all", nil)
	reqUninstallConflict.Host = "127.0.0.1:8125"
	wUninstallConflict := httptest.NewRecorder()
	ctrl.handleRoute(wUninstallConflict, reqUninstallConflict)
	if wUninstallConflict.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict when active, got %d", wUninstallConflict.Code)
	}

	// Reset active
	ctrl.componentDlMu.Lock()
	ctrl.componentDlActive = false
	ctrl.componentDlMu.Unlock()

	// Uninstall when inactive
	reqUninstall := httptest.NewRequest(http.MethodPost, "/api/android/env/uninstall-all", nil)
	reqUninstall.Host = "127.0.0.1:8125"
	wUninstall := httptest.NewRecorder()
	ctrl.handleRoute(wUninstall, reqUninstall)
	if wUninstall.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for uninstall, got %d: %s", wUninstall.Code, wUninstall.Body.String())
	}
	var uninstRes struct {
		OK         bool   `json:"ok"`
		FreedFiles int    `json:"freed_files"`
		FreedBytes int64  `json:"freed_bytes"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(wUninstall.Body.Bytes(), &uninstRes); err != nil {
		t.Fatalf("failed to decode uninstall response: %v", err)
	}
	if !uninstRes.OK {
		t.Errorf("expected ok=true for uninstall")
	}
}

func TestControlAndroidEnvInstallAll_LifecycleAndRetry(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Initial status has active=false
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/android/components/download-status", nil)
	reqStatus.Host = "127.0.0.1:8125"
	wStatus := httptest.NewRecorder()
	ctrl.handleRoute(wStatus, reqStatus)
	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wStatus.Code)
	}

	// 2. Set an error state simulating failed download
	ctrl.componentDlMu.Lock()
	ctrl.componentDlActive = false
	ctrl.componentDlProgress = patcher.DownloadProgress{
		Active:          false,
		Done:            false,
		Stage:           "error",
		Error:           patcher.ErrPermissionUserNotice,
		ErrorDetails:    "mkdir tools/android: Access is denied",
		IsPermissionErr: true,
	}
	ctrl.componentDlMu.Unlock()

	// 3. Verify /api/android/env reflects the error and permission flag
	reqEnv := httptest.NewRequest(http.MethodGet, "/api/android/env", nil)
	reqEnv.Host = "127.0.0.1:8125"
	wEnv := httptest.NewRecorder()
	ctrl.handleRoute(wEnv, reqEnv)
	if wEnv.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", wEnv.Code)
	}
	var envRes struct {
		OK       bool `json:"ok"`
		Download struct {
			Active          bool   `json:"active"`
			Stage           string `json:"stage"`
			Error           string `json:"error"`
			ErrorDetails    string `json:"error_details"`
			IsPermissionErr bool   `json:"is_permission_error"`
		} `json:"download"`
	}
	if err := json.Unmarshal(wEnv.Body.Bytes(), &envRes); err != nil {
		t.Fatalf("failed to decode env response: %v", err)
	}
	if envRes.Download.Stage != "error" {
		t.Errorf("expected download stage=error, got %s", envRes.Download.Stage)
	}
	if !envRes.Download.IsPermissionErr {
		t.Errorf("expected is_permission_error=true")
	}
	if envRes.Download.Error != patcher.ErrPermissionUserNotice {
		t.Errorf("expected permission notice, got %s", envRes.Download.Error)
	}
	if envRes.Download.ErrorDetails != "mkdir tools/android: Access is denied" {
		t.Errorf("expected error details preserved, got %s", envRes.Download.ErrorDetails)
	}

	// 4. Verify user can retry install-all without conflict
	// In writable test environment, install-all should accept and start
	reqRetry := httptest.NewRequest(http.MethodPost, "/api/android/env/install-all", nil)
	reqRetry.Host = "127.0.0.1:8125"
	wRetry := httptest.NewRecorder()
	ctrl.handleRoute(wRetry, reqRetry)
	// Cancel immediately so background goroutine does not do unnecessary network I/O
	ctrl.componentDlMu.Lock()
	if ctrl.componentDlCancelFn != nil {
		ctrl.componentDlCancelFn()
	}
	ctrl.componentDlMu.Unlock()

	if wRetry.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for retry install-all, got %d: %s", wRetry.Code, wRetry.Body.String())
	}
}

func TestControlAndroidEnvInstallAll_ConcurrentRequests_OnlyOneWins(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Set componentDlActive = true to simulate an in-flight active download
	ctrl.componentDlMu.Lock()
	ctrl.componentDlActive = true
	ctrl.componentDlMu.Unlock()

	// 2. Launch concurrent requests while componentDlActive = true
	const concurrentCount = 10
	results := make(chan int, concurrentCount)
	startBarrier := make(chan struct{})

	for i := 0; i < concurrentCount; i++ {
		go func() {
			<-startBarrier
			req := httptest.NewRequest(http.MethodPost, "/api/android/env/install-all", nil)
			req.Host = "127.0.0.1:8125"
			w := httptest.NewRecorder()
			ctrl.handleRoute(w, req)
			results <- w.Code
		}()
	}

	close(startBarrier)

	for i := 0; i < concurrentCount; i++ {
		code := <-results
		if code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict when download is active, got %d", code)
		}
	}

	// 3. Reset active and verify clean request succeeds with 200 OK
	ctrl.componentDlMu.Lock()
	ctrl.componentDlActive = false
	ctrl.componentDlMu.Unlock()

	reqOK := httptest.NewRequest(http.MethodPost, "/api/android/env/install-all", nil)
	reqOK.Host = "127.0.0.1:8125"
	wOK := httptest.NewRecorder()
	ctrl.handleRoute(wOK, reqOK)

	ctrl.componentDlMu.Lock()
	if ctrl.componentDlCancelFn != nil {
		ctrl.componentDlCancelFn()
	}
	ctrl.componentDlActive = false
	ctrl.componentDlMu.Unlock()

	if wOK.Code != http.StatusOK {
		t.Fatalf("expected 200 OK after download became inactive, got %d", wOK.Code)
	}
}

func TestControlAndroidAdbDownloadTools_PreFlight(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	req := httptest.NewRequest(http.MethodPost, "/api/android/adb/download-tools", nil).WithContext(ctx)
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	// In writable test environment, pre-flight should succeed and not return 403 Forbidden
	if w.Code == http.StatusForbidden {
		t.Errorf("did not expect 403 Forbidden in writable test directory, got: %s", w.Body.String())
	}
}

func TestControlAndroidAdb_MaliciousPackageNameRejected(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	maliciousPkgs := []string{
		"com.dena.skyleap; rm -rf /",
		"../../evil",
		"com.dena.skyleap$(whoami)",
		"com.dena.skyleap`id`",
		"com.dena.skyleap | cat",
		"invalid package name",
		"com.dena.skyleap\nreboot",
		".leadingdot",
		"trailingdot.",
		"two..dots",
		"com.-hyphenstart",
	}

	endpoints := []string{
		"/api/android/adb/probe-app",
		"/api/android/adb/extract",
		"/api/android/adb/install",
	}

	for _, ep := range endpoints {
		for _, badPkg := range maliciousPkgs {
			body, _ := json.Marshal(map[string]interface{}{
				"serial":       "emulator-5554",
				"package_name": badPkg,
			})
			req := httptest.NewRequest(http.MethodPost, ep, bytes.NewBuffer(body))
			req.Host = "127.0.0.1:8125"
			w := httptest.NewRecorder()
			ctrl.handleRoute(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("[%s] expected 400 Bad Request for malicious package %q, got %d: %s", ep, badPkg, w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["ok"] == true {
				t.Errorf("[%s] expected ok=false for malicious package %q", ep, badPkg)
			}
		}
	}
}

func TestControlAndroidPatch_CancelAndStatusRecovery(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Cancel when no patch job is running
	reqCancelIdle := httptest.NewRequest(http.MethodPost, "/api/android/patch/cancel", nil)
	reqCancelIdle.Host = "127.0.0.1:8125"
	wCancelIdle := httptest.NewRecorder()
	ctrl.handleRoute(wCancelIdle, reqCancelIdle)
	if wCancelIdle.Code != http.StatusOK {
		t.Fatalf("expected 200 OK when cancelling idle patcher, got %d", wCancelIdle.Code)
	}

	// 2. Simulate an active running patch job with cancelFn
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctrl.androidPatchMu.Lock()
	ctrl.androidPatchStatus = AndroidPatchStatus{
		Running:   true,
		Stage:     3,
		StageText: "Executing LSPatch injection...",
		Progress:  0.6,
		Logs:      []string{"patching..."},
	}
	ctrl.androidPatchCancelFn = cancel
	ctrl.androidPatchMu.Unlock()

	// 3. Verify status shows running
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/android/patch/status", nil)
	reqStatus.Host = "127.0.0.1:8125"
	wStatus := httptest.NewRecorder()
	ctrl.handleRoute(wStatus, reqStatus)
	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for status, got %d", wStatus.Code)
	}
	var statusResp struct {
		Running bool `json:"running"`
	}
	_ = json.Unmarshal(wStatus.Body.Bytes(), &statusResp)
	if !statusResp.Running {
		t.Errorf("expected running=true")
	}

	// 4. Send cancel request
	reqCancel := httptest.NewRequest(http.MethodPost, "/api/android/patch/cancel", nil)
	reqCancel.Host = "127.0.0.1:8125"
	wCancel := httptest.NewRecorder()
	ctrl.handleRoute(wCancel, reqCancel)
	if wCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for cancel, got %d: %s", wCancel.Code, wCancel.Body.String())
	}

	// Verify context was cancelled
	if ctx.Err() != context.Canceled {
		t.Errorf("expected cancelFn to be invoked, ctx.Err() = %v", ctx.Err())
	}

	// 5. Simulate goroutine recovering state on cancellation
	ctrl.androidPatchMu.Lock()
	ctrl.androidPatchStatus.Running = false
	ctrl.androidPatchStatus.Done = true
	ctrl.androidPatchStatus.Error = "Patch 任务已被用户取消"
	ctrl.androidPatchCancelFn = nil
	ctrl.androidPatchMu.Unlock()

	// Verify status after recovery
	wStatusAfter := httptest.NewRecorder()
	ctrl.handleRoute(wStatusAfter, reqStatus)
	var statusAfterResp struct {
		Running bool   `json:"running"`
		Done    bool   `json:"done"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(wStatusAfter.Body.Bytes(), &statusAfterResp)
	if statusAfterResp.Running {
		t.Errorf("expected running=false after cancellation")
	}
	if !statusAfterResp.Done {
		t.Errorf("expected done=true after cancellation")
	}
	if !strings.Contains(statusAfterResp.Error, "取消") {
		t.Errorf("expected cancellation error message, got %q", statusAfterResp.Error)
	}
}

func TestControlAndroidPatch_CancelRealBackgroundTask(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Write a valid test APK file
	apkBytes := buildMinimalValidApkBytes()
	tempApk := filepath.Join(t.TempDir(), "skyleap_valid.apk")
	if err := os.WriteFile(tempApk, apkBytes, 0644); err != nil {
		t.Fatalf("failed to write temp apk: %v", err)
	}

	ctrl.checkComponentsFn = func(toolsDir, exeDir string) (bool, bool, []patcher.ComponentStatus, string) {
		return true, true, nil, ""
	}

	jobStarted := make(chan struct{})
	jobExited := make(chan struct{})

	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		close(jobStarted)
		defer close(jobExited)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	// 1. Start the patch job via real API
	body, _ := json.Marshal(map[string]interface{}{
		"file_path": tempApk,
	})
	reqPatch := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body))
	reqPatch.Host = "127.0.0.1:8125"
	wPatch := httptest.NewRecorder()
	ctrl.handleRoute(wPatch, reqPatch)

	if wPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting patch job, got %d: %s", wPatch.Code, wPatch.Body.String())
	}

	// Wait for goroutine to actually start and execute
	select {
	case <-jobStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for patch job to start")
	}

	// 2. Query status: should be running
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/android/patch/status", nil)
	reqStatus.Host = "127.0.0.1:8125"
	wStatus := httptest.NewRecorder()
	ctrl.handleRoute(wStatus, reqStatus)
	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for status, got %d", wStatus.Code)
	}
	var statusRunning struct {
		Running bool `json:"running"`
		Done    bool `json:"done"`
	}
	_ = json.Unmarshal(wStatus.Body.Bytes(), &statusRunning)
	if !statusRunning.Running || statusRunning.Done {
		t.Fatalf("expected running=true, done=false, got %+v", statusRunning)
	}

	// 3. Send cancel request via real cancel endpoint
	reqCancel := httptest.NewRequest(http.MethodPost, "/api/android/patch/cancel", nil)
	reqCancel.Host = "127.0.0.1:8125"
	wCancel := httptest.NewRecorder()
	ctrl.handleRoute(wCancel, reqCancel)
	if wCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK cancelling patch job, got %d: %s", wCancel.Code, wCancel.Body.String())
	}

	// 4. Verify background goroutine exits naturally
	select {
	case <-jobExited:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for background goroutine to exit naturally after cancellation")
	}

	// 5. Query status: verify natural exit and state recovery
	deadline := time.Now().Add(2 * time.Second)
	var finalStatus struct {
		Running bool   `json:"running"`
		Done    bool   `json:"done"`
		Error   string `json:"error"`
	}
	for time.Now().Before(deadline) {
		wStatusFinal := httptest.NewRecorder()
		ctrl.handleRoute(wStatusFinal, reqStatus)
		_ = json.Unmarshal(wStatusFinal.Body.Bytes(), &finalStatus)
		if !finalStatus.Running && finalStatus.Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if finalStatus.Running || !finalStatus.Done {
		t.Errorf("expected job to recover to running=false, done=true, got: %+v", finalStatus)
	}
	if !strings.Contains(finalStatus.Error, "取消") {
		t.Errorf("expected cancellation error message, got %q", finalStatus.Error)
	}

	// Verify cancel handle was cleared
	ctrl.androidPatchMu.Lock()
	cancelFnNil := (ctrl.androidPatchCancelFn == nil)
	ctrl.androidPatchMu.Unlock()
	if !cancelFnNil {
		t.Errorf("expected androidPatchCancelFn to be cleared (nil) after job exit")
	}

	// 6. Verify that a subsequent patch job can be started cleanly
	jobStarted2 := make(chan struct{})
	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		close(jobStarted2)
		return &patcher.BundleResult{SingleApk: "skyleap-patched.apk"}, nil
	}
	wPatch2 := httptest.NewRecorder()
	reqPatch2 := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body))
	reqPatch2.Host = "127.0.0.1:8125"
	ctrl.handleRoute(wPatch2, reqPatch2)
	if wPatch2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting 2nd patch job, got %d: %s", wPatch2.Code, wPatch2.Body.String())
	}
	select {
	case <-jobStarted2:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for 2nd patch job to start")
	}
}

func TestControlAndroidPatch_CancelHandleRaceRegression(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	apkBytes := buildMinimalValidApkBytes()
	tempApk := filepath.Join(t.TempDir(), "skyleap_race.apk")
	if err := os.WriteFile(tempApk, apkBytes, 0644); err != nil {
		t.Fatalf("failed to write temp apk: %v", err)
	}

	ctrl.checkComponentsFn = func(toolsDir, exeDir string) (bool, bool, []patcher.ComponentStatus, string) {
		return true, true, nil, ""
	}

	job1Started := make(chan struct{})
	job1CanFinish := make(chan struct{})
	job1Done := make(chan struct{})

	job2Started := make(chan struct{})
	job2Exited := make(chan struct{})

	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		ctrl.androidPatchMu.Lock()
		currID := ctrl.androidPatchJobID
		ctrl.androidPatchMu.Unlock()

		if currID == 1 {
			close(job1Started)
			<-job1CanFinish
			defer close(job1Done)
			return &patcher.BundleResult{SingleApk: "job1.apk"}, nil
		}

		close(job2Started)
		defer close(job2Exited)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	// 1. Start Job 1 via real HTTP API
	body, _ := json.Marshal(map[string]interface{}{
		"file_path": tempApk,
	})
	reqPatch1 := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body))
	reqPatch1.Host = "127.0.0.1:8125"
	wPatch1 := httptest.NewRecorder()
	ctrl.handleRoute(wPatch1, reqPatch1)
	if wPatch1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting Job 1, got %d: %s", wPatch1.Code, wPatch1.Body.String())
	}

	select {
	case <-job1Started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Job 1 to start")
	}

	// Verify Job 1 state
	ctrl.androidPatchMu.Lock()
	if ctrl.androidPatchJobID != 1 || !ctrl.androidPatchStatus.Running || ctrl.androidPatchCancelFn == nil {
		ctrl.androidPatchMu.Unlock()
		t.Fatal("Job 1 not properly initialized")
	}
	ctrl.androidPatchMu.Unlock()

	bridge1 := &patchListenerBridge{server: ctrl.ControlServer, jobID: 1}

	// 2. Simulate Job 2 being permitted to start (e.g. if Running was reset or during state transition)
	ctrl.androidPatchMu.Lock()
	ctrl.androidPatchStatus.Running = false
	ctrl.androidPatchMu.Unlock()

	// Start Job 2 via real HTTP API
	reqPatch2 := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body))
	reqPatch2.Host = "127.0.0.1:8125"
	wPatch2 := httptest.NewRecorder()
	ctrl.handleRoute(wPatch2, reqPatch2)
	if wPatch2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting Job 2, got %d: %s", wPatch2.Code, wPatch2.Body.String())
	}

	select {
	case <-job2Started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Job 2 to start")
	}

	ctrl.androidPatchMu.Lock()
	if ctrl.androidPatchJobID != 2 || !ctrl.androidPatchStatus.Running || ctrl.androidPatchCancelFn == nil {
		ctrl.androidPatchMu.Unlock()
		t.Fatal("Job 2 not properly initialized")
	}
	ctrl.androidPatchMu.Unlock()

	// 3. Now release Job 1's background goroutine to run its delayed exit cleanup!
	close(job1CanFinish)
	select {
	case <-job1Done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Job 1 to finish")
	}

	// Give a moment for Job 1's defer to execute
	time.Sleep(50 * time.Millisecond)

	// 4. VERIFY: Job 1's delayed exit MUST NOT clear Job 2's cancelFn or status!
	ctrl.androidPatchMu.Lock()
	curCancelFn := ctrl.androidPatchCancelFn
	curRunning := ctrl.androidPatchStatus.Running
	curJobID := ctrl.androidPatchJobID
	ctrl.androidPatchMu.Unlock()

	if curJobID != 2 {
		t.Fatalf("expected active job ID 2, got %d", curJobID)
	}
	if !curRunning {
		t.Fatal("REGRESSION: Job 1's exit wiped out Job 2's Running status!")
	}
	if curCancelFn == nil {
		t.Fatal("REGRESSION: Job 1's exit wiped out Job 2's androidPatchCancelFn!")
	}

	// 5. Verify Job 1's bridge callbacks cannot pollute Job 2
	bridge1.OnStage(9, "Job 1 stale stage", 0.99)
	bridge1.OnLog("Job 1 stale log")

	ctrl.androidPatchMu.Lock()
	curStage := ctrl.androidPatchStatus.Stage
	curLogs := ctrl.androidPatchStatus.Logs
	ctrl.androidPatchMu.Unlock()

	if curStage == 9 {
		t.Errorf("Job 1 bridge polluted Job 2 stage: got 9")
	}
	for _, l := range curLogs {
		if strings.Contains(l, "Job 1 stale log") {
			t.Errorf("Job 1 bridge polluted Job 2 logs: %s", l)
		}
	}

	// 6. Verify Job 2 can be cancelled via real HTTP cancel endpoint
	reqCancel := httptest.NewRequest(http.MethodPost, "/api/android/patch/cancel", nil)
	reqCancel.Host = "127.0.0.1:8125"
	wCancel := httptest.NewRecorder()
	ctrl.handleRoute(wCancel, reqCancel)
	if wCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK cancelling Job 2, got %d: %s", wCancel.Code, wCancel.Body.String())
	}

	select {
	case <-job2Exited:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Job 2 to exit after cancellation")
	}

	time.Sleep(50 * time.Millisecond)

	ctrl.androidPatchMu.Lock()
	finalRunning := ctrl.androidPatchStatus.Running
	finalDone := ctrl.androidPatchStatus.Done
	finalCancelFn := ctrl.androidPatchCancelFn
	ctrl.androidPatchMu.Unlock()

	if finalRunning || !finalDone {
		t.Errorf("expected Job 2 to finish with running=false, done=true, got running=%v, done=%v", finalRunning, finalDone)
	}
	if finalCancelFn != nil {
		t.Errorf("expected Job 2's cancelFn to be cleared after exit")
	}
}

func TestControlAndroidPatch_CancelHandleRaceConcurrent(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// High concurrency stress test across goroutines to ensure no race conditions
	// between status queries, cancellations, and state updates.
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Query status
					reqStatus := httptest.NewRequest(http.MethodGet, "/api/android/patch/status", nil)
					reqStatus.Host = "127.0.0.1:8125"
					wStatus := httptest.NewRecorder()
					ctrl.handleRoute(wStatus, reqStatus)

					// Attempt cancel
					reqCancel := httptest.NewRequest(http.MethodPost, "/api/android/patch/cancel", nil)
					reqCancel.Host = "127.0.0.1:8125"
					wCancel := httptest.NewRecorder()
					ctrl.handleRoute(wCancel, reqCancel)
				}
			}
		}(i)
	}

	// Concurrently simulate jobs starting and stopping
	for j := 0; j < 50; j++ {
		_, jobCancel := context.WithCancel(context.Background())
		ctrl.androidPatchMu.Lock()
		ctrl.androidPatchJobID++
		currJob := ctrl.androidPatchJobID
		ctrl.androidPatchStatus = AndroidPatchStatus{
			Running:   true,
			Stage:     j % 5,
			StageText: fmt.Sprintf("Job %d", j),
			Progress:  float64(j) / 50.0,
		}
		ctrl.androidPatchCancelFn = jobCancel
		ctrl.androidPatchMu.Unlock()

		bridge := &patchListenerBridge{server: ctrl.ControlServer, jobID: currJob}
		bridge.OnLog(fmt.Sprintf("log from job %d", j))
		bridge.OnStage(j%5, fmt.Sprintf("stage %d", j), 0.5)

		time.Sleep(2 * time.Millisecond)

		ctrl.androidPatchMu.Lock()
		if ctrl.androidPatchJobID == currJob {
			ctrl.androidPatchStatus.Running = false
			ctrl.androidPatchStatus.Done = true
			ctrl.androidPatchCancelFn = nil
		}
		ctrl.androidPatchMu.Unlock()
		jobCancel()
	}

	cancel()
	wg.Wait()
}

func TestControlAndroidUpload_FailureCleanup(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Count existing gbf_upload_* dirs in temp directory before upload
	tempBase := os.TempDir()
	countUploadDirs := func() int {
		entries, _ := os.ReadDir(tempBase)
		cnt := 0
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "gbf_upload_") {
				cnt++
			}
		}
		return cnt
	}

	beforeCount := countUploadDirs()

	// Upload invalid non-APK content disguised as .apk
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "corrupt.apk")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = part.Write([]byte("this is not a valid zip or apk file"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
	req.Host = "127.0.0.1:8125"
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for corrupted package, got %d: %s", w.Code, w.Body.String())
	}

	afterCount := countUploadDirs()
	if afterCount > beforeCount {
		t.Errorf("temporary upload directory was leaked on failure: before=%d, after=%d", beforeCount, afterCount)
	}
}

func TestControlAndroidAdbExtract_FailureCleanup(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	tempBase := os.TempDir()
	countExtractDirs := func() int {
		entries, _ := os.ReadDir(tempBase)
		cnt := 0
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "gbf_extracted_") {
				cnt++
			}
		}
		return cnt
	}

	beforeCount := countExtractDirs()

	body, _ := json.Marshal(map[string]interface{}{
		"serial":       "fake-device-1234",
		"package_name": "com.dena.skyleap",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/android/adb/extract", bytes.NewBuffer(body))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	afterCount := countExtractDirs()
	if afterCount > beforeCount {
		t.Errorf("temporary extract directory was leaked: before=%d, after=%d", beforeCount, afterCount)
	}
}

func buildMinimalValidApkBytes() []byte {
	var axml bytes.Buffer
	str := "com.dena.skyleap"
	var strBlock bytes.Buffer
	strBlock.WriteByte(byte(len(str))) // u16 len prefix
	strBlock.WriteByte(byte(len(str))) // u8 len prefix
	strBlock.WriteString(str)
	strBlock.WriteByte(0) // null terminator
	for strBlock.Len()%4 != 0 {
		strBlock.WriteByte(0)
	}

	spChunkSize := uint32(28 + 4 + strBlock.Len())
	totalAxmlSize := uint32(8 + spChunkSize)

	_ = binary.Write(&axml, binary.LittleEndian, uint32(0x00080003)) // RES_XML_TYPE
	_ = binary.Write(&axml, binary.LittleEndian, totalAxmlSize)
	_ = binary.Write(&axml, binary.LittleEndian, uint32(0x001C0001)) // RES_STRING_POOL_TYPE
	_ = binary.Write(&axml, binary.LittleEndian, spChunkSize)
	_ = binary.Write(&axml, binary.LittleEndian, uint32(1))          // stringCount
	_ = binary.Write(&axml, binary.LittleEndian, uint32(0))          // styleCount
	_ = binary.Write(&axml, binary.LittleEndian, uint32(1<<8))       // flags: UTF-8
	_ = binary.Write(&axml, binary.LittleEndian, uint32(32))         // stringsStart (28 + 4)
	_ = binary.Write(&axml, binary.LittleEndian, uint32(0))          // stylesStart
	_ = binary.Write(&axml, binary.LittleEndian, uint32(0))          // offset[0]
	axml.Write(strBlock.Bytes())

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, _ := zw.Create("AndroidManifest.xml")
	_, _ = w.Write(axml.Bytes())
	_ = zw.Close()
	return zipBuf.Bytes()
}

func TestControlAndroidUpload_PathTraversalBlocked(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	apkBytes := buildMinimalValidApkBytes()

	// 1. Filenames containing paths, slashes, or traversal sequences should be cleaned safely
	// and stored strictly within the created upload directory without escaping.
	safeTestCases := []struct {
		rawName      string
		expectedBase string
	}{
		{"../../evil.apk", "evil.apk"},
		{"..\\..\\evil.apk", "evil.apk"},
		{"/evil.apk", "evil.apk"},
		{"sub/../../evil.apk", "evil.apk"},
		{"..%2Fevil.apk", "evil.apk"},
		{"%2E%2E%2Fevil.apk", "evil.apk"},
		{"C:\\Users\\admin\\Downloads\\skyleap.apk", "skyleap.apk"},
		{"C:/fakepath/skyleap.apk", "skyleap.apk"},
		{"C:skyleap.apk", "skyleap.apk"},
		{"/var/tmp/upload.apk", "upload.apk"},
		{"  spaces_test.apk  ", "spaces_test.apk"},
	}

	for _, tc := range safeTestCases {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", tc.rawName)
		if err != nil {
			t.Fatalf("[%s] failed to create form file: %v", tc.rawName, err)
		}
		_, _ = part.Write(apkBytes)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
		req.Host = "127.0.0.1:8125"
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("[%s] expected 200 OK for safe path handling, got %d: %s", tc.rawName, w.Code, w.Body.String())
			continue
		}

		var resp struct {
			Ok            bool   `json:"ok"`
			FilePath      string `json:"file_path"`
			BaseInputName string `json:"base_input_name"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Errorf("[%s] failed to parse response: %v", tc.rawName, err)
			continue
		}

		if resp.BaseInputName != tc.expectedBase {
			t.Errorf("[%s] expected BaseInputName %q, got %q", tc.rawName, tc.expectedBase, resp.BaseInputName)
		}

		// Verify file exists on disk and is strictly inside the uploadDir
		if fi, err := os.Stat(resp.FilePath); err != nil || fi.Size() == 0 {
			t.Errorf("[%s] uploaded file does not exist on disk: %s (err: %v)", tc.rawName, resp.FilePath, err)
		}
		uploadDir := filepath.Dir(resp.FilePath)
		if !strings.Contains(uploadDir, "gbf_upload_") {
			t.Errorf("[%s] upload dir expected to contain 'gbf_upload_', got: %s", tc.rawName, uploadDir)
		}
		rel, err := filepath.Rel(uploadDir, resp.FilePath)
		if err != nil || rel != tc.expectedBase || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || rel == "." {
			t.Errorf("[%s] file escaped upload directory! rel=%q, uploadDir=%s, fullPath=%s", tc.rawName, rel, uploadDir, resp.FilePath)
		}

		_ = os.RemoveAll(uploadDir)
	}

	// 2. Genuinely invalid filenames (empty, dot-only, null bytes, or unsupported formats) must return 400 Bad Request
	invalidFilenames := []string{
		"../../",
		"..\\..\\",
		"/",
		"\\",
		"",
		"   ",
		".",
		"..",
		".apk",
		"/path/to/.apk",
		"bad.txt",
		"malicious.sh",
		"evil.apk\x00",
		"\x00evil.apk",
	}

	for _, badName := range invalidFilenames {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", badName)
		if err != nil {
			t.Fatalf("[%s] failed to create form file: %v", badName, err)
		}
		_, _ = part.Write(apkBytes)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
		req.Host = "127.0.0.1:8125"
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		ctrl.handleRoute(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("[%s] expected 400 Bad Request for invalid filename, got %d: %s", badName, w.Code, w.Body.String())
		}
	}
}

func TestControlAndroidUpload_SuccessPreservesFile(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	apkBytes := buildMinimalValidApkBytes()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "skyleap_test.apk")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	_, _ = part.Write(apkBytes)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/android/upload", &body)
	req.Host = "127.0.0.1:8125"
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid upload, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Ok            bool   `json:"ok"`
		FilePath      string `json:"file_path"`
		BaseInputName string `json:"base_input_name"`
		PackageName   string `json:"package_name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	if !resp.Ok {
		t.Errorf("expected ok=true")
	}
	if resp.PackageName != "com.dena.skyleap" {
		t.Errorf("expected package com.dena.skyleap, got %s", resp.PackageName)
	}
	if resp.BaseInputName != "skyleap_test.apk" {
		t.Errorf("expected base_input_name=skyleap_test.apk, got %s", resp.BaseInputName)
	}

	// Verify that the file actually exists on disk in the upload directory
	if fi, err := os.Stat(resp.FilePath); err != nil || fi.Size() == 0 {
		t.Errorf("expected preserved upload file at %s, err: %v", resp.FilePath, err)
	}

	// Clean up preserved upload directory after test
	_ = os.RemoveAll(filepath.Dir(resp.FilePath))
}

func TestControlAndroidPatch_Validation(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// 1. Invalid new_package_name
	bodyBadPkg, _ := json.Marshal(map[string]interface{}{
		"file_path":        "dummy.apk",
		"new_package_name": "../../evil",
	})
	reqBadPkg := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(bodyBadPkg))
	reqBadPkg.Host = "127.0.0.1:8125"
	wBadPkg := httptest.NewRecorder()
	ctrl.handleRoute(wBadPkg, reqBadPkg)
	if wBadPkg.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad new_package_name, got %d: %s", wBadPkg.Code, wBadPkg.Body.String())
	}

	// 2. Negative and over-limit timeout_seconds
	badTimeouts := []int{-1, -10, 3601, 100000}
	for _, badTimeout := range badTimeouts {
		bodyBadTimeout, _ := json.Marshal(map[string]interface{}{
			"file_path":       "dummy.apk",
			"timeout_seconds": badTimeout,
		})
		reqBadTimeout := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(bodyBadTimeout))
		reqBadTimeout.Host = "127.0.0.1:8125"
		wBadTimeout := httptest.NewRecorder()
		ctrl.handleRoute(wBadTimeout, reqBadTimeout)
		if wBadTimeout.Code != http.StatusBadRequest {
			t.Errorf("[%d] expected 400 for out-of-bounds timeout_seconds, got %d: %s", badTimeout, wBadTimeout.Code, wBadTimeout.Body.String())
		}
	}
}

func TestControlAndroidPatch_TimeoutBounds(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	apkBytes := buildMinimalValidApkBytes()
	tempApk := filepath.Join(t.TempDir(), "skyleap_timeout.apk")
	if err := os.WriteFile(tempApk, apkBytes, 0644); err != nil {
		t.Fatalf("failed to write temp apk: %v", err)
	}

	ctrl.checkComponentsFn = func(toolsDir, exeDir string) (bool, bool, []patcher.ComponentStatus, string) {
		return true, true, nil, ""
	}

	// 1. timeout_seconds == 0 (default: 15 minutes) should be accepted and start successfully
	jobDone1 := make(chan struct{})
	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		defer close(jobDone1)
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("expected context to have a deadline")
		} else {
			remaining := time.Until(deadline)
			// Default is 15 minutes (~900s), should be between 14m and 16m
			if remaining < 14*time.Minute || remaining > 16*time.Minute {
				t.Errorf("expected default timeout around 15m, got remaining: %v", remaining)
			}
		}
		return &patcher.BundleResult{SingleApk: "skyleap-patched.apk"}, nil
	}

	bodyZero, _ := json.Marshal(map[string]interface{}{
		"file_path":       tempApk,
		"timeout_seconds": 0,
	})
	reqZero := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(bodyZero))
	reqZero.Host = "127.0.0.1:8125"
	wZero := httptest.NewRecorder()
	ctrl.handleRoute(wZero, reqZero)
	if wZero.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for timeout_seconds=0, got %d: %s", wZero.Code, wZero.Body.String())
	}
	select {
	case <-jobDone1:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job 1 to finish")
	}

	// Wait for status reset
	time.Sleep(50 * time.Millisecond)

	// 2. timeout_seconds == 3600 (upper bound: 1 hour) should be accepted and start successfully
	jobDone2 := make(chan struct{})
	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		defer close(jobDone2)
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("expected context to have a deadline")
		} else {
			remaining := time.Until(deadline)
			// 3600s = 60 minutes, should be between 58m and 61m
			if remaining < 58*time.Minute || remaining > 61*time.Minute {
				t.Errorf("expected timeout around 60m, got remaining: %v", remaining)
			}
		}
		return &patcher.BundleResult{SingleApk: "skyleap-patched.apk"}, nil
	}

	bodyMax, _ := json.Marshal(map[string]interface{}{
		"file_path":       tempApk,
		"timeout_seconds": 3600,
	})
	reqMax := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(bodyMax))
	reqMax.Host = "127.0.0.1:8125"
	wMax := httptest.NewRecorder()
	ctrl.handleRoute(wMax, reqMax)
	if wMax.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for timeout_seconds=3600, got %d: %s", wMax.Code, wMax.Body.String())
	}
	select {
	case <-jobDone2:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job 2 to finish")
	}

	// Wait for status reset
	time.Sleep(50 * time.Millisecond)

	// 3. timeout_seconds == 60 (explicit 1 minute) should be accepted and set duration accurately
	jobDone3 := make(chan struct{})
	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		defer close(jobDone3)
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Errorf("expected context to have a deadline")
		} else {
			remaining := time.Until(deadline)
			if remaining < 50*time.Second || remaining > 70*time.Second {
				t.Errorf("expected timeout around 60s, got remaining: %v", remaining)
			}
		}
		return &patcher.BundleResult{SingleApk: "skyleap-patched.apk"}, nil
	}

	body60, _ := json.Marshal(map[string]interface{}{
		"file_path":       tempApk,
		"timeout_seconds": 60,
	})
	req60 := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body60))
	req60.Host = "127.0.0.1:8125"
	w60 := httptest.NewRecorder()
	ctrl.handleRoute(w60, req60)
	if w60.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for timeout_seconds=60, got %d: %s", w60.Code, w60.Body.String())
	}
	select {
	case <-jobDone3:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for job 3 to finish")
	}
}

func TestControlAndroidAdbExtract_FailureCleanupWithMockAdb(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	// Build a mock adb that succeeds on `pm path` and fails on `pull`
	mockDir := t.TempDir()
	mockSrc := filepath.Join(mockDir, "mock_adb.go")
	srcCode := `package main
import (
	"fmt"
	"os"
	"strings"
)
func main() {
	args := strings.Join(os.Args, " ")
	if strings.Contains(args, "pm path") {
		fmt.Println("package:/data/app/com.dena.skyleap/base.apk")
		os.Exit(0)
	}
	if strings.Contains(args, "dumpsys package") {
		fmt.Println("versionName=1.60.0")
		os.Exit(0)
	}
	if strings.Contains(args, "pull") {
		// Simulate adb pull network failure or disconnection
		fmt.Fprintln(os.Stderr, "adb: error: failed to copy: connection closed")
		os.Exit(1)
	}
	os.Exit(0)
}
`
	if err := os.WriteFile(mockSrc, []byte(srcCode), 0644); err != nil {
		t.Fatalf("failed to write mock adb src: %v", err)
	}

	toolsDir := patcher.GetAndroidToolsDir()
	platformTools := filepath.Join(toolsDir, "platform-tools")
	if err := os.MkdirAll(platformTools, 0755); err != nil {
		t.Fatalf("failed to create platform-tools: %v", err)
	}
	defer os.RemoveAll(toolsDir)

	mockBin := filepath.Join(platformTools, "adb")
	if runtime.GOOS == "windows" {
		mockBin += ".exe"
		buildCmd := exec.Command("go", "build", "-o", mockBin, mockSrc)
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Skipf("skipping mock adb test (go build failed: %v): %s", err, string(out))
		}
	} else {
		shScript := "#!/bin/sh\n" +
			"for arg in \"$@\"; do\n" +
			"    if [ \"$arg\" = \"path\" ]; then\n" +
			"        echo \"package:/data/app/com.dena.skyleap/base.apk\"\n" +
			"        exit 0\n" +
			"    fi\n" +
			"    if [ \"$arg\" = \"dumpsys\" ]; then\n" +
			"        echo \"versionName=1.60.0\"\n" +
			"        exit 0\n" +
			"    fi\n" +
			"    if [ \"$arg\" = \"pull\" ]; then\n" +
			"        echo \"adb: pull error simulated\" >&2\n" +
			"        exit 1\n" +
			"    fi\n" +
			"done\n" +
			"exit 0\n"
		if err := os.WriteFile(mockBin, []byte(shScript), 0755); err != nil {
			t.Fatalf("failed to write mock adb script: %v", err)
		}
	}

	tempBase := os.TempDir()
	countExtractDirs := func() int {
		entries, _ := os.ReadDir(tempBase)
		cnt := 0
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "gbf_extracted_") {
				cnt++
			}
		}
		return cnt
	}

	beforeCount := countExtractDirs()

	body, _ := json.Marshal(map[string]interface{}{
		"serial":       "emulator-mock",
		"package_name": "com.dena.skyleap",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/android/adb/extract", bytes.NewBuffer(body))
	req.Host = "127.0.0.1:8125"
	w := httptest.NewRecorder()
	ctrl.handleRoute(w, req)

	// Since pull failed, it must return 500
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error when adb pull fails, got %d: %s", w.Code, w.Body.String())
	}

	// Verify that the extractDir created during extraction was cleaned up!
	afterCount := countExtractDirs()
	if afterCount > beforeCount {
		t.Errorf("temporary extract directory was leaked after failed pull: before=%d, after=%d", beforeCount, afterCount)
	}
}

func TestControlAndroidPatch_PanicRecovery(t *testing.T) {
	ctrl, cleanup := setupTestControlServer(t)
	defer cleanup()

	ctrl.checkComponentsFn = func(toolsDir, exeDir string) (bool, bool, []patcher.ComponentStatus, string) {
		return true, true, nil, ""
	}

	apkBytes := buildMinimalValidApkBytes()
	tempApk := filepath.Join(t.TempDir(), "panic_recovery_test.apk")
	if err := os.WriteFile(tempApk, apkBytes, 0644); err != nil {
		t.Fatalf("failed to write temp apk: %v", err)
	}

	// 1. Configure runPatchFn to simulate an unhandled panic inside the background job
	panicTriggered := make(chan struct{})
	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		close(panicTriggered)
		panic("simulated critical crash in patch engine")
	}

	body1, _ := json.Marshal(map[string]interface{}{
		"file_path": tempApk,
	})
	req1 := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body1))
	req1.Host = "127.0.0.1:8125"
	w1 := httptest.NewRecorder()
	ctrl.handleRoute(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting patch job 1, got %d: %s", w1.Code, w1.Body.String())
	}

	select {
	case <-panicTriggered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for patch job to trigger panic")
	}

	// 2. Poll until the background defer/recover completes and status is updated
	deadline := time.Now().Add(2 * time.Second)
	recovered := false
	for time.Now().Before(deadline) {
		ctrl.androidPatchMu.Lock()
		running := ctrl.androidPatchStatus.Running
		done := ctrl.androidPatchStatus.Done
		errStr := ctrl.androidPatchStatus.Error
		logs := ctrl.androidPatchStatus.Logs
		cancelNil := ctrl.androidPatchCancelFn == nil
		ctrl.androidPatchMu.Unlock()

		if !running && done && strings.Contains(errStr, "simulated critical crash in patch engine") && cancelNil {
			hasPanicLog := false
			for _, l := range logs {
				if strings.Contains(l, "[PANIC]") && strings.Contains(l, "simulated critical crash in patch engine") {
					hasPanicLog = true
					break
				}
			}
			if !hasPanicLog {
				t.Errorf("expected logs to contain [PANIC] entry, got: %v", logs)
			}
			recovered = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !recovered {
		ctrl.androidPatchMu.Lock()
		defer ctrl.androidPatchMu.Unlock()
		t.Fatalf("timed out waiting for panic recovery: Running=%v, Done=%v, Error=%q, CancelFnNil=%v",
			ctrl.androidPatchStatus.Running, ctrl.androidPatchStatus.Done, ctrl.androidPatchStatus.Error, ctrl.androidPatchCancelFn == nil)
	}

	// 3. Verify /api/android/patch/status HTTP endpoint reflects the recovered state
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/android/patch/status", nil)
	reqStatus.Host = "127.0.0.1:8125"
	wStatus := httptest.NewRecorder()
	ctrl.handleRoute(wStatus, reqStatus)
	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for status, got %d", wStatus.Code)
	}
	var stResp struct {
		Running bool     `json:"running"`
		Done    bool     `json:"done"`
		Error   string   `json:"error"`
		Logs    []string `json:"logs"`
	}
	if err := json.Unmarshal(wStatus.Body.Bytes(), &stResp); err != nil {
		t.Fatalf("failed to parse status JSON: %v", err)
	}
	if stResp.Running {
		t.Errorf("expected status.running=false after panic, got true")
	}
	if !stResp.Done {
		t.Errorf("expected status.done=true after panic, got false")
	}
	if !strings.Contains(stResp.Error, "simulated critical crash in patch engine") {
		t.Errorf("expected status.error to contain panic message, got: %q", stResp.Error)
	}

	// 4. Verify subsequent patch task can start normally and complete successfully
	job2Done := make(chan struct{})
	ctrl.runPatchFn = func(ctx context.Context, opts patcher.PatchOptions) (*patcher.BundleResult, error) {
		defer close(job2Done)
		return &patcher.BundleResult{SingleApk: "skyleap-patched.apk"}, nil
	}

	body2, _ := json.Marshal(map[string]interface{}{
		"file_path": tempApk,
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/android/patch", bytes.NewBuffer(body2))
	req2.Host = "127.0.0.1:8125"
	w2 := httptest.NewRecorder()
	ctrl.handleRoute(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK starting patch job 2, got %d: %s", w2.Code, w2.Body.String())
	}

	select {
	case <-job2Done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for patch job 2 to finish")
	}

	deadline2 := time.Now().Add(2 * time.Second)
	job2Success := false
	for time.Now().Before(deadline2) {
		ctrl.androidPatchMu.Lock()
		running := ctrl.androidPatchStatus.Running
		done := ctrl.androidPatchStatus.Done
		errStr := ctrl.androidPatchStatus.Error
		cancelNil := ctrl.androidPatchCancelFn == nil
		ctrl.androidPatchMu.Unlock()

		if !running && done && errStr == "" && cancelNil {
			job2Success = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if !job2Success {
		ctrl.androidPatchMu.Lock()
		defer ctrl.androidPatchMu.Unlock()
		t.Fatalf("job 2 failed to complete cleanly after job 1 panic recovery: Running=%v, Done=%v, Error=%q, CancelFnNil=%v",
			ctrl.androidPatchStatus.Running, ctrl.androidPatchStatus.Done, ctrl.androidPatchStatus.Error, ctrl.androidPatchCancelFn == nil)
	}
}

