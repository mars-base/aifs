//go:build windows

package podman

// EnsureRepoReadable is a no-op on Windows because host-side chmod does not
// affect files that live inside the WSL filesystem used by podman.
func (m *BackupManager) EnsureRepoReadable() error {
	return nil
}
