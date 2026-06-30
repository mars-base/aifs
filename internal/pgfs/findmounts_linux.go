//go:build linux

package pgfs

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindInstanceMounts returns the active FUSE mount points for the given
// instance name.  It uses the state file first (with liveness validation),
// then falls back to scanning /proc/*/cmdline for aifs mount processes.
//
// If instance is empty, all aifs FUSE mount points are returned regardless
// of which instance owns them.
func FindInstanceMounts(instance string) ([]string, error) {
	records, err := ListMountState()
	if err != nil {
		return nil, err
	}
	if len(records) > 0 {
		// Validate each record against /proc/self/mounts to prune stale entries.
		live, err := procSelfMountSet()
		if err != nil {
			return nil, err
		}
		var out []string
		for _, r := range records {
			if !live[r.MountPoint] {
				// Stale entry: process died without cleaning up state file.
				if rmErr := RemoveMountState(r.MountPoint); rmErr != nil {
					fmt.Fprintf(os.Stderr, "warning: removing stale mount state: %v\n", rmErr)
				}
				continue
			}
			if instance == "" || r.Instance == instance {
				out = append(out, r.MountPoint)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}

	// Fallback: reconstruct from running aifs processes.
	procMap := procFUSEInstanceMounts()
	if instance == "" {
		var out []string
		for _, mps := range procMap {
			out = append(out, mps...)
		}
		return out, nil
	}
	return procMap[instance], nil
}

// procSelfMountSet returns the set of aifs FUSE mount points currently visible
// in /proc/self/mounts.
func procSelfMountSet() (map[string]bool, error) {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	set := make(map[string]bool)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		// fields: device mountpoint fstype ...
		if fields[0] == "aifs" && strings.HasPrefix(fields[2], "fuse") {
			set[fields[1]] = true
		}
	}
	return set, sc.Err()
}

// procFUSEInstanceMounts scans /proc/<pid>/cmdline for processes that hold
// /dev/fuse and look like aifs mount invocations.
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
		parts := bytes.Split(raw, []byte{0})
		args := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := string(p); s != "" {
				args = append(args, s)
			}
		}
		inst, mp := parseAIFSArgs(args)
		if inst != "" && mp != "" {
			result[inst] = append(result[inst], mp)
		}
	}
	return result
}

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
