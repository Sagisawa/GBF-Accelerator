package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gbf-proxy/config"
	"gbf-proxy/desktop"
	"gbf-proxy/patcher"
)

type AndroidPatchStatus struct {
	Running   bool                  `json:"running"`
	Stage     int                   `json:"stage"`      // 0..5 (0 = idle)
	StageText string                `json:"stage_text"` // e.g. "Inspecting input package..."
	Progress  float64               `json:"progress"`   // 0.0..1.0
	Logs      []string              `json:"logs"`
	Error     string                `json:"error"`
	Done      bool                  `json:"done"`
	Result    *patcher.BundleResult `json:"result,omitempty"`
}

type patchListenerBridge struct {
	server *ControlServer
}

func (b *patchListenerBridge) OnStage(stage int, text string, progress float64) {
	b.server.androidPatchMu.Lock()
	defer b.server.androidPatchMu.Unlock()
	b.server.androidPatchStatus.Stage = stage
	b.server.androidPatchStatus.StageText = text
	b.server.androidPatchStatus.Progress = progress
}

func (b *patchListenerBridge) OnLog(line string) {
	b.server.androidPatchMu.Lock()
	defer b.server.androidPatchMu.Unlock()
	b.server.androidPatchStatus.Logs = append(b.server.androidPatchStatus.Logs, line)
	// Cap logs to last 1000 lines to prevent unbounded memory growth
	if len(b.server.androidPatchStatus.Logs) > 1000 {
		b.server.androidPatchStatus.Logs = b.server.androidPatchStatus.Logs[len(b.server.androidPatchStatus.Logs)-1000:]
	}
}

func (c *ControlServer) getExeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return "."
}

func (c *ControlServer) handleAndroidEnv(w http.ResponseWriter, req *http.Request) {
	exeDir := c.getExeDir()
	toolsDir := patcher.GetAndroidToolsDir()

	// 1. Probe Android Components
	componentsInstalled, componentsVerified, componentStatuses, componentsErr := patcher.CheckComponents(toolsDir, exeDir)
	componentsCorrupted := (componentsInstalled && !componentsVerified)

	// 2. Probe Java runtime
	javaFound := false
	javaPath, javaVer, javaErr := patcher.FindJavaRuntime("")
	var javaErrStr string
	if javaErr == nil {
		javaFound = true
	} else {
		javaErrStr = javaErr.Error()
	}

	// 3. Probe LSPatch Jar
	lspatchFound := false
	lspatchVerified := false
	lspatchPath, lspatchErr := patcher.FindLSPatchJar("", exeDir)
	var lspatchErrStr string
	var lspatchSha string
	if lspatchErr == nil {
		lspatchFound = true
		if valErr := patcher.ValidateLSPatchJar(lspatchPath); valErr == nil {
			lspatchVerified = true
		} else {
			lspatchErrStr = valErr.Error()
		}
		// Compute sha256
		if f, err := os.Open(lspatchPath); err == nil {
			h := sha256.New()
			_, _ = io.Copy(h, f)
			_ = f.Close()
			lspatchSha = hex.EncodeToString(h.Sum(nil))
		}
	} else {
		lspatchErrStr = lspatchErr.Error()
	}

	// 4. Probe SkyLeapModule APK
	moduleFound := false
	moduleVerified := false
	modulePath, moduleErr := patcher.FindModuleApk("", exeDir)
	var moduleErrStr string
	if moduleErr == nil {
		moduleFound = true
		if valErr := patcher.ValidateModuleApk(modulePath); valErr == nil {
			moduleVerified = true
		} else {
			moduleErrStr = valErr.Error()
		}
	} else {
		moduleErrStr = moduleErr.Error()
	}

	// 5. Probe ADB
	adbFound, adbPath, adbVer, adbErr := patcher.CheckAdb(toolsDir, exeDir)
	var adbErrStr string
	if adbErr != nil {
		adbErrStr = adbErr.Error()
	}

	ready := javaFound && componentsVerified

	var toolsDiskBytes int64
	_ = filepath.Walk(toolsDir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			toolsDiskBytes += fi.Size()
		}
		return nil
	})
	jreDir := filepath.Join(config.GetBaseDir(), "jre")
	_ = filepath.Walk(jreDir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && fi != nil && !fi.IsDir() {
			toolsDiskBytes += fi.Size()
		}
		return nil
	})
	backupDir := filepath.Join(config.GetBaseDir(), "backups")

	c.componentDlMu.Lock()
	dlActive := c.componentDlActive
	dlProg := c.componentDlProgress
	c.componentDlMu.Unlock()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                   true,
		"ready":                ready,
		"full_env_ready":       ready && adbFound,
		"tools_disk_bytes":     toolsDiskBytes,
		"backup_dir":           backupDir,
		"components_installed": componentsInstalled,
		"components_verified":  componentsVerified,
		"components_corrupted": componentsCorrupted,
		"components_error":     componentsErr,
		"tools_dir":            toolsDir,
		"components":           componentStatuses,
		"download": map[string]interface{}{
			"active":           dlActive,
			"current_file":     dlProg.CurrentFile,
			"file_index":       dlProg.FileIndex,
			"total_files":      dlProg.TotalFiles,
			"downloaded_bytes": dlProg.DownloadedBytes,
			"total_bytes":      dlProg.TotalBytes,
			"percent":          dlProg.Percent,
			"speed_bytes_sec":  dlProg.SpeedBytesSec,
			"stage":            dlProg.Stage,
			"error":            dlProg.Error,
			"done":             dlProg.Done,
		},
		"java": map[string]interface{}{
			"found":   javaFound,
			"path":    javaPath,
			"version": javaVer,
			"error":   javaErrStr,
		},
		"adb": map[string]interface{}{
			"found":   adbFound,
			"path":    adbPath,
			"version": adbVer,
			"error":   adbErrStr,
		},
		"lspatch": map[string]interface{}{
			"found":           lspatchFound,
			"path":            lspatchPath,
			"version":         patcher.CanonicalLSPatchVersion,
			"sha256":          lspatchSha,
			"expected_sha256": patcher.CanonicalLSPatchSHA256,
			"verified":        lspatchVerified,
			"error":           lspatchErrStr,
		},
		"module": map[string]interface{}{
			"found":    moduleFound,
			"path":     modulePath,
			"verified": moduleVerified,
			"error":    moduleErrStr,
		},
	})
}

