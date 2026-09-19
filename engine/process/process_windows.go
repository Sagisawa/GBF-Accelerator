//go:build windows
// +build windows

package process

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func killProcessOnPort(port int) (bool, error) {
	cmd := exec.Command("netstat", "-aon")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	outBytes, err := cmd.Output()
	if err != nil {
		return false, err
	}

	out := string(outBytes)
	pattern := regexp.MustCompile(fmt.Sprintf(`(?i):%d\s+.*LISTENING\s+(\d+)`, port))
	matches := pattern.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return false, nil
	}

	pids := make(map[int]bool)
	currentPID := os.Getpid()
	for _, m := range matches {
		if len(m) >= 2 {
			pid, err := strconv.Atoi(m[1])
			if err == nil && pid > 4 && pid != currentPID {
				pids[pid] = true
			}
		}
	}

	if len(pids) == 0 {
		return false, nil
	}

	killedAny := false
	for pid := range pids {
		// Inspect process info
		tlCmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
		tlCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		tlOutBytes, err := tlCmd.Output()
		if err != nil {
			continue
		}
		procInfo := string(tlOutBytes)
		procLower := strings.ToLower(procInfo)

		shouldKill := false
		if strings.Contains(procLower, "gbf_accelerator") ||
			strings.Contains(procLower, "gbf-accelerator") ||
			strings.Contains(procLower, "gbf_proxy") ||
			strings.Contains(procLower, "gbf-proxy") {
			shouldKill = true
		} else if strings.Contains(procLower, "python") {
			// Query full command line for python scripts
			psCmd := exec.Command("powershell", "-NoProfile", "-Command",
				fmt.Sprintf("(Get-CimInstance Win32_Process -Filter 'ProcessId=%d').CommandLine", pid))
			psCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if psOut, err := psCmd.Output(); err == nil {
				cmdLine := strings.ToLower(string(psOut))
				for _, target := range []string{"gbf_proxy", "app_main", "gui_main", "gbfaccelerator", "gbf-proxy"} {
					if strings.Contains(cmdLine, target) {
						shouldKill = true
						break
					}
				}
			}
		}

		if shouldKill {
			killCmd := exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))
			killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			if err := killCmd.Run(); err == nil {
				killedAny = true
			}
		}
	}

	if killedAny {
		time.Sleep(200 * time.Millisecond)
	}

	return killedAny, nil
}
