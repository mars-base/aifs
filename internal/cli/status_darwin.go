//go:build darwin

package cli

import (
	"context"
	"strings"

	"github.com/mars-base/aifs/internal/pgfs"
	"golang.org/x/sys/unix"
)

// activeAIFSMounts returns FUSE mount points for the given instance on macOS.
//
// Uses pgfs.FindInstanceMounts for the primary path (state file) and the
// ps(1)-based process-scan fallback.  As a final resort, falls back to
// Getfsstat + advisory-lock confirmation.
func activeAIFSMounts(ctx context.Context, instance, pgURL, tablePrefix string) ([]string, error) {
	if instance != "" {
		mounts, err := pgfs.FindInstanceMounts(instance)
		if err != nil {
			return nil, err
		}
		if len(mounts) > 0 {
			return mounts, nil
		}

		// Last resort: Getfsstat scan + advisory-lock check.
		all, err := getfsstatAIFSMounts()
		if err != nil || len(all) == 0 {
			return nil, err
		}
		mounted, _ := pgfs.IsInstanceMounted(ctx, pgURL, tablePrefix)
		if mounted {
			return all, nil
		}
		return nil, nil
	}

	return getfsstatAIFSMounts()
}

func getfsstatAIFSMounts() ([]string, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		return nil, nil
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	var mounts []string
	for i := range n {
		source := unix.ByteSliceToString(buf[i].Mntfromname[:])
		fstype := strings.ToLower(unix.ByteSliceToString(buf[i].Fstypename[:]))
		if source == "aifs" && strings.Contains(fstype, "fuse") {
			mounts = append(mounts, unix.ByteSliceToString(buf[i].Mntonname[:]))
		}
	}
	return mounts, nil
}
