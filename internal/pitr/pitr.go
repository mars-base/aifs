// Package pitr implements core PITR (Point-In-Time Recovery) features:
// snapshot create/list/delete, point-in-time restore, branch clone.
// pgBackRest operations are executed inside the shared backup container via BackupExec.
package pitr

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/podman"
)

// Snapshot represents a pgBackRest backup snapshot.
type Snapshot struct {
	Name      string    `json:"name"`      // Backup label
	Timestamp time.Time `json:"timestamp"` // Backup time
	Type      string    `json:"type"`      // full / incr / diff
	Size      int64     `json:"size"`      // Backup size (bytes)
}

// Manager encapsulates PITR operations.
// pgBackRest commands run in the shared backup container (via BackupManager),
// while PG container lifecycle (stop/start) uses the podman Manager.
type Manager struct {
	cfg    *config.Config
	podman *podman.Manager       // PG container lifecycle
	backup *podman.BackupManager // pgBackRest operations (in backup container)
}

// New creates a PITR manager.
func New(cfg *config.Config, pm *podman.Manager, bm *podman.BackupManager) *Manager {
	return &Manager{cfg: cfg, podman: pm, backup: bm}
}

// ─── Stanza management ──────────────────────────────────────────

// EnsureStanza ensures the pgBackRest stanza is created.
func (m *Manager) EnsureStanza() error {
	stanza := m.cfg.PITR.PgBackRestStanza

	// Check if stanza already exists
	out, err := m.pgbackrest(false, "--stanza="+stanza, "stanza-create", "--log-level-console=info")
	if err != nil {
		// stanza-create errors if already exists, ignore
		if !strings.Contains(err.Error(), "already exists") && !strings.Contains(out, "already exists") {
			_ = m.backup.EnsureRepoReadable()
			return fmt.Errorf("creating pgBackRest stanza: %w\n%s", err, out)
		}
	} else {
		fmt.Println("→ pgBackRest stanza created")
	}

	// Make sure repo files are readable by the postgres user during archive-get.
	if err := m.backup.EnsureRepoReadable(); err != nil {
		fmt.Printf("  ⚠ repo readability warning: %v\n", err)
	}
	return nil
}

// CheckStanza verifies the stanza configuration.
func (m *Manager) CheckStanza() error {
	stanza := m.cfg.PITR.PgBackRestStanza
	out, err := m.pgbackrest(false, "--stanza="+stanza, "check", "--log-level-console=info")
	if err != nil {
		return fmt.Errorf("pgBackRest stanza check failed: %w\n%s", err, out)
	}
	return nil
}

// ─── Snapshot management ─────────────────────────────────────────────

// CreateSnapshot creates a backup snapshot.
// backupType: "full" (default), "incr", "diff"
// tailLogs streams the backup container output to stdout during the backup.
func (m *Manager) CreateSnapshot(backupType string, tailLogs bool) (*Snapshot, error) {
	if backupType == "" {
		backupType = "full"
	}

	stanza := m.cfg.PITR.PgBackRestStanza
	args := []string{
		"--stanza=" + stanza,
		"backup",
		"--type=" + backupType,
		"--log-level-console=info",
	}

	fmt.Printf("→ Creating %s backup...\n", backupType)
	out, err := m.pgbackrest(tailLogs, args...)
	if err != nil {
		_ = m.backup.EnsureRepoReadable()
		return nil, fmt.Errorf("creating backup: %w\n%s", err, out)
	}

	// Make sure repo files are readable by the postgres user during archive-get/recovery.
	if err := m.backup.EnsureRepoReadable(); err != nil {
		fmt.Printf("  ⚠ repo readability warning: %v\n", err)
	}

	// Parse backup label
	label := extractLabel(out)
	snap := &Snapshot{
		Name:      label,
		Timestamp: time.Now(),
		Type:      backupType,
	}
	fmt.Printf("  ✓ Snapshot created: %s (%s)\n", label, backupType)
	return snap, nil
}

// ListSnapshots lists all backups.
func (m *Manager) ListSnapshots(limit int) ([]Snapshot, error) {
	stanza := m.cfg.PITR.PgBackRestStanza
	args := []string{
		"--stanza=" + stanza,
		"info",
		"--log-level-console=info",
	}

	out, err := m.pgbackrest(false, args...)
	if err != nil {
		return nil, fmt.Errorf("listing backups: %w\n%s", err, out)
	}

	snapshots := parseInfoOutput(out)
	if limit > 0 && limit < len(snapshots) {
		snapshots = snapshots[:limit]
	}
	return snapshots, nil
}

// DeleteSnapshot deletes a specific backup.
func (m *Manager) DeleteSnapshot(name string) error {
	stanza := m.cfg.PITR.PgBackRestStanza
	args := []string{
		"--stanza=" + stanza,
		"expire",
		"--set=" + name,
		"--log-level-console=info",
	}

	out, err := m.pgbackrest(false, args...)
	if err != nil {
		return fmt.Errorf("deleting backup %s: %w\n%s", name, err, out)
	}
	fmt.Printf("→ Snapshot %s deleted\n", name)
	return nil
}

