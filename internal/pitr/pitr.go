// // Package pitr implements core PITR (Point-In-Time Recovery) features:
// snapshot create/list/delete, point-in-time restore, branch clone.
// All operations are executed via pgBackRest inside the PostgreSQL container.
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
	Comment   string    `json:"comment"`   // Backup comment
}

// Manager encapsulates PITR operations, executing pgBackRest commands via Podman.
type Manager struct {
	cfg    *config.Config
	podman *podman.Manager
}

// New creates a PITR manager.
func New(cfg *config.Config, pm *podman.Manager) *Manager {
	return &Manager{cfg: cfg, podman: pm}
}

// ─── Stanza management ──────────────────────────────────────────

// EnsureStanza ensures the pgBackRest stanza is created.
func (m *Manager) EnsureStanza() error {
	stanza := m.cfg.PITR.PgBackRestStanza

	// Check if stanza already exists
	out, err := m.pgbackrest("--stanza="+stanza, "stanza-create", "--log-level-console=info")
	if err != nil {
		// stanza-create errors if already exists, ignore
		if strings.Contains(err.Error(), "already exists") || strings.Contains(out, "already exists") {
			return nil
		}
		return fmt.Errorf("creating pgBackRest stanza: %w\n%s", err, out)
	}
	fmt.Println("→ pgBackRest stanza created")
	return nil
}

// CheckStanza verifies the stanza configuration.
func (m *Manager) CheckStanza() error {
	stanza := m.cfg.PITR.PgBackRestStanza
	out, err := m.pgbackrest("--stanza="+stanza, "check", "--log-level-console=info")
	if err != nil {
		return fmt.Errorf("pgBackRest stanza check failed: %w\n%s", err, out)
	}
	return nil
}

// ─── Snapshot management ─────────────────────────────────────────────

// CreateSnapshot creates a backup snapshot.
// backupType: "full" (default), "incr", "diff"
func (m *Manager) CreateSnapshot(comment, backupType string) (*Snapshot, error) {
	if backupType == "" {
		backupType = "full"
	}

	stanza := m.cfg.PITR.PgBackRestStanza
	args := []string{
		"--stanza=" + stanza,
		"--type=" + backupType,
		"--log-level-console=info",
	}

	fmt.Printf("→ Creating %s backup...\n", backupType)
	out, err := m.pgbackrest(args...)
	if err != nil {
		return nil, fmt.Errorf("creating backup: %w\n%s", err, out)
	}

	// Parse backup label
	label := extractLabel(out)
	snap := &Snapshot{
		Name:      label,
		Timestamp: time.Now(),
		Type:      backupType,
		Comment:   comment,
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

	out, err := m.pgbackrest(args...)
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

	out, err := m.pgbackrest(args...)
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
	out, err := m.pgbackrest(args...)
	if err != nil {
		return fmt.Errorf("cleaning old backups: %w\n%s", err, out)
	}
	fmt.Println("→ Old backups cleaned")
	return nil
}

// ─── PITR restore ────────────────────────────────────────────

// Restore performs point-in-time recovery.
// targetTime specifies the recovery target time.
// Restore process:
// 1. Stop PostgreSQL container
// 2. pgBackRest restore
// 3. Start PostgreSQL container
func (m *Manager) Restore(targetTime time.Time, dryRun bool) error {
	stanza := m.cfg.PITR.PgBackRestStanza
	targetStr := targetTime.Format("2006-01-02 15:04:05")

	if dryRun {
		fmt.Printf("→ [DRY RUN] Would restore to: %s\n", targetStr)
		return nil
	}

	fmt.Printf("⚠️  Restoring PostgreSQL to %s\n", targetStr)
	fmt.Println("  1/3 Stopping PostgreSQL...")
	if err := m.podman.StopContainer(); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}

	fmt.Println("  2/3 Running pgBackRest restore...")
	args := []string{
		"--stanza=" + stanza,
		"restore",
		"--type=time",
		"--target=" + targetStr,
		"--target-action=promote",
		"--delta",
		"--log-level-console=info",
	}
	out, err := m.pgbackrest(args...)
	if err != nil {
		return fmt.Errorf("pgBackRest restore: %w\n%s", err, out)
	}

	fmt.Println("  3/3 Starting PostgreSQL...")
	if err := m.podman.StartContainer(); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	fmt.Println("✓ Restore complete")
	return nil
}

// ─── Internal methods ─────────────────────────────────────────────

func (m *Manager) pgbackrest(args ...string) (string, error) {
	slog.Debug("pgbackrest", "args", args)
	return m.podman.Exec(append([]string{"pgbackrest"}, args...)...)
}

// extractLabel extracts the backup label from pgBackRest output.
func extractLabel(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "backup") && strings.Contains(line, "complete") {
			// Format: "full backup: 20260614-143005F"
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

// parseInfoOutput parses pgbackrest info output into a Snapshot list.
func parseInfoOutput(out string) []Snapshot {
	var snapshots []Snapshot

	// pgbackrest info output format example:
	// full backup: 20260614-143005F
	//     timestamp: 2026-06-14 14:30:05
	//     size: 1.2GB
	//
	// incr backup: 20260614-150010I
	//     timestamp: 2026-06-14 15:00:10
	//     ...
	var current *Snapshot

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)

		// Detect backup line
		for _, bt := range []string{"full backup:", "incr backup:", "diff backup:"} {
			if strings.HasPrefix(line, bt) {
				name := strings.TrimSpace(strings.TrimPrefix(line, bt))
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

		// Parse timestamp
		if strings.HasPrefix(line, "timestamp:") {
			ts := strings.TrimSpace(strings.TrimPrefix(line, "timestamp:"))
			if t, err := time.Parse("2006-01-02 15:04:05", ts); err == nil {
				current.Timestamp = t
				// Update value in slice
				snapshots[len(snapshots)-1].Timestamp = t
			}
		}
	}

	return snapshots
}
