//go:build windows

package pgfs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// MountBackground starts a detached aifs process on Windows that mounts the
// filesystem at mountPoint, then waits for the sentinel file to appear.
func MountBackground(aifsBin string, args []string, mountPoint string) error {
	creationFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP)
	if isDriveLetter(mountPoint) {
		creationFlags |= windows.DETACHED_PROCESS
	}

	logFile, err := os.CreateTemp("", "aifs-mount-*.log")
	if err != nil {
		return fmt.Errorf("creating mount log: %w", err)
	}

	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening NUL: %w", err)
	}
	defer null.Close()

	attr := &os.ProcAttr{
		Env:   os.Environ(),
		Files: []*os.File{null, logFile, logFile},
		Sys: &windows.SysProcAttr{
			CreationFlags: creationFlags,
		},
	}
	fullArgs := append([]string{aifsBin}, args...)
	p, err := os.StartProcess(aifsBin, fullArgs, attr)
	if err != nil {
		return fmt.Errorf("starting background mount: %w", err)
	}

	for range 150 {
		time.Sleep(200 * time.Millisecond)
		if mountBgVisible(mountPoint) {
			_ = os.Remove(logFile.Name())
			return nil
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
	_, err := os.Stat(MountPathJoin(mountPoint, SentinelName))
	return err == nil
}

func isDriveLetter(mp string) bool {
	mp = strings.TrimSpace(mp)
	mp = strings.TrimSuffix(mp, `\`)
	if len(mp) != 2 || mp[1] != ':' {
		return false
	}
	c := mp[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}