func (c *ControlServer) handleAndroidInspect(w http.ResponseWriter, req *http.Request) {
	var body struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "Invalid JSON body"})
		return
	}

	targetPath := strings.TrimSpace(body.FilePath)
	if targetPath == "" {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "file_path is required"})
		return
	}

	fi, err := os.Stat(targetPath)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": fmt.Sprintf("File not found: %v", err)})
		return
	}
	if fi.IsDir() {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "Specified path is a directory, expected an APK/APKS/XAPK package file"})
		return
	}

	workDir, err := os.MkdirTemp("", "gbf_inspect_*")
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to create temp inspect dir: %v", err)})
		return
	}
	defer os.RemoveAll(workDir)

	pkg, err := patcher.InspectInput(targetPath, workDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to inspect package: %v", err)})
		return
	}

	isOfficialSkyLeap := (pkg.PackageName == "com.dena.skyleap")

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                      true,
		"file_path":               targetPath,
		"base_input_name":         filepath.Base(targetPath),
		"package_name":            pkg.PackageName,
		"version_name":            pkg.VersionName,
		"is_split":                pkg.IsSplit,
		"total_apks":              pkg.TotalApks,
		"is_official_skyleap":     isOfficialSkyLeap,
		"suggested_clone_package": pkg.PackageName + ".accelerated",
	})
}

