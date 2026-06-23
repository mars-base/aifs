package pgfs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mars-base/aifs/internal/pgfs/meta"
	"github.com/mars-base/aifs/internal/pgfs/object"
)

// advisoryLockKey is used with PostgreSQL advisory locks to ensure only one
// aifs mount is active per database at a time.
const advisoryLockKey int64 = 0x41494653 // "AIFS"

// Open opens a metadata + blob store pair for the given PostgreSQL URL.
func Open(ctx context.Context, pgURL, tablePrefix string) (*sql.DB, meta.MetadataStore, object.BlobStore, error) {
	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("opening postgres: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, nil, nil, fmt.Errorf("pinging postgres: %w", err)
	}
	schema := meta.NewSchema(tablePrefix)
	blob := object.NewPGStore(db, schema.Blob)
	m := meta.NewDB(db, blob, schema)
	return db, m, blob, nil
}

// IsInstanceMounted reports whether another aifs mount is currently holding
// the advisory lock for this PostgreSQL database.
//
// If PostgreSQL is unreachable (container stopped, crashed, or still starting
// up after a failed restore), there cannot be an active aifs mount -- a mount
// process also needs a live PG connection to hold the advisory lock. In that
// case we return (false, nil) rather than an error, so that `aifs restore`
// can proceed to repair the broken cluster instead of being blocked by a
// connectivity check that is moot when PG is down.
func IsInstanceMounted(ctx context.Context, pgURL, tablePrefix string) (bool, error) {
	db, _, _, err := Open(ctx, pgURL, tablePrefix)
	if err != nil {
		if isPGUnreachable(err) {
			return false, nil
		}
		return false, err
	}
	defer db.Close()

	lockConn, err := db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("acquiring lock connection: %w", err)
	}
	defer lockConn.Close()

	var locked bool
	if err := lockConn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		return false, fmt.Errorf("checking mount lock: %w", err)
	}
	if !locked {
		// Another session holds the lock.
		return true, nil
	}

	// We acquired the lock just to probe; release it immediately.
	_, _ = lockConn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	return false, nil
}

// isPGUnreachable reports whether err represents a PostgreSQL connectivity
// failure (connection refused, no route, DNS failure, timeout, or the server
// is still starting up) rather than an application-level error. When PG is
// unreachable there can be no active aifs mount, so callers treat this as
// "not mounted" instead of a hard error.
func isPGUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused",
		"connectex: no connection", // Windows: actively refused
		"no such host",
		"no route to host",
		"network is unreachable",
		"i/o timeout",
		"connection reset",
		"eof",                       // server accepted then closed (starting up)
		"database system is starting up",
		"the database system is starting up",
		"server closed the connection unexpectedly",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
