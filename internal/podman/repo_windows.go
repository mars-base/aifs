//go:build windows

package podman

import (
	"fmt"
	"os/exec"
)

// EnsureRepoReadable recursively ensures the backup repository is readable by
// other users. On Windows the repository lives in the WSL ext4 filesystem used
// by podman, so we must run chmod inside WSL rather than on the Windows side.
func (m *BackupManager) EnsureRepoReadable() error {
	repoDir := m.cfg.Backup.DataDir
	if repoDir == "" {
		return nil
	}

	wslPath := hostMountPath(repoDir)
	if wslPath == "" {
		return fmt.Errorf("could not determine WSL path for backup repo %s", repoDir)
	}

	distro := wslDistro()
	cmd := exec.Command("wsl", "-d", distro, "--exec", "chmod", "-R", "a+rX", wslPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chmod backup repo %s (wsl): %w (output: %s)", wslPath, err, string(out))
	}
	return nil
}
