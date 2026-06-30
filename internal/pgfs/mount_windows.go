//go:build windows

package pgfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/sys/windows"
)

const uoiName = 2

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procGetProcessWindowStation  = user32.NewProc("GetProcessWindowStation")
	procCloseWindowStation       = user32.NewProc("CloseWindowStation")
	procGetUserObjectInformation = user32.NewProc("GetUserObjectInformationW")
)

func getProcessWindowStation() windows.Handle {
	r, _, _ := procGetProcessWindowStation.Call()
	return windows.Handle(r)
}

func closeWindowStation(h windows.Handle) bool {
	r, _, _ := procCloseWindowStation.Call(uintptr(h))
	return r != 0
}

func getUserObjectInformation(h windows.Handle, nIndex int, pvInfo unsafe.Pointer, nLength uint32, lpnLengthNeeded *uint32) bool {
	r, _, _ := procGetUserObjectInformation.Call(
		uintptr(h),
		uintptr(nIndex),
		uintptr(pvInfo),
		uintptr(nLength),
		uintptr(unsafe.Pointer(lpnLengthNeeded)),
	)
	return r != 0
}

// NormalizeMountPoint ensures a Windows drive-letter mount point has a
// trailing backslash so that filepath.Join produces correct paths.
// "Z:" -> "Z:\", "Z:\" -> "Z:\", "C:\dir" -> "C:\dir".
func NormalizeMountPoint(mountPoint string) string {
	vol := filepath.VolumeName(mountPoint)
	if vol != "" && len(mountPoint) == len(vol) {
		return vol + "\\"
	}
	return mountPoint
}

// MountPathJoin is like filepath.Join(mountPoint, name) but handles bare
// Windows drive letters correctly (Z: + .aifs-mounted -> Z:\.aifs-mounted).
func MountPathJoin(mountPoint, name string) string {
	return filepath.Join(NormalizeMountPoint(mountPoint), name)
}

// toWinFspMountPoint converts a user-supplied mount point into the form
// expected by WinFsp.  A bare drive letter like "Z:" is turned into
// "\\.\\Z:" so that WinFsp uses the Mount Manager and the resulting drive
// letter is visible in all sessions.  Directory paths and already-qualified
// device paths are left unchanged.
func toWinFspMountPoint(mountPoint string) string {
	// Already-qualified device paths (e.g., "\\.\\Z:") pass through unchanged.
	if strings.HasPrefix(strings.ToLower(mountPoint), "\\\\.\\") {
		return mountPoint
	}
	vol := filepath.VolumeName(mountPoint)
	if vol == "" {
		return mountPoint
	}
	rest := mountPoint[len(vol):]
	if rest != "" && rest != "\\" && rest != "/" {
		return mountPoint
	}
	return "\\\\.\\" + vol
}

