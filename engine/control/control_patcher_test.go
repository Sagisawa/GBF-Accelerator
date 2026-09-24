package control

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gbf-proxy/cache"
	"gbf-proxy/config"
	"gbf-proxy/patcher"
	"gbf-proxy/telemetry"
)

func setupTestControlServer(t *testing.T) (*ControlServer, func()) {
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
	return ctrl, cleanup
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



