//go:build darwin

package cli

import (
	"context"
	"strings"

	"github.com/mars-base/aifs/internal/pgfs"
	"golang.org/x/sys/unix"
)

// activeAIFSMounts returns FUSE mount points for the given instance.
//
// Primary path (v1.2.x+): use the shared mount-state file which records
// instance names alongside mount points.
//
// Fallback (old-style mounts without state file): scan Getfsstat for all aifs
// entries. If the instance has a PG advisory lock held (confirmed via
// IsInstanceMounted), the full list is returned as a best-effort approximation.
func activeAIFSMounts(ctx context.Context, instance, pgURL, tablePrefix string) ([]string, error) {
	if instance != "" {
		records, err := pgfs.ListMountState()
		if err != nil {
			return nil, err
		}
		if len(records) > 0 {
			var mounts []string
			for _, rec := range records {
				if rec.Instance == instance {
					mounts = append(mounts, rec.MountPoint)
				}
			}
			return mounts, nil
		}
		// State file empty: fall back to Getfsstat + advisory-lock check.
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
