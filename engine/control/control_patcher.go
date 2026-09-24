package control

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	// 1. Probe Java runtime
	javaFound := false
	javaPath, javaVer, javaErr := patcher.FindJavaRuntime("")
	var javaErrStr string
	if javaErr == nil {
		javaFound = true
	} else {
		javaErrStr = javaErr.Error()
	}

	// 2. Probe LSPatch Jar
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

	// 3. Probe SkyLeapModule APK
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

	ready := javaFound && lspatchVerified && moduleVerified

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"ready": ready,
		"java": map[string]interface{}{
			"found":   javaFound,
			"path":    javaPath,
			"version": javaVer,
			"error":   javaErrStr,
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
		"ok":                  true,
		"file_path":           targetPath,
		"base_input_name":     filepath.Base(targetPath),
		"package_name":        pkg.PackageName,
		"version_name":        pkg.VersionName,
		"is_split":            pkg.IsSplit,
		"total_apks":          pkg.TotalApks,
		"is_official_skyleap": isOfficialSkyLeap,
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
		"ok":                  true,
		"file_path":           savedPath,
		"base_input_name":     header.Filename,
		"package_name":        pkg.PackageName,
		"version_name":        pkg.VersionName,
		"is_split":            pkg.IsSplit,
		"total_apks":          pkg.TotalApks,
		"is_official_skyleap": isOfficialSkyLeap,
	})
}

func (c *ControlServer) handleAndroidPatch(w http.ResponseWriter, req *http.Request) {
	var body struct {
		FilePath  string `json:"file_path"`
		OutputDir string `json:"output_dir"`
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
			"error": "A patch job is already in progress. Please wait for it to complete.",
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
	go func(inPath, outDir string) {
		bridge := &patchListenerBridge{server: c}
		opts := patcher.PatchOptions{
			InputPath: inPath,
			OutputDir: outDir,
			Listener:  bridge,
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
	}(targetPath, outputDir)

	c.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"message": "Patch job started",
	})
}

func (c *ControlServer) handleAndroidPatchStatus(w http.ResponseWriter, req *http.Request) {
	c.androidPatchMu.Lock()
	defer c.androidPatchMu.Unlock()

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
