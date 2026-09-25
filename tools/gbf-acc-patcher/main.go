package main

import (
	"flag"
	"fmt"
	"os"

	"gbf-proxy/patcher"
)

func main() {
	var (
		outputDir       string
		javaOverride    string
		lspatchOverride string
		moduleOverride  string
		verbose         bool
		keepTemp        bool
	)

	flag.StringVar(&outputDir, "o", "./output", "Target directory for patched APK output")
	flag.StringVar(&outputDir, "output", "./output", "Target directory for patched APK output")
	flag.StringVar(&javaOverride, "java", "", "Path to Java 21+ binary (default: auto-detected from JAVA_HOME or JBR)")
	flag.StringVar(&lspatchOverride, "lspatch", "", "Path to lspatch.jar (default: auto-detected)")
	flag.StringVar(&moduleOverride, "module", "", "Path to SkyLeapModule APK (default: auto-detected)")
	flag.BoolVar(&verbose, "v", false, "Enable verbose output from LSPatch")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose output from LSPatch")
	flag.BoolVar(&keepTemp, "keep-temp", false, "Do not delete temporary workspace directory on exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <input.apk | input.apks | directory>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s skyleap.apks -o ./patched/\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s skyleap.apk -o ./patched/\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s ./skyleap_splits/ -o ./patched/\n", os.Args[0])
	}

	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Error: missing input file or directory.\n\n")
		flag.Usage()
		os.Exit(1)
	}

	inputPath := args[0]

	opts := patcher.PatchOptions{
		InputPath:       inputPath,
		OutputDir:       outputDir,
		JavaOverride:    javaOverride,
		LSPatchOverride: lspatchOverride,
		ModuleOverride:  moduleOverride,
		Verbose:         verbose,
		KeepTemp:        keepTemp,
	}

	p, err := patcher.NewPatcher(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Configuration error: %v\n", err)
		os.Exit(1)
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n[-] Patching failed: %v\n", err)
		os.Exit(1)
	}
}
