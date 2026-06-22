//go:build windows

package podman

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func hideWindowWindows(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}
