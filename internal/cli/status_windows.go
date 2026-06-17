//go:build windows

package cli

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"

	"github.com/mars-base/aifs/internal/pgfs"
)

// activeAIFSMounts returns the list of aifs mount points recorded in the
// shared state file, pruning entries whose process no longer exists.
func activeAIFSMounts() ([]string, error) {
	records, err := pgfs.ListMountState()
	if err != nil {
		return nil, err
	}

	var mounts []string
	for _, rec := range records {
		if processAlive(rec.PID) {
			mounts = append(mounts, rec.MountPoint)
		} else {
			// Clean up stale record; the sentinel-based unmount already removed
			// it, but if the process died uncleanly the state may be left behind.
			if err := pgfs.RemoveMountState(rec.MountPoint); err != nil {
				fmt.Fprintf(os.Stderr, "warning: removing stale mount state: %v\n", err)
			}
		}
	}
	return mounts, nil
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}
