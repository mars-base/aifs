package pgfs

import (
	"context"
	"database/sql"
	"fmt"

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
func IsInstanceMounted(ctx context.Context, pgURL, tablePrefix string) (bool, error) {
	db, _, _, err := Open(ctx, pgURL, tablePrefix)
	if err != nil {
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