func (c *ControlServer) handleAndroidUpload(w http.ResponseWriter, req *http.Request) {
	// Allow up to 256MB multipart upload for larger Split APKs or XAPK bundles
	if err := req.ParseMultipartForm(256 << 20); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to parse upload form: %v", err)})
		return
	}

	file, header, err := req.FormFile("file")
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "No file uploaded (field 'file' required)"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".apk" && ext != ".apks" && ext != ".xapk" && ext != ".zip" {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("Unsupported file format %q. Expected .apk, .apks, .xapk, or .zip", ext),
		})
		return
	}

	uploadDir, err := os.MkdirTemp("", "gbf_upload_*")
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to create upload dir: %v", err)})
		return
	}

	savedPath := filepath.Join(uploadDir, header.Filename)
	outF, err := os.Create(savedPath)
	if err != nil {
		_ = os.RemoveAll(uploadDir)
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to save uploaded file: %v", err)})
		return
	}

	if _, err := io.Copy(outF, file); err != nil {
		_ = outF.Close()
		_ = os.RemoveAll(uploadDir)
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to write uploaded file: %v", err)})
		return
	}
	_ = outF.Close()

	// Inspect the uploaded package
	workDir, err := os.MkdirTemp("", "gbf_inspect_*")
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Failed to create temp inspect dir: %v", err)})
		return
	}
	defer os.RemoveAll(workDir)

	pkg, err := patcher.InspectInput(savedPath, workDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Uploaded file is not a valid Android package: %v", err)})
		return
	}

	isOfficialSkyLeap := (pkg.PackageName == "com.dena.skyleap")

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                      true,
		"file_path":               savedPath,
		"base_input_name":         header.Filename,
		"package_name":            pkg.PackageName,
		"version_name":            pkg.VersionName,
		"is_split":                pkg.IsSplit,
		"total_apks":              pkg.TotalApks,
		"is_official_skyleap":     isOfficialSkyLeap,
		"suggested_clone_package": pkg.PackageName + ".accelerated",
	})
}

