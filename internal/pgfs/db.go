// Package pgfs provides a minimal PostgreSQL-backed filesystem extracted
// from JuiceFS's PostgreSQL object/blob storage ideas.
package pgfs

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mars-base/aifs/internal/pgfs/meta"
	"github.com/mars-base/aifs/internal/pgfs/object"
)

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
