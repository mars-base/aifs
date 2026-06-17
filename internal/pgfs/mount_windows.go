//go:build windows

package pgfs

import "errors"

// Umount is a placeholder on Windows. The real implementation will use the
// WinFsp service control utilities.
func Umount(mountPoint string) error {
	return errors.New("umount is not yet implemented on Windows")
}