func (c *ControlServer) handleAndroidPatch(w http.ResponseWriter, req *http.Request) {
	var body struct {
		FilePath       string `json:"file_path"`
		OutputDir      string `json:"output_dir"`
		AppLabel       string `json:"app_label"`
		NewPackageName string `json:"new_package_name"`
		AutoBackup     *bool  `json:"auto_backup"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "Invalid JSON body"})
		return
	}

	targetPath := strings.TrimSpace(body.FilePath)
	if targetPath == "" {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "file_path is required"})
		return
	}

	if _, err := os.Stat(targetPath); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": fmt.Sprintf("Target package not found: %v", err)})
		return
	}

	autoBackup := true
	if body.AutoBackup != nil {
		autoBackup = *body.AutoBackup
	}
	appLabel := strings.TrimSpace(body.AppLabel)
	newPkg := strings.TrimSpace(body.NewPackageName)

	c.androidPatchMu.Lock()
	if c.androidPatchStatus.Running {
		c.androidPatchMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "已有正在进行的 Patch 任务，请等待完成",
		})
		return
	}
	c.androidPatchMu.Unlock()

	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	_, allVerified, _, errStr := patcher.CheckComponents(toolsDir, exeDir)
	if !allVerified {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": "Android Patch 组件尚未就绪或已损坏，请先在面板中下载并启用组件: " + errStr,
		})
		return
	}

	outputDir := strings.TrimSpace(body.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Join(".", "output_patched")
	}
	absOutputDir, err := filepath.Abs(outputDir)
	if err == nil {
		outputDir = absOutputDir
	}

	c.androidPatchMu.Lock()
	if c.androidPatchStatus.Running {
		c.androidPatchMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "已有正在进行的 Patch 任务，请等待完成",
		})
		return
	}

	// Initialize new job status
	c.androidPatchStatus = AndroidPatchStatus{
		Running:   true,
		Stage:     0,
		StageText: "Starting patch job...",
		Progress:  0.0,
		Logs:      []string{fmt.Sprintf("Patch requested for: %s", filepath.Base(targetPath))},
		Error:     "",
		Done:      false,
		Result:    nil,
	}
	c.androidPatchMu.Unlock()

	// Launch patch execution in background goroutine (non-blocking)
	go func(inPath, outDir, label, customPkg string, backup bool) {
		bridge := &patchListenerBridge{server: c}
		opts := patcher.PatchOptions{
			InputPath:      inPath,
			OutputDir:      outDir,
			AppLabel:       label,
			NewPackageName: customPkg,
			AutoBackup:     backup,
			Listener:       bridge,
		}

		p, err := patcher.NewPatcher(opts)
		if err != nil {
			c.androidPatchMu.Lock()
			c.androidPatchStatus.Running = false
			c.androidPatchStatus.Done = true
			c.androidPatchStatus.Error = err.Error()
			c.androidPatchStatus.Logs = append(c.androidPatchStatus.Logs, fmt.Sprintf("[ERROR] %v", err))
			c.androidPatchMu.Unlock()
			return
		}

		result, err := p.Run()

		c.androidPatchMu.Lock()
		defer c.androidPatchMu.Unlock()
		c.androidPatchStatus.Running = false
		c.androidPatchStatus.Done = true
		if err != nil {
			c.androidPatchStatus.Error = err.Error()
			c.androidPatchStatus.Logs = append(c.androidPatchStatus.Logs, fmt.Sprintf("[ERROR] %v", err))
		} else {
			c.androidPatchStatus.Result = result
			c.androidPatchStatus.Progress = 1.0
			c.androidPatchStatus.Stage = 5
			c.androidPatchStatus.StageText = "Patch completed successfully"
		}
	}(targetPath, outputDir, appLabel, newPkg, autoBackup)

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Patch job started",
	})
}

func (c *ControlServer) handleAndroidPatchStatus(w http.ResponseWriter, req *http.Request) {
	c.androidPatchMu.Lock()
	defer c.androidPatchMu.Unlock()

	backupDir := filepath.Join(config.GetBaseDir(), "backups")
	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":         true,
		"running":    c.androidPatchStatus.Running,
		"stage":      c.androidPatchStatus.Stage,
		"stage_text": c.androidPatchStatus.StageText,
		"progress":   c.androidPatchStatus.Progress,
		"logs":       c.androidPatchStatus.Logs,
		"error":      c.androidPatchStatus.Error,
		"done":       c.androidPatchStatus.Done,
		"result":     c.androidPatchStatus.Result,
		"backup_dir": backupDir,
	})
}

func (c *ControlServer) handleAndroidPatchOpenOutput(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	targetDir := strings.TrimSpace(body.Path)
	if targetDir == "" {
		c.androidPatchMu.Lock()
		if c.androidPatchStatus.Result != nil {
			if c.androidPatchStatus.Result.IsSplit {
				targetDir = c.androidPatchStatus.Result.SplitDir
			} else if c.androidPatchStatus.Result.SingleApk != "" {
				targetDir = filepath.Dir(c.androidPatchStatus.Result.SingleApk)
			}
		}
		c.androidPatchMu.Unlock()
	}

	if targetDir == "" {
		targetDir = filepath.Join(".", "output_patched")
	}

	absDir, err := filepath.Abs(targetDir)
	if err == nil {
		targetDir = absDir
	}

	_ = os.MkdirAll(targetDir, 0755)

	go func() {
		_ = desktop.OpenFolder(targetDir)
	}()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"path": targetDir,
	})
}

func (c *ControlServer) handleAndroidComponentsDownload(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Force      bool              `json:"force"`
		CustomURLs map[string]string `json:"custom_urls,omitempty"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()

	c.componentDlMu.Lock()
	if c.componentDlActive {
		c.componentDlMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "组件下载任务正在进行中",
		})
		return
	}

	if !body.Force {
		_, allVerified, _, _ := patcher.CheckComponents(toolsDir, exeDir)
		if allVerified {
			c.componentDlMu.Unlock()
			c.sendJSON(w, http.StatusOK, map[string]interface{}{
				"ok":                true,
				"already_installed": true,
				"message":           "组件已全部安装并通过 SHA-256 校验",
			})
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.componentDlActive = true
	c.componentDlCancelFn = cancel
	c.componentDlProgress = patcher.DownloadProgress{
		Active:  true,
		Stage:   "downloading",
		Percent: 0,
	}
	c.componentDlMu.Unlock()

	go func(ctx context.Context, toolsDir string, customURLs map[string]string) {
		err := patcher.DownloadComponents(ctx, toolsDir, customURLs, func(p patcher.DownloadProgress) {
			c.componentDlMu.Lock()
			c.componentDlProgress = p
			c.componentDlMu.Unlock()
		})

		c.componentDlMu.Lock()
		defer c.componentDlMu.Unlock()
		c.componentDlActive = false
		c.componentDlCancelFn = nil
		if err != nil {
			c.componentDlProgress.Active = false
			c.componentDlProgress.Done = false
			c.componentDlProgress.Stage = "error"
			c.componentDlProgress.Error = err.Error()
		} else {
			c.componentDlProgress.Active = false
			c.componentDlProgress.Done = true
			c.componentDlProgress.Stage = "done"
			c.componentDlProgress.Percent = 100
			c.componentDlProgress.Error = ""
		}
	}(ctx, toolsDir, body.CustomURLs)

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "组件下载已启动",
	})
}

