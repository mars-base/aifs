//go:build !windows

package podman

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnsureRepoReadable recursively ensures the backup repository is readable by
// other users. This is required because the PostgreSQL container runs archive-get
// as the postgres user (different host UID than the backup container root).
func (m *BackupManager) EnsureRepoReadable() error {
	repoDir := m.cfg.Backup.DataDir
	if repoDir == "" {
		return nil
	}

	if err := os.Chmod(repoDir, 0755); err != nil {
		return fmt.Errorf("chmod backup repo %s: %w", repoDir, err)
	}

	return filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.Chmod(path, info.Mode()|0555)
		}
		return os.Chmod(path, info.Mode()|0444)
	})
}
