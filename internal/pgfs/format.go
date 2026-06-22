package pgfs

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/mars-base/aifs/internal/pgfs/meta"
)

// Format initializes a new PG-backed filesystem in the given database.
func Format(ctx context.Context, pgURL, volumeName, tablePrefix string, force bool) (*meta.FormatInfo, error) {
	db, m, _, err := Open(ctx, pgURL, tablePrefix)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Windows has no real uid/gid concept; os.Getuid()/os.Getgid() return -1
	// there, which cast to uint32 becomes 0xffffffff and overflows the int4
	// uid/gid columns. Use 0 on Windows (matching fs_windows.go, which sets
	// Uid/Gid to 0 for all created nodes).
	uid, gid := uint32(0), uint32(0)
	if runtime.GOOS != "windows" {
		uid, gid = uint32(os.Getuid()), uint32(os.Getgid())
	}

	info, err := m.Init(ctx, volumeName, uid, gid, force)
	if err != nil {
		return nil, fmt.Errorf("format failed: %w", err)
	}
	return info, nil
}