func (c *ControlServer) handleAndroidComponentsDownloadStatus(w http.ResponseWriter, req *http.Request) {
	c.componentDlMu.Lock()
	defer c.componentDlMu.Unlock()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"active":   c.componentDlActive,
		"progress": c.componentDlProgress,
	})
}

func (c *ControlServer) handleAndroidComponentsDownloadCancel(w http.ResponseWriter, req *http.Request) {
	c.componentDlMu.Lock()
	defer c.componentDlMu.Unlock()

	if !c.componentDlActive || c.componentDlCancelFn == nil {
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": "没有正在进行的下载任务",
		})
		return
	}

	c.componentDlCancelFn()
	c.componentDlProgress.Stage = "cancelling"
	c.componentDlProgress.Error = "下载已被用户取消"

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "已发送取消信号",
	})
}

func (c *ControlServer) handleAndroidAdbDevices(w http.ResponseWriter, req *http.Request) {
	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	adbPath, err := patcher.FindAdb(toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"adb_found": false,
			"error":     err.Error(),
			"devices":   []patcher.AdbDevice{},
		})
		return
	}

	devices, err := patcher.ListAdbDevices(adbPath)
	if err != nil {
		c.sendJSON(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"adb_found": true,
			"adb_path":  adbPath,
			"error":     err.Error(),
			"devices":   []patcher.AdbDevice{},
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"adb_found": true,
		"adb_path":  adbPath,
		"devices":   devices,
	})
}

func (c *ControlServer) handleAndroidAdbListBrowsers(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Serial string `json:"serial"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	serial := strings.TrimSpace(body.Serial)
	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	adbPath, err := patcher.FindAdb(toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "ADB 未找到: " + err.Error()})
		return
	}

	browsers, err := patcher.ListDeviceBrowsers(adbPath, serial)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": fmt.Sprintf("获取设备浏览器列表失败: %v", err)})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"browsers": browsers,
	})
}

func (c *ControlServer) handleAndroidAdbProbeApp(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Serial      string `json:"serial"`
		PackageName string `json:"package_name"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	serial := strings.TrimSpace(body.Serial)
	pkgName := strings.TrimSpace(body.PackageName)
	if pkgName == "" {
		pkgName = "com.dena.skyleap"
	}

	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	adbPath, err := patcher.FindAdb(toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "ADB 未找到: " + err.Error()})
		return
	}

	appInfo, err := patcher.GetDeviceApp(adbPath, serial, pkgName)
	if err != nil {
		if errors.Is(err, patcher.ErrAppNotInstalled) {
			c.sendJSON(w, http.StatusOK, map[string]interface{}{
				"ok":           true,
				"installed":    false,
				"package_name": pkgName,
			})
			return
		}
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("探测应用失败: %v", err),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":           true,
		"installed":    true,
		"package_name": appInfo.PackageName,
		"version_name": appInfo.VersionName,
		"is_split":     appInfo.IsSplit,
		"total_apks":   appInfo.TotalApks,
		"remote_paths": appInfo.RemotePaths,
	})
}

