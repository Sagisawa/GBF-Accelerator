package patcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gbf-proxy/config"
)

type PatchListener interface {
	OnStage(stage int, text string, progress float64)
	OnLog(line string)
}

type PatchOptions struct {
	Context         context.Context
	Timeout         time.Duration
	InputPath       string
	OutputDir       string
	AppLabel        string
	NewPackageName  string
	AutoBackup      bool
	BackupDir       string
	JavaOverride    string
	LSPatchOverride string
	ModuleOverride  string
	Verbose         bool
	KeepTemp        bool
	Listener        PatchListener
}

type Patcher struct {
	opts   PatchOptions
	exeDir string
}

func (p *Patcher) stage(stage int, text string, progress float64) {
	msg := fmt.Sprintf("[%d/5] %s", stage, text)
	fmt.Println(msg)
	if p.opts.Listener != nil {
		p.opts.Listener.OnStage(stage, text, progress)
		p.opts.Listener.OnLog(msg)
	}
}

func (p *Patcher) logf(format string, a ...interface{}) {
	line := fmt.Sprintf(format, a...)
	fmt.Print(line)
	if p.opts.Listener != nil {
		p.opts.Listener.OnLog(strings.TrimRight(line, "\r\n"))
	}
}

func NewPatcher(opts PatchOptions) (*Patcher, error) {
	if opts.InputPath == "" {
		return nil, fmt.Errorf("input path is required")
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "./output"
	}

	exePath, err := os.Executable()
	var exeDir string
	if err == nil {
		exeDir = filepath.Dir(exePath)
	}

	return &Patcher{
		opts:   opts,
		exeDir: exeDir,
	}, nil
}

func (p *Patcher) Run() (*BundleResult, error) {
	ctx := p.opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if p.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.opts.Timeout)
		defer cancel()
	} else if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultLSPatchTimeout)
		defer cancel()
	}
	return p.RunContext(ctx)
}

