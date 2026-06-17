//go:build windows

package pgfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// Mount mounts the PG-backed filesystem at mountPoint using WinFsp/cgofuse.
// This call blocks until the filesystem is unmounted.
func Mount(ctx context.Context, pgURL, tablePrefix, dataPath, mountPoint string) error {
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

	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return fmt.Errorf("creating mount point: %w", err)
	}

	fs := &winFS{m: m, dataPath: dataPath}
	host := fuse.NewFileSystemHost(fs)
	fs.host = host

	fmt.Printf("mounted aifs at %s\n", mountPoint)
	if !host.Mount(mountPoint, nil) {
		return fmt.Errorf("mounting %s failed", mountPoint)
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
	sentinel := filepath.Join(mountPoint, SentinelName)

	// Make sure the filesystem is currently responding before we try to unmount.
	if _, err := os.Stat(sentinel); err != nil {
		return fmt.Errorf("mount point %s does not appear to be an aifs volume: %w", mountPoint, err)
	}

	trigger := filepath.Join(mountPoint, ".UMOUNTIT")
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