func (c *ControlServer) handleAndroidAdbExtract(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Serial      string `json:"serial"`
		PackageName string `json:"package_name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "Invalid JSON body"})
		return
	}

	serial := strings.TrimSpace(body.Serial)
	pkgName := strings.TrimSpace(body.PackageName)
	if pkgName == "" {
		pkgName = "com.dena.skyleap"
	}

	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	adbPath, err := patcher.FindAdb(toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "ADB 未找到: " + err.Error()})
		return
	}

	appInfo, err := patcher.GetDeviceApp(adbPath, serial, pkgName)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("无法在设备上找到应用 %s: %v", pkgName, err),
		})
		return
	}

	extractDir, err := os.MkdirTemp("", "gbf_extracted_*")
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("创建提取临时目录失败: %v", err),
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	localPaths, err := patcher.PullDeviceApp(ctx, adbPath, serial, appInfo, extractDir, nil)
	if err != nil {
		_ = os.RemoveAll(extractDir)
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("从设备提取文件失败: %v", err),
		})
		return
	}

	inspectWorkDir, err := os.MkdirTemp("", "gbf_inspect_*")
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("创建临时检查目录失败: %v", err),
		})
		return
	}
	defer os.RemoveAll(inspectWorkDir)

	targetInspectPath := extractDir
	if len(localPaths) == 1 {
		targetInspectPath = localPaths[0]
	}

	pkg, err := patcher.InspectInput(targetInspectPath, inspectWorkDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("提取的应用包解析失败: %v", err),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                      true,
		"file_path":               targetInspectPath,
		"base_input_name":         fmt.Sprintf("%s (从设备提取)", pkgName),
		"package_name":            pkg.PackageName,
		"version_name":            pkg.VersionName,
		"is_split":                pkg.IsSplit,
		"total_apks":              pkg.TotalApks,
		"is_official_skyleap":     (pkg.PackageName == "com.dena.skyleap"),
		"suggested_clone_package": pkg.PackageName + ".accelerated",
	})
}

func (c *ControlServer) handleAndroidAdbInstall(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Serial         string `json:"serial"`
		PackageName    string `json:"package_name"`
		ForceUninstall bool   `json:"force_uninstall"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "Invalid JSON body"})
		return
	}

	serial := strings.TrimSpace(body.Serial)
	pkgName := strings.TrimSpace(body.PackageName)
	if pkgName == "" {
		pkgName = "com.dena.skyleap"
	}

	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	adbPath, err := patcher.FindAdb(toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "ADB 未找到: " + err.Error()})
		return
	}

	c.androidPatchMu.Lock()
	result := c.androidPatchStatus.Result
	c.androidPatchMu.Unlock()

	if result == nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":    false,
			"error": "当前没有可供安装的修补产物，请先完成补丁制作",
		})
		return
	}

	var apkPaths []string
	if result.IsSplit {
		entries, err := os.ReadDir(result.SplitDir)
		if err != nil {
			c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{"ok": false, "error": fmt.Sprintf("读取产物分包目录失败: %v", err)})
			return
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".apk") {
				apkPaths = append(apkPaths, filepath.Join(result.SplitDir, e.Name()))
			}
		}
	} else if result.SingleApk != "" {
		apkPaths = []string{result.SingleApk}
	}

	if len(apkPaths) == 0 {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "未在产物目录中找到有效的 .apk 安装包"})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	if body.ForceUninstall {
		_ = patcher.UninstallFromDevice(ctx, adbPath, serial, pkgName)
	}

	installOut, err := patcher.InstallToDevice(ctx, adbPath, serial, result.IsSplit, apkPaths)
	if err != nil {
		if errors.Is(err, patcher.ErrSignatureMismatch) {
			c.sendJSON(w, http.StatusConflict, map[string]interface{}{
				"ok":                 false,
				"signature_mismatch": true,
				"error":              "应用签名不兼容（官方原版签名与加速补丁签名不同）。Android 安全机制要求先卸载旧版应用方可安装",
				"details":            installOut,
			})
			return
		}
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":      false,
			"error":   fmt.Sprintf("安装到设备失败: %v", err),
			"details": installOut,
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "安装成功！应用已部署至设备",
		"output":  installOut,
	})
}

func (c *ControlServer) handleAndroidAdbDownloadTools(w http.ResponseWriter, req *http.Request) {
	toolsDir := patcher.GetAndroidToolsDir()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	adbPath, err := patcher.DownloadPlatformTools(ctx, toolsDir, "", nil)
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("下载平台工具失败: %v", err),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"message":  "ADB 平台工具下载并解压成功",
		"adb_path": adbPath,
	})
}

