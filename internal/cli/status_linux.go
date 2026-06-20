//go:build linux

package cli

import (
	"bufio"
	"os"
	"strings"
)

// activeAIFSMounts returns local FUSE mount points whose source is "aifs".
func activeAIFSMounts() ([]string, error) {
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
