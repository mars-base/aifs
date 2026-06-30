//go:build linux

package cli

import (
	"bufio"
	"context"
	"os"
	"strings"

	"github.com/mars-base/aifs/internal/pgfs"
)

// activeAIFSMounts returns FUSE mount points for the given instance.
//
// Uses pgfs.FindInstanceMounts for the primary path (state file) and the
// process-scan fallback.  As a final resort for very old mounts where neither
// the state file nor any aifs process is visible, falls back to
// /proc/self/mounts + advisory-lock confirmation.
func activeAIFSMounts(ctx context.Context, instance, pgURL, tablePrefix string) ([]string, error) {
	if instance != "" {
		mounts, err := pgfs.FindInstanceMounts(instance)
		if err != nil {
			return nil, err
		}
		if len(mounts) > 0 {
			return mounts, nil
		}

		// Last resort: /proc/self/mounts scan + advisory-lock check.
		// Handles the edge case where the mount process is already gone but the
		// kernel mount is still alive (e.g. zombie mount after a crash).
		all, err := procAIFSMounts()
		if err != nil || len(all) == 0 {
			return nil, err
		}
		mounted, _ := pgfs.IsInstanceMounted(ctx, pgURL, tablePrefix)
		if mounted {
			return all, nil
		}
		return nil, nil
	}

	return procAIFSMounts()
}

func procAIFSMounts() ([]string, error) {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mounts []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 4 {
			continue
		}
		if fields[0] == "aifs" && strings.HasPrefix(fields[2], "fuse") {
			mounts = append(mounts, fields[1])
		}
	}
	return mounts, s.Err()
}