func (c *ControlServer) handleAndroidAdbInstallHostApp(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Serial string `json:"serial"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "Invalid JSON body"})
		return
	}

	serial := strings.TrimSpace(body.Serial)
	if serial == "" {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "未指定设备序列号"})
		return
	}

	toolsDir := patcher.GetAndroidToolsDir()
	exeDir := c.getExeDir()
	adbPath, err := patcher.FindAdb(toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": "ADB 未找到: " + err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	hostApkPath, err := patcher.FindOrFetchHostApp(ctx, toolsDir, exeDir)
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("获取 Android 端 GBF-Accelerator 安装包失败: %v", err),
		})
		return
	}

	installOut, err := patcher.InstallToDevice(ctx, adbPath, serial, false, []string{hostApkPath})
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":      false,
			"error":   fmt.Sprintf("安装 Android 端 GBF-Accelerator 到设备失败: %v", err),
			"details": installOut,
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Android 端 GBF-Accelerator 已成功安装至手机！可在手机桌面打开并开启加速服务。",
		"output":  installOut,
	})
}

func (c *ControlServer) handleAndroidPatchOpenBackup(w http.ResponseWriter, req *http.Request) {
	backupDir := filepath.Join(config.GetBaseDir(), "backups")
	absDir, err := filepath.Abs(backupDir)
	if err == nil {
		backupDir = absDir
	}

	_ = os.MkdirAll(backupDir, 0755)

	go func() {
		_ = desktop.OpenFolder(backupDir)
	}()

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"path": backupDir,
	})
}

func (c *ControlServer) handleAndroidEnvInstallAll(w http.ResponseWriter, req *http.Request) {
	var body struct {
		CustomURLs map[string]string `json:"custom_urls,omitempty"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)

	toolsDir := patcher.GetAndroidToolsDir()

	c.componentDlMu.Lock()
	if c.componentDlActive {
		c.componentDlMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "组件或环境安装任务正在进行中",
		})
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.componentDlActive = true
	c.componentDlCancelFn = cancel
	c.componentDlProgress = patcher.DownloadProgress{
		Active:  true,
		Stage:   "downloading",
		Percent: 0,
	}
	c.componentDlMu.Unlock()

	go func(ctx context.Context, toolsDir string, customURLs map[string]string) {
		err := patcher.InstallAllComponents(ctx, toolsDir, customURLs, func(p patcher.DownloadProgress) {
			c.componentDlMu.Lock()
			c.componentDlProgress = p
			c.componentDlMu.Unlock()
		})

		c.componentDlMu.Lock()
		defer c.componentDlMu.Unlock()
		c.componentDlActive = false
		c.componentDlCancelFn = nil
		if err != nil {
			c.componentDlProgress.Active = false
			c.componentDlProgress.Done = false
			c.componentDlProgress.Stage = "error"
			c.componentDlProgress.Error = err.Error()
		} else {
			c.componentDlProgress.Active = false
			c.componentDlProgress.Done = true
			c.componentDlProgress.Stage = "done"
			c.componentDlProgress.Percent = 100
			c.componentDlProgress.Error = ""
		}
	}(ctx, toolsDir, body.CustomURLs)

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "全环境组件下载与解压安装任务已启动",
	})
}

func (c *ControlServer) handleAndroidEnvUninstallAll(w http.ResponseWriter, req *http.Request) {
	c.componentDlMu.Lock()
	if c.componentDlActive {
		c.componentDlMu.Unlock()
		c.sendJSON(w, http.StatusConflict, map[string]interface{}{
			"ok":    false,
			"error": "组件正在下载安装中，请等待完成或取消后再卸载",
		})
		return
	}
	c.componentDlMu.Unlock()

	files, bytes, err := patcher.UninstallAllComponents()
	if err != nil {
		c.sendJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("卸载失败: %v", err),
		})
		return
	}

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":          true,
		"message":     fmt.Sprintf("已成功卸载全套 Android 工具链与 JRE 环境，释放 %d 个文件 (%.1f MB)", files, float64(bytes)/(1024*1024)),
		"freed_files": files,
		"freed_bytes": bytes,
	})
}