func (p *Patcher) RunContext(ctx context.Context) (*BundleResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	printBanner()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 1. Setup temporary workspace
	workDir, err := os.MkdirTemp("", "gbf_patch_workspace_*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary workspace: %w", err)
	}
	defer func() {
		if !p.opts.KeepTemp {
			os.RemoveAll(workDir)
		} else {
			p.logf("[*] Preserving workspace at: %s\n", workDir)
		}
	}()

	// 2. Discover required toolchain
	p.stage(1, "Checking environment & toolchain...", 0.20)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	javaPath, javaVer, err := FindJavaRuntimeContext(ctx, p.opts.JavaOverride)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	p.logf("      - Java Runtime: %s (%s)\n", javaPath, javaVer)

	lspatchJar, err := FindLSPatchJar(p.opts.LSPatchOverride, p.exeDir)
	if err != nil {
		return nil, err
	}
	if err := ValidateLSPatchJar(lspatchJar); err != nil {
		return nil, err
	}
	p.logf("      - LSPatch Jar: %s (%s, SHA-256 verified)\n", lspatchJar, CanonicalLSPatchVersion)

	var moduleApk string
	if p.opts.ModuleOverride != "" {
		mod, err := FindModuleApk(p.opts.ModuleOverride, p.exeDir)
		if err != nil {
			return nil, err
		}
		if err := ValidateModuleApk(mod); err != nil {
			return nil, err
		}
		moduleApk = mod
		p.logf("      - SkyLeapModule: %s (User override)\n", moduleApk)
	} else {
		// When no override is given, ensure we are using the latest canonical module
		mod, err := EnsureCanonicalModule(ctx, GetAndroidToolsDir(), p.exeDir, func(msg string) {
			p.logf("      [*] %s\n", msg)
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			// If fetching latest failed (e.g. offline), fall back to existing local module if available
			fallbackMod, fbErr := FindModuleApk("", p.exeDir)
			if fbErr != nil {
				return nil, fmt.Errorf("module APK not found and auto-download failed: %w", err)
			}
			moduleApk = fallbackMod
			p.logf("      [!] Note: Could not fetch latest module (%v); falling back to existing local module: %s\n", err, moduleApk)
		} else {
			moduleApk = mod
		}

		if err := ValidateModuleApk(moduleApk); err != nil {
			return nil, err
		}
		if sha, err := ComputeFileSHA256(moduleApk); err == nil && strings.EqualFold(sha, CanonicalModuleSHA256) {
			p.logf("      - SkyLeapModule: %s (v%s, Canonical SHA-256 verified)\n", moduleApk, config.AppVersion)
		} else {
			p.logf("      - SkyLeapModule: %s\n", moduleApk)
		}
	}

	// 3. Inspect Input package
	p.stage(2, "Inspecting input package...", 0.40)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pkgInfo, err := InspectInput(p.opts.InputPath, workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect input: %w", err)
	}

	if pkgInfo.IsAlreadyPatched {
		p.logf("      [+] Detected LSPatch Portable patched package\n")
		p.logf("      [*] Extracting embedded original APK...\n")
		unwrapDir := filepath.Join(workDir, "unwrapped_origin")
		cleanPkgInfo, unwrapErr := UnwrapPatchedPackage(pkgInfo, unwrapDir)
		if unwrapErr != nil {
			p.logf("      [!] Failed to extract original APK: %v\n", unwrapErr)
			return nil, fmt.Errorf("failed to unwrap already patched package: %w", unwrapErr)
		}
		if cleanPkgInfo.IsSplit {
			p.logf("      [+] Restored clean original package (%d split files: base + %d splits)\n", cleanPkgInfo.TotalApks, len(cleanPkgInfo.SplitApkPaths))
		} else {
			p.logf("      [+] Restored clean original package (%s)\n", filepath.Base(cleanPkgInfo.BaseApkPath))
		}
		p.logf("      [+] Continuing with normal patch pipeline...\n")
		pkgInfo = cleanPkgInfo
	}

	pkgLabel := pkgInfo.PackageName
	if pkgLabel == "" {
		pkgLabel = "unknown"
	}
	verLabel := pkgInfo.VersionName
	if verLabel == "" {
		verLabel = "unknown"
	}

	p.logf("      - Package: %s\n", pkgLabel)
	p.logf("      - Version: %s\n", verLabel)
	if pkgInfo.IsSplit {
		p.logf("      - Structure: Split APK (%d files: base + %d splits)\n", pkgInfo.TotalApks, len(pkgInfo.SplitApkPaths))
	} else {
		p.logf("      - Structure: Single Standalone APK\n")
	}

	if !pkgInfo.IsSystemWebView {
		p.logf("      [!] Error: %s\n", pkgInfo.UnsupportedReason)
		return nil, fmt.Errorf("不支持该浏览器内核: %s", pkgInfo.UnsupportedReason)
	}

	if pkgLabel != "com.dena.skyleap" {
		p.logf("      [!] Warning: Detected package %q differs from official SkyLeap (com.dena.skyleap).\n", pkgLabel)
	} else {
		p.logf("      [+] Validated official SkyLeap target.\n")
	}

	// Automatic Backup of original APK before patching
	var backupPath string
	var backupDir string
	if p.opts.AutoBackup {
		backupDir = p.opts.BackupDir
		if backupDir == "" {
			backupDir = filepath.Join(config.GetBaseDir(), "backups")
		}
		if err := os.MkdirAll(backupDir, 0755); err == nil {
			safePkg := pkgLabel
			if !IsValidPackageName(safePkg) {
				safePkg = "app"
			}
			safeVer := sanitizeVersionName(verLabel)
			ts := time.Now().Format("20060102_150405")
			origExt := filepath.Ext(p.opts.InputPath)
			if origExt == "" || strings.Contains(origExt, "/") || strings.Contains(origExt, "\\") || strings.Contains(origExt, "..") {
				origExt = ".apk"
			}
			backupFileName := fmt.Sprintf("%s_v%s_%s_original%s", safePkg, safeVer, ts, origExt)
			destBackup := filepath.Join(backupDir, backupFileName)
			if !isPathContained(backupDir, destBackup) {
				p.logf("      [!] Warning: Invalid backup path %s escapes backup directory, skipping backup\n", destBackup)
			} else if fi, statErr := os.Stat(p.opts.InputPath); statErr == nil {
				if !fi.IsDir() {
					if copyErr := copyFile(p.opts.InputPath, destBackup); copyErr == nil {
						backupPath = destBackup
						p.logf("      [+] Original package backed up to: %s\n", backupPath)
					}
				} else {
					destBackup = strings.TrimSuffix(destBackup, filepath.Ext(destBackup)) + ".apks"
					if isPathContained(backupDir, destBackup) {
						allSplits := append([]string{pkgInfo.BaseApkPath}, pkgInfo.SplitApkPaths...)
						if err := createApksArchive(allSplits, destBackup); err == nil {
							backupPath = destBackup
							p.logf("      [+] Original split packages backed up to: %s\n", backupPath)
						}
					}
				}
			}
		}
	}

	// 4. Run LSPatch Portable
	p.stage(3, "Executing LSPatch Portable injection...", 0.60)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lspatchOutDir := filepath.Join(workDir, "lspatch_raw_out")
	if err := os.MkdirAll(lspatchOutDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lspatch output dir: %w", err)
	}

	cfg := &LSPatchConfig{
		Context:        ctx,
		JavaBinaryPath: javaPath,
		LSPatchJarPath: lspatchJar,
		ModuleApkPath:  moduleApk,
		AppLabel:       p.opts.AppLabel,
		NewPackageName: p.opts.NewPackageName,
		Verbose:        p.opts.Verbose,
		LogFn: func(line string) {
			p.logf("      %s\n", line)
		},
	}

	rawOutputs, err := ExecuteLSPatch(cfg, pkgInfo.BaseApkPath, pkgInfo.SplitApkPaths, lspatchOutDir)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	p.logf("      [+] Injected SkyLeapModule into %d package file(s).\n", len(rawOutputs))

	// 5. Organize and verify output
	p.stage(4, "Packaging final output...", 0.80)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	baseInputName := filepath.Base(p.opts.InputPath)
	result, err := BundleOutput(pkgInfo.IsSplit, baseInputName, rawOutputs, p.opts.OutputDir)
	if err != nil {
		return nil, err
	}
	if result != nil {
		result.BackupPath = backupPath
		result.BackupDir = backupDir
	}

	p.stage(5, "Performing post-patch integrity audit...", 1.00)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := VerifyIntegrity(result, pkgInfo.IsSplit, pkgInfo.TotalApks); err != nil {
		return nil, fmt.Errorf("output integrity verification failed: %w", err)
	}
	p.logf("      [+] All package artifacts verified successfully.\n")

	printSummary(result)

	return result, nil
}