// Mount mounts the PG-backed filesystem at mountPoint using WinFsp/cgofuse.
// This call blocks until the filesystem is unmounted.
//
// onMounted is called in a separate goroutine shortly after host.Mount()
// begins blocking (WinFsp signals the mount asynchronously). It is never
// called when mounting fails immediately. If nil, it is not called.
func Mount(ctx context.Context, pgURL, tablePrefix, dataPath, mountPoint string, onMounted func()) error {
	dirPath := NormalizeMountPoint(mountPoint)
	db, m, _, err := Open(ctx, pgURL, tablePrefix)
	if err != nil {
		return err
	}
	defer db.Close()

	// Advisory locks are bound to a single database session. Acquire the lock
	// on a dedicated *sql.Conn and hold that connection open for the lifetime
	// of the mount so the lock is not released by the connection pool.
	lockConn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquiring dedicated lock connection: %w", err)
	}
	defer lockConn.Close()

	var locked bool
	if err := lockConn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		return fmt.Errorf("acquiring mount lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("instance is already mounted by another aifs mount")
	}

	// WinFsp directory mounts need the parent to exist, but the mount
	// point itself must NOT exist — WinFsp creates it. Drive letters
	// (e.g. "Z:") go through the Mount Manager and need no directory.
	if !isDriveLetterMount(dirPath) {
		if info, err := os.Stat(dirPath); err == nil {
			if info.IsDir() {
				return fmt.Errorf("mount point %s already exists as a directory; WinFsp requires the mount point to NOT exist — it creates it on mount. Remove the directory first (e.g. rmdir %s) and try again", dirPath, dirPath)
			}
			return fmt.Errorf("mount point %s already exists (not a directory); remove it and try again", dirPath)
		}
		if err := os.MkdirAll(filepath.Dir(dirPath), 0755); err != nil {
			return fmt.Errorf("creating parent of mount point: %w", err)
		}
	}

	fs := &winFS{m: m, dataPath: dataPath}
	host := fuse.NewFileSystemHost(fs)
	fs.host = host

	// Use the Mount Manager form for drive letters so the volume is visible
	// across sessions; directory paths are passed through unchanged.
	winFspMountPoint := toWinFspMountPoint(mountPoint)

	// Directory mounts on Windows only work from an interactive window
	// station (WinSta0). Drive-letter mounts go through the global Mount
	// Manager and work from services/non-interactive contexts.
	if !isDriveLetterMount(dirPath) && !isInteractiveSession() {
		return fmt.Errorf("directory mounts on Windows require an interactive session (WinSta0); use a drive letter such as Z: or run aifs from a logged-on console")
	}

	fmt.Printf("mounted aifs at %s\n", mountPoint)
	if onMounted != nil {
		go func() {
			sentinel := MountPathJoin(mountPoint, SentinelName)
			for range 100 {
				if _, err := os.Stat(sentinel); err == nil {
					onMounted()
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}
	if !host.Mount(winFspMountPoint, nil) {
		return fmt.Errorf("mounting %s failed", winFspMountPoint)
	}
	return nil
}

// Umount unmounts a WinFsp/cgofuse filesystem mounted at mountPoint.
// It uses the .UMOUNTIT cooperative-unmount trick: creating a directory named
// .UMOUNTIT inside the mount triggers the FUSE Mkdir callback to unmount the
// volume. A synthetic sentinel file is used to detect when the volume has gone.
// If the cooperative unmount times out and a background PID was recorded, the
// process is killed as a force fallback.
func Umount(mountPoint string) error {
	sentinel := MountPathJoin(mountPoint, SentinelName)

	// Make sure the filesystem is currently responding before we try to unmount.
	if _, err := os.Stat(sentinel); err != nil {
		return fmt.Errorf("mount point %s does not appear to be an aifs volume: %w", mountPoint, err)
	}

	trigger := MountPathJoin(mountPoint, ".UMOUNTIT")
	// The Mkdir callback will call host.Unmount() and return an error.
	_ = os.Mkdir(trigger, 0755)

	// Wait for the sentinel to disappear, which means the volume is unmounted.
	if waitForSentinelGone(sentinel, 50, 100*time.Millisecond) {
		_ = RemoveMountState(mountPoint)
		fmt.Printf("unmounted %s\n", mountPoint)
		return nil
	}

	// Force fallback: kill the recorded background process if there is one.
	if rec, ok, _ := GetMountState(mountPoint); ok && rec.PID != 0 {
		if p, err := os.FindProcess(rec.PID); err == nil {
			_ = p.Kill()
			_, _ = p.Wait()
		}
	}

	if waitForSentinelGone(sentinel, 30, 100*time.Millisecond) {
		_ = RemoveMountState(mountPoint)
		fmt.Printf("unmounted %s\n", mountPoint)
		return nil
	}
	return fmt.Errorf("umount %s: volume did not unmount in time", mountPoint)
}

func waitForSentinelGone(sentinel string, attempts int, delay time.Duration) bool {
	for range attempts {
		if _, err := os.Stat(sentinel); err != nil {
			return true
		}
		time.Sleep(delay)
	}
	return false
}

// isInteractiveSession reports whether the current process is attached to the
// interactive window station WinSta0. WinFsp directory (pathname) mounts only
// work in interactive sessions; drive-letter mounts are session-independent.
func isInteractiveSession() bool {
	ws := getProcessWindowStation()
	if ws == 0 {
		return false
	}
	defer closeWindowStation(ws)

	var needed uint32
	getUserObjectInformation(ws, uoiName, nil, 0, &needed)
	if needed == 0 {
		return false
	}

	buf := make([]uint16, needed/2)
	if !getUserObjectInformation(ws, uoiName, unsafe.Pointer(&buf[0]), needed, &needed) {
		return false
	}

	return strings.EqualFold(windows.UTF16ToString(buf), "WinSta0")
}

// isDriveLetterMount reports whether path is a bare drive letter (e.g., "Z:"
// or "Z:\") as opposed to a real directory path. WinFsp/cgofuse mounts to
// drive letters directly without needing a pre-existing directory.
func isDriveLetterMount(path string) bool {
	vol := filepath.VolumeName(path)
	if vol == "" {
		return false
	}
	rest := path[len(vol):]
	return rest == "" || rest == "\\" || rest == "/"
}
