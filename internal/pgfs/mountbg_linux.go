//go:build linux

package pgfs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// MountBackground starts a detached aifs process that mounts the filesystem at
// mountPoint, then waits for the mount to become visible in /proc/self/mounts.
//
// aifsBin is the path to the aifs binary.
// args are the arguments passed after the binary name (e.g. ["-i", "ai01", "mount", "/tmp/ai01"]).
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
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return false
	}
	defer f.Close()

	mp, err := filepath.Abs(mountPoint)
	if err != nil {
		mp = mountPoint
	}

	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "aifs" && strings.HasPrefix(fields[2], "fuse") {
			m, _ := filepath.Abs(fields[1])
			if m == mp {
				return true
			}
		}
	}
	return false
}
