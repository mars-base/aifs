//go:build darwin

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func mountInBackground(mountPoint string) error {
	args := []string{os.Args[0]}
	if cfgPath != "" {
		args = append(args, "-c", cfgPath)
	}
	args = append(args, "-i", cfgInstance, "mount", mountPoint)

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
	p, err := os.StartProcess(os.Args[0], args, attr)
	if err != nil {
		return fmt.Errorf("starting background mount: %w", err)
	}

	// Wait for the mount to become visible in the macOS mount table.
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if mountVisible(mountPoint) {
			fmt.Printf("background mount pid %d at %s\n", p.Pid, mountPoint)
			fmt.Println("note: mount runs under your user session and will be lost on logout; re-mount after logging back in")
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

// mountVisible reports whether mountPoint is currently mounted as an aifs FUSE filesystem.
func mountVisible(mountPoint string) bool {
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
