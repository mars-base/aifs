//go:build darwin

package cli

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strings"

	"github.com/mars-base/aifs/internal/pgfs"
	"golang.org/x/sys/unix"
)

// activeAIFSMounts returns FUSE mount points for the given instance.
//
// Primary path (v1.2.x+): use the shared mount-state file which records
// instance names alongside mount points.
//
// Fallback (old-style mounts without state file): use ps(1) to scan all
// running processes for aifs mount invocations, parse -i and mountpoint.
// If the process-scan yields no results, fall back to Getfsstat + advisory-
// lock confirmation (least precise, but handles edge cases).
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

		// State file empty: reconstruct from running processes via ps(1).
		procMounts := psFUSEInstanceMounts()
		if mps, ok := procMounts[instance]; ok {
			return mps, nil
		}
		if len(procMounts) > 0 {
			// Other instances are mounted but not ours.
			return nil, nil
		}

		// ps scan found nothing (no aifs mount processes visible).
		// Last resort: Getfsstat + advisory-lock check.
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

// psFUSEInstanceMounts uses ps(1) to find aifs mount processes and returns
// an instance → []mountpoint map.  macOS has no /proc, so we use ps -axww
// which lists all processes with full argument strings.
func psFUSEInstanceMounts() map[string][]string {
	result := make(map[string][]string)

	out, err := exec.Command("ps", "-axww", "-o", "args=").Output()
	if err != nil {
		return result
	}

	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Split shell-style but we only need space-split for our purposes
		// (aifs paths and mount points don't contain spaces in practice).
		args := strings.Fields(line)
		inst, mp := parseAIFSArgs(args)
		if inst == "" || mp == "" {
			continue
		}
		result[inst] = append(result[inst], mp)
	}
	return result
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
