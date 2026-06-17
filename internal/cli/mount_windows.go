//go:build windows

package cli

import "errors"

func mountInBackground(mountPoint string) error {
	return errors.New("background mount is not yet implemented on Windows")
}

// mountVisible is always false on Windows until WinFsp integration is complete.
func mountVisible(mountPoint string) bool {
	return false
}
