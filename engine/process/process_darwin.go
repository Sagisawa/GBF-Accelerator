//go:build darwin
// +build darwin

package process

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func killProcessOnPort(port int) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-tiTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
	outBytes, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(outBytes)) == 0 {
		return false, nil
	}

	currentPID := os.Getpid()
	lines := strings.Split(strings.TrimSpace(string(outBytes)), "\n")
	pids := make([]int, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if pid, err := strconv.Atoi(l); err == nil {
			if pid > 1 && pid != currentPID {
				pids = append(pids, pid)
			}
		}
	}

	if len(pids) == 0 {
		return false, nil
	}

	pyScriptRe := regexp.MustCompile(`(?:^|[\s/])(gbf_proxy|app_main|gui_main)\.py(?:\s|$)`)
	killedAny := false

	for _, pid := range pids {
		// 1. Check comm
		pCtx, pCancel := context.WithTimeout(context.Background(), 2*time.Second)
		commCmd := exec.CommandContext(pCtx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=")
		commOut, _ := commCmd.Output()
		pCancel()
		commName := filepath.Base(strings.TrimSpace(string(commOut)))

		commLower := strings.ToLower(commName)
		shouldKill := false
		if commLower == "gbf_accelerator" || commLower == "gbf-accelerator" || commLower == "gbf-proxy" || commLower == "gbf_proxy" {
			shouldKill = true
		} else {
			// 2. Check full command line
			cCtx, cCancel := context.WithTimeout(context.Background(), 2*time.Second)
			cmdLineCmd := exec.CommandContext(cCtx, "ps", "-p", strconv.Itoa(pid), "-o", "command=")
			cmdLineOut, err := cmdLineCmd.Output()
			cCancel()
			if err == nil {
				cmdLine := string(cmdLineOut)
				cmdLower := strings.ToLower(cmdLine)
				if pyScriptRe.MatchString(cmdLine) ||
					strings.Contains(cmdLower, "gbf_accelerator") ||
					strings.Contains(cmdLower, "gbf-accelerator") ||
					strings.Contains(cmdLower, "gbf_proxy") ||
					strings.Contains(cmdLower, "gbf-proxy") {
					shouldKill = true
				}
			}
		}

		if shouldKill {
			_ = syscall.Kill(pid, syscall.SIGTERM)
			time.Sleep(100 * time.Millisecond)
			// If still running, force kill
			if err := syscall.Kill(pid, 0); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
			killedAny = true
		}
	}

	if killedAny {
		time.Sleep(200 * time.Millisecond)
	}

	return killedAny, nil
}
