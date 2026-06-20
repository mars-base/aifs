//go:build darwin

package cli

import (
	"strings"

	"golang.org/x/sys/unix"
)

// activeAIFSMounts returns local FUSE mount points whose source is "aifs".
func activeAIFSMounts() ([]string, error) {
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
