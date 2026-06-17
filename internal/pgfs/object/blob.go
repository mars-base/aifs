// Package object provides a PostgreSQL-backed blob store extracted from
// JuiceFS's pkg/object/sql.go. It stores opaque blobs in a single table
// using upsert semantics.
package object

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// BlobStore is a simple key/blob object store.
type BlobStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte) error
	Delete(ctx context.Context, key string) error
}

// PGStore implements BlobStore on top of PostgreSQL.
type PGStore struct {
	db    *sql.DB
	table string
}

// NewPGStore creates a blob store backed by the given table.
func NewPGStore(db *sql.DB, table string) *PGStore {
	return &PGStore{db: db, table: table}
}

// Get returns the blob for key, or sql.ErrNoRows if it does not exist.
func (s *PGStore) Get(ctx context.Context, key string) ([]byte, error) {
	var data []byte
	query := fmt.Sprintf("SELECT data FROM %s WHERE key = $1", s.table)
	err := s.db.QueryRowContext(ctx, query, []byte(key)).Scan(&data)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Put writes a blob, replacing any existing blob with the same key.
func (s *PGStore) Put(ctx context.Context, key string, data []byte) error {
	query := fmt.Sprintf(`
		INSERT INTO %s (key, size, modified, data)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key) DO UPDATE
		SET size = EXCLUDED.size,
		    modified = EXCLUDED.modified,
		    data = EXCLUDED.data
	`, s.table)
	_, err := s.db.ExecContext(ctx, query, []byte(key), int64(len(data)), time.Now().UTC(), data)
	return err
}

// Delete removes a blob.
func (s *PGStore) Delete(ctx context.Context, key string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE key = $1", s.table)
	_, err := s.db.ExecContext(ctx, query, []byte(key))
	return err
}
