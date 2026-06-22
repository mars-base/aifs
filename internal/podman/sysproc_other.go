//go:build !windows

package podman

import "os/exec"

func hideWindowWindows(cmd *exec.Cmd) {
	// no-op on non-Windows platforms
}
