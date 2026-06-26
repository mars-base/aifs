//go:build windows

package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"

	"github.com/mars-base/aifs/internal/pgfs"
)

// mountInBackground starts a detached aifs process that mounts the filesystem
// at mountPoint. It waits for the synthetic sentinel file to appear before
// returning, and records the mount in the shared state file.
func mountInBackground(mountPoint string) error {
	creationFlags := uint32(windows.CREATE_NEW_PROCESS_GROUP)
	if isDriveLetterMount(mountPoint) {
		creationFlags |= windows.DETACHED_PROCESS
	}
	return mountInBackgroundWithFlags(mountPoint, creationFlags)
}

// mountInBackgroundWithFlags lets the caller control the Windows process
// creation flags. Drive-letter mounts tolerate DETACHED_PROCESS, while
// directory pathname mounts need a regular process so WinFsp can access the
// current session/window station.
func mountInBackgroundWithFlags(mountPoint string, creationFlags uint32) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determining executable: %w", err)
	}

	args := []string{exe}
	if cfgPath != "" {
		args = append(args, "-c", cfgPath)
	}
	if mountURL != "" {
		args = append(args, "mount", "--url", mountURL, "--prefix", mountPrefix, mountPoint)
	} else {
		args = append(args, "-i", cfgInstance, "mount", mountPoint)
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
		Env:   os.Environ(), // inherit full environment (includes CONTAINER_HOST)
		Files: []*os.File{null, logFile, logFile},
		Sys: &windows.SysProcAttr{
			CreationFlags: creationFlags,
		},
	}

	p, err := os.StartProcess(exe, args, attr)
	if err != nil {
		return fmt.Errorf("starting background mount: %w", err)
	}

	// Wait for the background mount to become visible. WinFsp drive-letter
	// mounts can take a few seconds to initialise before the FUSE filesystem
	// starts responding to requests, so we poll with a generous timeout.
	for range 150 {
		time.Sleep(200 * time.Millisecond)
		visible := mountVisible(mountPoint)
		if visible {
			rec := pgfs.MountRecord{
				MountPoint: mountPoint,
				Instance:   cfgInstance,
				PID:        p.Pid,
				StartedAt:  time.Now().UTC(),
			}
			if err := pgfs.AddMountState(rec); err != nil {
				fmt.Fprintf(os.Stderr, "warning: recording mount state: %v\n", err)
			}
			fmt.Printf("background mount pid %d at %s\n", p.Pid, mountPoint)
			fmt.Println("note: mount runs under your user session and will be lost on logout; re-mount after logging back in")
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

// isDriveLetterMount reports whether mountPoint is a Windows drive letter
// (e.g. "Z:" or "Z:\") rather than a directory path.
func isDriveLetterMount(mp string) bool {
	mp = strings.TrimSpace(mp)
	if mp == "" {
		return false
	}
	mp = strings.TrimSuffix(mp, `\`)
	if len(mp) != 2 || mp[1] != ':' {
		return false
	}
	c := mp[0]
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// mountVisible reports whether mountPoint currently hosts a live aifs volume
// by checking for the synthetic sentinel file.
func mountVisible(mountPoint string) bool {
	_, err := os.Stat(pgfs.MountPathJoin(mountPoint, pgfs.SentinelName))
	return err == nil
}
