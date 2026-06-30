//go:build windows

package pgfs

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// FindInstanceMounts returns the active FUSE mount points for the given
// instance name on Windows.  Windows always uses the state file, so no
// process-scan fallback is needed.
//
// If instance is empty, all recorded (and alive) mount points are returned.
func FindInstanceMounts(instance string) ([]string, error) {
	records, err := ListMountState()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range records {
		if !winProcessAlive(r.PID) {
			if rmErr := RemoveMountState(r.MountPoint); rmErr != nil {
				fmt.Fprintf(os.Stderr, "warning: removing stale mount state: %v\n", rmErr)
			}
			continue
		}
		if instance == "" || r.Instance == instance {
			out = append(out, r.MountPoint)
		}
	}
	return out, nil
}

func winProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}
