//go:build darwin

package pgfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// MountBackground starts a detached aifs process that mounts the filesystem at
// mountPoint, then waits for the mount to become visible in the macOS mount table.
func MountBackground(aifsBin string, args []string, mountPoint string) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening /dev/null: %w", err)
	}
	defer devNull.Close()

	logFile, err := os.CreateTemp("", "aifs-mount-*.log")
	if err != nil {
		return fmt.Errorf("creating mount log: %w", err)
	}
	defer logFile.Close()

	attr := &os.ProcAttr{
		Files: []*os.File{devNull, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
	fullArgs := append([]string{aifsBin}, args...)
	p, err := os.StartProcess(aifsBin, fullArgs, attr)
	if err != nil {
		return fmt.Errorf("starting background mount: %w", err)
	}

	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if mountBgVisible(mountPoint) {
			_ = os.Remove(logFile.Name())
			return nil
		}
		if err := p.Signal(syscall.Signal(0)); err != nil {
			break
		}
	}
	_ = p.Kill()
	_, _ = p.Wait()
	logOut, _ := os.ReadFile(logFile.Name())
	_ = os.Remove(logFile.Name())
	if len(logOut) > 0 {
		return fmt.Errorf("background mount did not become ready: %s", logOut)
	}
	return fmt.Errorf("background mount did not become ready")
}

func mountBgVisible(mountPoint string) bool {
	mp, err := filepath.Abs(mountPoint)
	if err != nil {
		mp = mountPoint
	}

	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return false
	}
	buf := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return false
	}
	for i := range n {
		source := unix.ByteSliceToString(buf[i].Mntfromname[:])
		fstype := strings.ToLower(unix.ByteSliceToString(buf[i].Fstypename[:]))
		if source == "aifs" && strings.Contains(fstype, "fuse") {
			mntPt := unix.ByteSliceToString(buf[i].Mntonname[:])
			if mntPt == mp {
				return true
			}
		}
	}
	return false
}
