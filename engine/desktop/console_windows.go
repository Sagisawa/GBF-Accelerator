//go:build windows

package desktop

import (
	"os"
)

var (
	procAttachConsole = modKernel32.NewProc("AttachConsole")
	procGetStdHandle  = modKernel32.NewProc("GetStdHandle")
	procGetFileType   = modKernel32.NewProc("GetFileType")
)

const (
	attachParentProcess = ^uintptr(0) // -1
	stdOutputHandle     = ^uintptr(10) // -11 = STD_OUTPUT_HANDLE
	stdErrorHandle      = ^uintptr(11) // -12 = STD_ERROR_HANDLE
	fileTypeDisk        = 0x0001
	fileTypeChar        = 0x0002
	fileTypePipe        = 0x0003
)

// AttachConsole attaches to the parent console if launched from a terminal.
// This allows GUI-subsystem executables to output to cmd/PowerShell when invoked via CLI.
func AttachConsole() {
	r, _, _ := procAttachConsole.Call(attachParentProcess)
	if r == 0 {
		return
	}

	// Check if stdout was already redirected to a pipe or disk file
	hOut, _, _ := procGetStdHandle.Call(stdOutputHandle)
	if hOut != 0 && hOut != ^uintptr(0) {
		ftOut, _, _ := procGetFileType.Call(hOut)
		if ftOut != fileTypePipe && ftOut != fileTypeDisk {
			if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
				os.Stdout = f
			}
		}
	} else {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stdout = f
		}
	}

	hErr, _, _ := procGetStdHandle.Call(stdErrorHandle)
	if hErr != 0 && hErr != ^uintptr(0) {
		ftErr, _, _ := procGetFileType.Call(hErr)
		if ftErr != fileTypePipe && ftErr != fileTypeDisk {
			if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
				os.Stderr = f
			}
		}
	} else {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			os.Stderr = f
		}
	}
}
