//go:build windows

package pgfs

import (
	"context"
	"errors"
)

// errNotImplemented is returned by Windows stubs that have not been replaced
// with the WinFsp/cgofuse implementation yet.
var errNotImplemented = errors.New("FUSE filesystem is not yet implemented on Windows")

// Mount is a placeholder on Windows. The real implementation lives in
// mount_windows.go once WinFsp/cgofuse integration is complete.
func Mount(ctx context.Context, pgURL, tablePrefix, dataPath, mountPoint string) error {
	return errNotImplemented
}
