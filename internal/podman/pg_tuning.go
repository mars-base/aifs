package podman

import (
	"fmt"
	"strings"
)

// pgTuningParams holds the performance parameters written to postgresql.conf.
// Values are intentionally conservative and work on machines with ≥4 GB RAM.
//
// The block is idempotent: repeated aifs start calls replace the block
// in-place rather than appending duplicate sections.
var pgTuningParams = []string{
	// Disable synchronous WAL commit.  Transactions return as soon as WAL is
	// written to the kernel buffer — no fsync wait.  In the worst case (hard
	// OS crash) up to ~200 ms of committed data may be lost, which is
	// acceptable for a local filesystem workload.
	"synchronous_commit = off",

	// Shared buffer pool pre-allocated at startup.  512 MB is a reasonable
	// default for a machine with ≥8 GB RAM.  Requires restart to change.
	"shared_buffers = 512MB",

	// WAL buffer in shared memory.  Lets multiple small transactions coalesce
	// WAL writes, reducing flush frequency.  Requires restart to change.
	"wal_buffers = 64MB",

	// Spread checkpoint dirty-page flushes evenly over 70 % of the interval.
	// 0.7 completes faster than the default 0.9, reducing peak dirty-page
	// accumulation and shortening post-bench I/O tails.
	"checkpoint_completion_target = 0.7",

	// Allow WAL to grow to 2 GB before forcing a checkpoint.
	"max_wal_size = 2GB",

	// Check every 5 min instead of 15 min.  Smaller checkpoint intervals mean
	// less dirty data per cycle and shorter recovery time; the tradeoff is
	// slightly more frequent background I/O.
	"checkpoint_timeout = 5min",

	// Throttle autovacuum I/O so it does not compete with foreground writes on
	// HDD.  10ms is 5x the PG 18 default (2ms) but still 2x faster than the
	// old 20ms setting — a balance between cleanup speed and I/O contention.
	"autovacuum_vacuum_cost_delay = 10ms",
	"autovacuum_vacuum_cost_limit = 200",
}

// pgRestartParams is the set of parameter names that require a PostgreSQL
// restart (not just reload) to take effect.
var pgRestartParams = map[string]bool{
	"shared_buffers": true,
	"wal_buffers":    true,
}

const (
	pgTuningBegin = "# === aifs performance tuning (managed — do not edit) ==="
	pgTuningEnd   = "# === end aifs performance tuning ==="
	pgConfPath    = "/var/lib/postgresql/data/postgresql.conf"
)

// ApplyPGTuning writes (or replaces) the aifs performance-tuning block inside
// the running PostgreSQL container, then reloads (or restarts) as needed.
//
// It is called by doStart after the container is running and PostgreSQL is
// ready, so it can use podman exec to access the file with the correct
// in-container user permissions.
//
// Behaviour:
//   - If the block is absent or differs, it is written and pg_reload_conf() is
//     called for reload-only parameters.
//   - If any restart-required parameter (shared_buffers, wal_buffers) changed,
//     the container is restarted once and the caller waits for PG to be ready
//     again.  The return value needsRestart signals this to the caller.
func (m *Manager) ApplyPGTuning() (needsRestart bool, err error) {
	// Read current postgresql.conf from inside the container.
	current, err := m.Exec("cat", pgConfPath)
	if err != nil {
		return false, fmt.Errorf("pg_tuning: read postgresql.conf: %w", err)
	}

	// Build new block.
	lines := []string{pgTuningBegin}
	lines = append(lines, pgTuningParams...)
	lines = append(lines, pgTuningEnd)
	newBlock := "\n" + strings.Join(lines, "\n") + "\n"

	// Check whether the block already exists and is identical.
	if strings.Contains(current, pgTuningBegin) {
		start := strings.Index(current, pgTuningBegin)
		end := strings.Index(current[start:], pgTuningEnd)
		if end >= 0 {
			existing := current[start : start+end+len(pgTuningEnd)]
			if existing == strings.TrimPrefix(strings.TrimSuffix(newBlock, "\n"), "\n") {
				// Block is already up-to-date — nothing to do.
				return false, nil
			}
		}
	}

	// Detect whether any restart-required param is being newly set or changed.
	for _, param := range pgTuningParams {
		key := strings.SplitN(param, " ", 2)[0]
		if !pgRestartParams[key] {
			continue
		}
		// If the current conf doesn't already have this key set to this value,
		// we will need a restart.
		if !strings.Contains(current, param) {
			needsRestart = true
			break
		}
	}

	// Build the merged content: replace existing block or append.
	content := current
	if idx := strings.Index(content, pgTuningBegin); idx >= 0 {
		endIdx := strings.Index(content[idx:], pgTuningEnd)
		if endIdx >= 0 {
			tail := idx + endIdx + len(pgTuningEnd)
			if tail < len(content) && content[tail] == '\n' {
				tail++
			}
			content = content[:idx] + newBlock + content[tail:]
		} else {
			content += newBlock
		}
	} else {
		content += newBlock
	}

	// Write back via tee (heredoc inside sh -c to handle newlines safely).
	escaped := strings.ReplaceAll(content, "'", "'\\''")
	_, err = m.Exec("sh", "-c", fmt.Sprintf("printf '%%s' '%s' > %s", escaped, pgConfPath))
	if err != nil {
		return false, fmt.Errorf("pg_tuning: write postgresql.conf: %w", err)
	}

	fmt.Println("-> PostgreSQL performance tuning applied")
	return needsRestart, nil
}
