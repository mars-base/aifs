//go:build darwin

package pgfs

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
)

// FindInstanceMounts returns the active FUSE mount points for the given
// instance name on macOS.  State file first, then ps(1) fallback.
//
// If instance is empty, all aifs FUSE mount points are returned.
func FindInstanceMounts(instance string) ([]string, error) {
	records, err := ListMountState()
	if err != nil {
		return nil, err
	}
	if len(records) > 0 {
		var out []string
		for _, r := range records {
			if instance == "" || r.Instance == instance {
				out = append(out, r.MountPoint)
			}
		}
		return out, nil
	}

	// Fallback: scan running processes via ps(1).
	procMap := psFUSEInstanceMounts()
	if instance == "" {
		var out []string
		for _, mps := range procMap {
			out = append(out, mps...)
		}
		return out, nil
	}
	return procMap[instance], nil
}

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
		args := strings.Fields(line)
		inst, mp := parseAIFSArgs(args)
		if inst != "" && mp != "" {
			result[inst] = append(result[inst], mp)
		}
	}
	return result
}
