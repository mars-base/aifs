package podman

import (
	"os/exec"
	"runtime"
)

// hideWindow configures cmd so that it does not pop up a console window on
// Windows. On a detached process (e.g. the background aifs mount, which runs
// with DETACHED_PROCESS and owns no console), every podman/wsl/netsh child
// would otherwise allocate its own console window, producing a flurry of
// flashing black windows during mount/start. CREATE_NO_WINDOW prevents the
// child from creating a console at all; HideWindow hides any transient one.
//
// On Linux/macOS this is a no-op (there are no console windows to hide).
func hideWindow(cmd *exec.Cmd) {
	if runtime.GOOS != "windows" {
		return
	}
	hideWindowWindows(cmd)
}
