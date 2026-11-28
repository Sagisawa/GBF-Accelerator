package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate free loopback port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestWatchStdinGracefulExit(t *testing.T) {
	tmpDir := t.TempDir()
	exeName := "gbf-engine-test"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	binPath := filepath.Join(tmpDir, exeName)

	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, string(out))
	}

	proxyPort := freeLoopbackPort(t)
	controlPort := freeLoopbackPort(t)
	baseDir := filepath.Join(tmpDir, "data")
	cacheDir := filepath.Join(tmpDir, "cache")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("mkdir baseDir failed: %v", err)
	}
	cfgContent := fmt.Sprintf(
		`{"clean_zombies":false,"auto_system_proxy":false,"enable_ram_warmup":false,"enable_prefetch":false,"direct_mode":true,"cache_dir":%q}`,
		cacheDir,
	)
	if err := os.WriteFile(filepath.Join(baseDir, "config.json"), []byte(cfgContent), 0644); err != nil {
		t.Fatalf("write config.json failed: %v", err)
	}

	cmd := exec.Command(
		binPath,
		"-nogui",
		"-watch-stdin",
		"-base-dir", baseDir,
		"-cache-dir", cacheDir,
		"-proxy-port", fmt.Sprintf("%d", proxyPort),
		"-control-port", fmt.Sprintf("%d", controlPort),
	)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe failed: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start engine binary: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	statusURL := fmt.Sprintf("http://127.0.0.1:%d/api/status", controlPort)
	client := &http.Client{
		Timeout:   500 * time.Millisecond,
		Transport: &http.Transport{Proxy: nil},
	}
	ready := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(statusURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("engine control server did not become ready at %s within 15s\noutput:\n%s", statusURL, outBuf.String())
	}

	if err := stdinPipe.Close(); err != nil {
		t.Fatalf("closing stdin pipe failed: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	select {
	case waitErr := <-waitCh:
		if waitErr != nil {
			t.Fatalf("expected clean exit (code 0) after stdin close, got: %v\noutput:\n%s", waitErr, outBuf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("engine process did not exit within 5s after stdin pipe closed\noutput:\n%s", outBuf.String())
	}
}