func printBanner() {
	fmt.Println("=================================================================")
	fmt.Println("       GBF-Accelerator PC Patch Tool v0.1 (CLI)")
	fmt.Println("=================================================================")
	fmt.Println("[*] Notice: This tool DOES NOT bundle or distribute official SkyLeap APKs.")
	fmt.Println("    Users must obtain their own authentic official SkyLeap package.")
	fmt.Println("[*] Local Processing: All operations run 100% locally on your machine.")
	fmt.Println("    No packages, analytics, or user data are ever uploaded or transmitted.")
	fmt.Println("[*] Signature Warning: Patched packages are re-signed with a local key.")
	fmt.Println("    Android strictly forbids overwriting apps signed with different keys.")
	fmt.Println("    You MUST uninstall the official version first before installing.")
	fmt.Println("=================================================================")
}

func printSummary(res *BundleResult) {
	fmt.Println("-----------------------------------------------------------------")
	fmt.Println("Output Artifacts:")
	if res.IsSplit {
		fmt.Printf("  - Split Directory: %s\n", res.SplitDir)
		fmt.Printf("  - Bundle Archive:  %s\n", res.ApksArchive)
		mb := float64(res.TotalBytes) / (1024 * 1024)
		fmt.Printf("  - Total Size:      %.2f MB (%d APK files)\n", mb, res.TotalApks)
		fmt.Println("\nInstallation Options on Android:")
		fmt.Println("  Option A (ADB):")
		fmt.Printf("    adb install-multiple %s%c*.apk\n", res.SplitDir, filepath.Separator)
		fmt.Println("  Option B (Phone Installer):")
		fmt.Printf("    Transfer %s to phone and open with SAI or Shizuku.\n", filepath.Base(res.ApksArchive))
	} else {
		fmt.Printf("  - Patched APK: %s\n", res.SingleApk)
		mb := float64(res.TotalBytes) / (1024 * 1024)
		fmt.Printf("  - Total Size:  %.2f MB\n", mb)
		fmt.Println("\nInstallation Option on Android:")
		fmt.Println("  Option A (ADB):")
		fmt.Printf("    adb install -r %s\n", res.SingleApk)
		fmt.Println("  Option B (Phone):")
		fmt.Printf("    Transfer %s to phone and tap to install.\n", filepath.Base(res.SingleApk))
	}
	fmt.Println("-----------------------------------------------------------------")
	fmt.Println("Next Step: Launch GBF-Accelerator Android, start Go Core, then launch SkyLeap.")
	fmt.Println("=================================================================")
}

func sanitizeVersionName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "unknown") {
		return "1.0"
	}
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else if r == ' ' {
			sb.WriteByte('_')
		}
	}
	res := sb.String()
	res = strings.Trim(res, ".-_")
	for strings.Contains(res, "..") {
		res = strings.ReplaceAll(res, "..", ".")
	}
	if res == "" {
		return "1.0"
	}
	return res
}

func isPathContained(baseDir, targetPath string) bool {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		absBase = filepath.Clean(baseDir)
	}
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		absTarget = filepath.Clean(targetPath)
	}
	cleanBase := filepath.Clean(absBase)
	cleanTarget := filepath.Clean(absTarget)
	if runtime.GOOS == "windows" {
		cleanBase = strings.ToLower(cleanBase)
		cleanTarget = strings.ToLower(cleanTarget)
	}
	rel, err := filepath.Rel(cleanBase, cleanTarget)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
