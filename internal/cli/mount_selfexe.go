//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
)

// selfExePath returns the absolute path to the running binary. It first tries
// os.Executable (resolves symlinks), then falls back to PATH lookup so that
// os.StartProcess can always find the binary regardless of how it was invoked
// (e.g. plain "aifs" on PATH, relative path, or via shell pipe).
func selfExePath() (string, error) {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
		if err == nil {
			return exe, nil
		}
	}
	// Fallback: look up the binary name on PATH.
	return exec.LookPath(filepath.Base(os.Args[0]))
}
