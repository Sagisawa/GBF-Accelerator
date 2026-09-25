package patcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gbf-proxy/config"
)

type PatchListener interface {
	OnStage(stage int, text string, progress float64)
	OnLog(line string)
}

type PatchOptions struct {
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
	printBanner()

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
	javaPath, javaVer, err := FindJavaRuntime(p.opts.JavaOverride)
	if err != nil {
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

	moduleApk, err := FindModuleApk(p.opts.ModuleOverride, p.exeDir)
	if err != nil {
		return nil, err
	}
	if err := ValidateModuleApk(moduleApk); err != nil {
		return nil, err
	}
	p.logf("      - SkyLeapModule: %s\n", moduleApk)

	// 3. Inspect Input package
	p.stage(2, "Inspecting input package...", 0.40)
	pkgInfo, err := InspectInput(p.opts.InputPath, workDir)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect input: %w", err)
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

	if pkgLabel != "com.dena.skyleap" {
		p.logf("      [!] Warning: Detected package %q differs from official SkyLeap (com.dena.skyleap).\n", pkgLabel)
	} else {
		p.logf("      [+] Validated official SkyLeap target.\n")
	}

	if p.opts.AppLabel != "" {
		p.logf("      - Custom Launcher Label: %s\n", p.opts.AppLabel)
	}
	if p.opts.NewPackageName != "" {
		p.logf("      [i] Notice: LSPatch maintains original package name to ensure component integrity. Retained package: %s\n", pkgLabel)
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
			if safePkg == "" || safePkg == "unknown" {
				safePkg = "app"
			}
			safeVer := verLabel
			if safeVer == "" || safeVer == "unknown" {
				safeVer = "1.0"
			}
			safeVer = strings.ReplaceAll(safeVer, " ", "_")
			ts := time.Now().Format("20060102_150405")
			origExt := filepath.Ext(p.opts.InputPath)
			if origExt == "" {
				origExt = ".apk"
			}
			backupFileName := fmt.Sprintf("%s_v%s_%s_original%s", safePkg, safeVer, ts, origExt)
			destBackup := filepath.Join(backupDir, backupFileName)
			if fi, statErr := os.Stat(p.opts.InputPath); statErr == nil && !fi.IsDir() {
				if copyErr := copyFile(p.opts.InputPath, destBackup); copyErr == nil {
					backupPath = destBackup
					p.logf("      [+] Original package backed up to: %s\n", backupPath)
				}
			}
		}
	}

	// 4. Run LSPatch Portable
	p.stage(3, "Executing LSPatch Portable injection...", 0.60)
	lspatchOutDir := filepath.Join(workDir, "lspatch_raw_out")
	if err := os.MkdirAll(lspatchOutDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lspatch output dir: %w", err)
	}

	cfg := &LSPatchConfig{
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
		return nil, err
	}
	p.logf("      [+] Injected SkyLeapModule into %d package file(s).\n", len(rawOutputs))

	// 5. Organize and verify output
	p.stage(4, "Packaging final output...", 0.80)
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
