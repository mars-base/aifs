//go:build !windows

package platform

import "os/exec"

func hideConsoleWindowWindows(cmd *exec.Cmd) {
	// no-op on non-Windows platforms
}
