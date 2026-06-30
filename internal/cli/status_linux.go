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
// /dev/fuse open, extracts the -i flag and positional mountpoint argument, and
// returns a map of instance name → []mountpoint.
//
// This handles the legacy case (mounts created before the state file was
// introduced). It relies on the CLI conventions:
//
//	aifs -i <name> mount <mountpoint>
//	aifs --instance <name> mount <mountpoint>
//	aifs -c <cfg> -i <name> mount <mountpoint>
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
		// Check if this process has /dev/fuse open.
		if !hasFUSEFD(pid) {
			continue
		}

		cmdline, err := os.ReadFile(filepath.Join("/proc", pid, "cmdline"))
		if err != nil {
			continue
		}
		inst, mountpoint := parseAIFSCmdline(cmdline)
		if inst == "" || mountpoint == "" {
			continue
		}
		result[inst] = append(result[inst], mountpoint)
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

// parseAIFSCmdline parses a NUL-separated /proc/<pid>/cmdline and returns the
// -i / --instance value and the positional argument that follows "mount".
// Returns ("", "") if this is not an aifs mount command.
func parseAIFSCmdline(raw []byte) (instance, mountpoint string) {
	args := bytes.Split(raw, []byte{0})
	if len(args) == 0 {
		return
	}
	// Verify this is an aifs process.
	exe := string(args[0])
	if !strings.HasSuffix(filepath.Base(exe), "aifs") {
		return
	}
	// Scan for "mount" subcommand and -i / --instance flag.
	strs := make([]string, 0, len(args))
	for _, a := range args {
		if s := string(a); s != "" {
			strs = append(strs, s)
		}
	}
	var hasMountCmd bool
	for i := 1; i < len(strs); i++ {
		s := strs[i]
		switch {
		case s == "mount":
			hasMountCmd = true
		case (s == "-i" || s == "--instance") && i+1 < len(strs):
			instance = strs[i+1]
			i++
		case strings.HasPrefix(s, "-i") && len(s) > 2:
			instance = s[2:]
		case strings.HasPrefix(s, "--instance="):
			instance = strings.TrimPrefix(s, "--instance=")
		}
	}
	if !hasMountCmd {
		return "", ""
	}
	// The mountpoint is the first non-flag argument after "mount".
	foundMount := false
	for i := 1; i < len(strs); i++ {
		s := strs[i]
		if !foundMount {
			if s == "mount" {
				foundMount = true
			}
			continue
		}
		// Skip flags and their values.
		if strings.HasPrefix(s, "-") {
			// Consume next arg if this flag takes a value.
			if s == "-d" || s == "--background" || s == "-l" || s == "--list" {
				continue
			}
			i++ // skip flag value
			continue
		}
		mountpoint = s
		return
	}
	return "", ""
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
