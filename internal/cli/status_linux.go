//go:build linux

package cli

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/mars-base/aifs/internal/pgfs"
)

// activeAIFSMounts returns FUSE mount points for the given instance.
//
// Primary path (v1.2.x+): use the shared mount-state file which records
// instance names alongside mount points.
//
// Fallback (old-style mounts without state file): scan /dev/fuse holders in
// /proc to reconstruct instance→mountpoint mappings from process cmdlines.
// This handles the common case where mounts were created before state tracking
// was added. If the cmdline-based scan yields no results we fall back to
// returning all aifs FUSE entries from /proc/self/mounts.
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

		// State file is empty: try to reconstruct from /proc cmdlines.
		procMounts := procFUSEInstanceMounts()
		if mps, ok := procMounts[instance]; ok {
			return mps, nil
		}
		// procMounts had entries for other instances but none for ours:
		// this instance is not currently mounted.
		if len(procMounts) > 0 {
			return nil, nil
		}

		// procFUSEInstanceMounts returned nothing (no aifs FUSE processes found at
		// all). Fall back to /proc/self/mounts scan + advisory-lock confirmation.
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

// procFUSEInstanceMounts scans /proc/*/cmdline for aifs processes that hold
// /dev/fuse open, parses -i and mountpoint, and returns instance→[]mountpoint.
func procFUSEInstanceMounts() map[string][]string {
	result := make(map[string][]string)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return result
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		if !hasFUSEFD(pid) {
			continue
		}

		raw, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
		if err != nil {
			continue
		}
		// /proc/<pid>/cmdline is NUL-separated; split and strip empties.
		parts := bytes.Split(raw, []byte{0})
		args := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := string(p); s != "" {
				args = append(args, s)
			}
		}

		inst, mp := parseAIFSArgs(args)
		if inst == "" || mp == "" {
			continue
		}
		result[inst] = append(result[inst], mp)
	}
	return result
}

// hasFUSEFD returns true if the process with the given PID string has a
// symbolic link in /proc/<pid>/fd pointing to /dev/fuse.
func hasFUSEFD(pid string) bool {
	fds, err := os.ReadDir(filepath.Join("/proc", pid, "fd"))
	if err != nil {
		return false
	}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc", pid, "fd", fd.Name()))
		if err != nil {
			continue
		}
		if target == "/dev/fuse" {
			return true
		}
	}
	return false
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
