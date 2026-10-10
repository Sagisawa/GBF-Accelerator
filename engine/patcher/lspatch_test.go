package patcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckJavaVersion_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// Use any dummy path or non-existent path
	_, _, err := checkJavaVersion(ctx, "java")
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected cancellation error, got %v", err)
	}
}

func TestFindJavaRuntimeContext_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, _, err := FindJavaRuntimeContext(ctx, "")
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") && !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected cancellation error, got %v", err)
	}
}

func TestExecuteLSPatchContext_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	tempDir := t.TempDir()
	dummyJar := filepath.Join(tempDir, "lspatch.jar")
	dummyMod := filepath.Join(tempDir, "module.apk")
	dummyBase := filepath.Join(tempDir, "base.apk")
	_ = os.WriteFile(dummyJar, []byte("jar"), 0644)
	_ = os.WriteFile(dummyMod, []byte("mod"), 0644)
	_ = os.WriteFile(dummyBase, []byte("base"), 0644)

	cfg := &LSPatchConfig{
		Context:        ctx,
		JavaBinaryPath: "java",
		LSPatchJarPath: dummyJar,
		ModuleApkPath:  dummyMod,
	}

	_, err := ExecuteLSPatch(cfg, dummyBase, nil, tempDir)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") && !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout or cancellation error, got %v", err)
	}
}

func TestExecuteLSPatchContext_ExplicitContextHelper(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tempDir := t.TempDir()
	dummyJar := filepath.Join(tempDir, "lspatch.jar")
	dummyMod := filepath.Join(tempDir, "module.apk")
	dummyBase := filepath.Join(tempDir, "base.apk")
	_ = os.WriteFile(dummyJar, []byte("jar"), 0644)
	_ = os.WriteFile(dummyMod, []byte("mod"), 0644)
	_ = os.WriteFile(dummyBase, []byte("base"), 0644)

	cfg := &LSPatchConfig{
		JavaBinaryPath: "java",
		LSPatchJarPath: dummyJar,
		ModuleApkPath:  dummyMod,
	}

	_, err := ExecuteLSPatchContext(ctx, cfg, dummyBase, nil, tempDir)
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") && !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout or cancellation error, got %v", err)
	}
}

func TestPatcherRunContext_Cancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	opts := PatchOptions{
		Context:   ctx,
		InputPath: "dummy.apk",
		OutputDir: t.TempDir(),
	}

	p, err := NewPatcher(opts)
	if err != nil {
		t.Fatalf("failed to create patcher: %v", err)
	}

	_, err = p.RunContext(ctx)
	if err == nil {
		t.Fatal("expected cancellation error from RunContext, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestPatcherRun_TimeoutOption(t *testing.T) {
	// With an already expired / 1ns timeout, Run() should fail immediately with context.DeadlineExceeded
	opts := PatchOptions{
		Timeout:   1 * time.Nanosecond,
		InputPath: "dummy.apk",
		OutputDir: t.TempDir(),
	}

	p, err := NewPatcher(opts)
	if err != nil {
		t.Fatalf("failed to create patcher: %v", err)
	}

	time.Sleep(2 * time.Millisecond) // ensure deadline has passed
	_, err = p.Run()
	if err == nil {
		t.Fatal("expected deadline exceeded error from Run, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestExecuteLSPatch_ContextWithoutDeadlineEnforcesTimeout(t *testing.T) {
	// When cfg.Context is context.Background() (has no deadline),
	// ExecuteLSPatch must enforce DefaultLSPatchTimeout and not hang indefinitely.
	tempDir := t.TempDir()
	dummyJar := filepath.Join(tempDir, "lspatch.jar")
	dummyMod := filepath.Join(tempDir, "module.apk")
	dummyBase := filepath.Join(tempDir, "base.apk")
	_ = os.WriteFile(dummyJar, []byte("jar"), 0644)
	_ = os.WriteFile(dummyMod, []byte("mod"), 0644)
	_ = os.WriteFile(dummyBase, []byte("base"), 0644)

	cfg := &LSPatchConfig{
		Context:        context.Background(), // non-nil but no deadline
		JavaBinaryPath: "non-existent-java-path-for-test-xyz",
		LSPatchJarPath: dummyJar,
		ModuleApkPath:  dummyMod,
	}

	_, err := ExecuteLSPatch(cfg, dummyBase, nil, tempDir)
	if err == nil {
		t.Fatal("expected error with non-existent java binary, got nil")
	}
}

func TestExecuteLSPatchContext_NilContextAppliesDefaultTimeout(t *testing.T) {
	tempDir := t.TempDir()
	dummyJar := filepath.Join(tempDir, "lspatch.jar")
	dummyMod := filepath.Join(tempDir, "module.apk")
	dummyBase := filepath.Join(tempDir, "base.apk")
	_ = os.WriteFile(dummyJar, []byte("jar"), 0644)
	_ = os.WriteFile(dummyMod, []byte("mod"), 0644)
	_ = os.WriteFile(dummyBase, []byte("base"), 0644)

	cfg := &LSPatchConfig{
		JavaBinaryPath: "non-existent-java-path-for-test-xyz",
		LSPatchJarPath: dummyJar,
		ModuleApkPath:  dummyMod,
	}

	_, err := ExecuteLSPatchContext(nil, cfg, dummyBase, nil, tempDir)
	if err == nil {
		t.Fatal("expected error with non-existent java binary, got nil")
	}
}

