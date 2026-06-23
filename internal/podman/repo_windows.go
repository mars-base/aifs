//go:build windows

package podman

// EnsureRepoReadable is a legacy no-op.
//
// Previously the backup container ran pgbackrest as root while the PG container
// ran archive-get as the postgres user (different host uids under rootless
// podman in WSL), so repo files written by root were not readable by postgres
// and had to be chmod-relaxed via `wsl --exec chmod -R a+rX`. That recursive
// chmod produced a flood of "Operation not permitted" errors (rootless subuid
// mapping prevents chmod of files owned by another mapped uid) that drowned
// out the restore log.
//
// Now both the backup container and the PG container run pgbackrest as the
// postgres user (uid 999 -> same host uid via rootless podman subuid mapping),
// so repo files are owned by postgres and are directly readable/writable. The
// createBackupContainer step also chowns existing repo files to postgres on
// every (re)creation.
//
// Kept as a no-op for call-site compatibility; callers (pitr.go) ignore its
// error.
func (m *BackupManager) EnsureRepoReadable() error {
	return nil
}