// DeleteBefore deletes backups older than a specified time.
func (m *Manager) DeleteBefore(before time.Time) error {
	stanza := m.cfg.PITR.PgBackRestStanza
	args := []string{
		"--stanza=" + stanza,
		"expire",
		"--log-level-console=info",
	}

	// pgBackRest expire uses retention config to auto-handle old backups
	out, err := m.pgbackrest(false, args...)
	if err != nil {
		return fmt.Errorf("cleaning old backups: %w\n%s", err, out)
	}
	fmt.Println("→ Old backups cleaned")
	return nil
}

// ─── PITR restore ────────────────────────────────────────────

// Restore performs point-in-time recovery.
// targetTime specifies the recovery target time.
// dryRun only prints the plan. tailLogs streams the restore container output to stdout.
// Restore process:
// 1. Stop PostgreSQL container
// 2. pgBackRest restore in a temporary container mounting the same data directory
// 3. Start PostgreSQL container
func (m *Manager) Restore(targetTime time.Time, dryRun bool, tailLogs bool) error {
	stanza := m.cfg.PITR.PgBackRestStanza
	targetStr := targetTime.Format("2006-01-02 15:04:05-07")

	if dryRun {
		fmt.Printf("→ [DRY RUN] Would restore to: %s\n", targetStr)
		return nil
	}

	fmt.Printf("⚠️  Restoring PostgreSQL to %s\n", targetStr)

	// Ensure repo is readable by postgres user during recovery archive-get.
	if err := m.backup.EnsureRepoReadable(); err != nil {
		fmt.Printf("  ⚠ repo readability warning: %v\n", err)
	}

	fmt.Println("  1/3 Stopping PostgreSQL...")
	if err := m.podman.StopContainer(); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}

	fmt.Println("  2/3 Running pgBackRest restore...")
	out, err := m.podman.RunRestoreContainer(stanza, targetStr, tailLogs)
	if err != nil {
		return fmt.Errorf("pgBackRest restore: %w\n%s", err, out)
	}

	// Restore may create repo files owned by root; make them readable again.
	if err := m.backup.EnsureRepoReadable(); err != nil {
		fmt.Printf("  ⚠ repo readability warning: %v\n", err)
	}

	fmt.Println("  3/3 Starting PostgreSQL...")
	if err := m.podman.StartContainer(); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// PG container IP may have changed; refresh backup container /etc/hosts so
	// subsequent backups can reach it.
	if err := m.backup.EnsureBackupInfra(); err != nil {
		fmt.Printf("  ⚠ backup infra refresh warning: %v\n", err)
	}

	fmt.Println("✓ Restore complete")
	return nil
}

// ─── Internal methods ─────────────────────────────────────────────

func (m *Manager) pgbackrest(tailLogs bool, args ...string) (string, error) {
	slog.Debug("pgbackrest", "args", args)
	return m.backup.BackupExec(tailLogs, append([]string{"pgbackrest"}, args...)...)
}

// extractLabel extracts the backup label from pgBackRest output.
func extractLabel(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"full backup:", "incr backup:", "diff backup:"} {
			if name, ok := strings.CutPrefix(line, prefix); ok {
				return strings.TrimSpace(name)
			}
		}
		if _, after, ok := strings.Cut(line, "new backup label ="); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// parseInfoOutput parses pgbackrest info output into a Snapshot list.
func parseInfoOutput(out string) []Snapshot {
	var snapshots []Snapshot

	// pgbackrest info output format example:
	// full backup: 20260614-143005F
	//     timestamp start/stop: 2026-06-14 14:30:05+00 / 2026-06-14 14:30:10+00
	//     database size: 30MB, database backup size: 30MB
	//
	// incr backup: 20260614-150010I
	//     timestamp start/stop: 2026-06-14 15:00:10+00 / 2026-06-14 15:00:15+00
	//     ...
	var current *Snapshot

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)

		// Detect backup line
		for _, bt := range []string{"full backup:", "incr backup:", "diff backup:"} {
			if name, ok := strings.CutPrefix(line, bt); ok {
				name = strings.TrimSpace(name)
				current = &Snapshot{
					Name: name,
					Type: strings.TrimSuffix(bt, " backup:"),
				}
				snapshots = append(snapshots, *current)
				continue
			}
		}

		if current == nil {
			continue
		}

		// Parse timestamp. pgbackrest info uses "timestamp start/stop: <start> / <stop>"
		if ts, ok := strings.CutPrefix(line, "timestamp start/stop:"); ok {
			// ts is " <start>+00 / <stop>+00"
			parts := strings.SplitN(ts, " / ", 2)
			if len(parts) > 0 {
				start := strings.TrimSpace(parts[0])
				if t, err := time.Parse("2006-01-02 15:04:05Z07", start); err == nil {
					current.Timestamp = t
					// Update value in slice
					snapshots[len(snapshots)-1].Timestamp = t
				}
			}
		}
	}

	return snapshots
}
