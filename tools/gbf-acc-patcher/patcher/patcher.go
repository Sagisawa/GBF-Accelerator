package patcher

import (
	"fmt"
	"os"
	"path/filepath"
)

type PatchOptions struct {
	InputPath       string
	OutputDir       string
	JavaOverride    string
	LSPatchOverride string
	ModuleOverride  string
	Verbose         bool
	KeepTemp        bool
}

type Patcher struct {
	opts   PatchOptions
	exeDir string
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
			fmt.Printf("[*] Preserving workspace at: %s\n", workDir)
		}
	}()

	// 2. Discover required toolchain
	fmt.Println("[1/5] Checking environment & toolchain...")
	javaPath, javaVer, err := FindJavaRuntime(p.opts.JavaOverride)
	if err != nil {
		return nil, err
	}
	fmt.Printf("      - Java Runtime: %s (%s)\n", javaPath, javaVer)

	lspatchJar, err := FindLSPatchJar(p.opts.LSPatchOverride, p.exeDir)
	if err != nil {
		return nil, err
	}
	fmt.Printf("      - LSPatch Jar: %s\n", lspatchJar)

	moduleApk, err := FindModuleApk(p.opts.ModuleOverride, p.exeDir)
	if err != nil {
		return nil, err
	}
	fmt.Printf("      - SkyLeapModule: %s\n", moduleApk)

	// 3. Inspect Input package
	fmt.Println("[2/5] Inspecting input package...")
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

	fmt.Printf("      - Package: %s\n", pkgLabel)
	fmt.Printf("      - Version: %s\n", verLabel)
	if pkgInfo.IsSplit {
		fmt.Printf("      - Structure: Split APK (%d files: base + %d splits)\n", pkgInfo.TotalApks, len(pkgInfo.SplitApkPaths))
	} else {
		fmt.Printf("      - Structure: Single Standalone APK\n")
	}

	if pkgLabel != "com.dena.skyleap" {
		fmt.Printf("      [!] Warning: Detected package %q differs from official SkyLeap (com.dena.skyleap).\n", pkgLabel)
	} else {
		fmt.Printf("      [+] Validated official SkyLeap target.\n")
	}

	// 4. Run LSPatch Portable
	fmt.Println("[3/5] Executing LSPatch Portable injection...")
	lspatchOutDir := filepath.Join(workDir, "lspatch_raw_out")
	if err := os.MkdirAll(lspatchOutDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lspatch output dir: %w", err)
	}

	cfg := &LSPatchConfig{
		JavaBinaryPath: javaPath,
		LSPatchJarPath: lspatchJar,
		ModuleApkPath:  moduleApk,
		Verbose:        p.opts.Verbose,
	}

	rawOutputs, err := ExecuteLSPatch(cfg, pkgInfo.BaseApkPath, pkgInfo.SplitApkPaths, lspatchOutDir)
	if err != nil {
		return nil, err
	}
	fmt.Printf("      [+] Injected SkyLeapModule into %d package file(s).\n", len(rawOutputs))

	// 5. Organize and verify output
	fmt.Println("[4/5] Packaging & verifying final output...")
	baseInputName := filepath.Base(p.opts.InputPath)
	result, err := BundleOutput(pkgInfo.IsSplit, baseInputName, rawOutputs, p.opts.OutputDir)
	if err != nil {
		return nil, err
	}

	fmt.Println("[5/5] Success! Patching completed.")
	printSummary(result)

	return result, nil
}

func printBanner() {
	fmt.Println("=================================================================")
	fmt.Println("       GBF-Accelerator PC Patch Tool v0.1 (CLI)")
	fmt.Println("=================================================================")
	fmt.Println("[*] Notice: This tool DOES NOT bundle or distribute official SkyLeap APKs.")
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
