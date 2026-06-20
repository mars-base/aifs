package meta

import (
	"context"
	"database/sql"
	"fmt"
)

// Schema holds the table names for a PG-backed filesystem.
type Schema struct {
	Prefix  string
	Setting string
	Counter string
	Inode   string
	Dentry  string
	Chunk   string
	Symlink string
	Blob    string
}

// NewSchema builds table names from the given prefix.
func NewSchema(prefix string) *Schema {
	if prefix == "" {
		prefix = "aifs_"
	}
	return &Schema{
		Prefix:  prefix,
		Setting: prefix + "setting",
		Counter: prefix + "counter",
		Inode:   prefix + "inode",
		Dentry:  prefix + "dentry",
		Chunk:   prefix + "chunk",
		Symlink: prefix + "symlink",
		Blob:    prefix + "blob",
	}
}

// DDL returns the CREATE TABLE / INDEX statements.
func (s *Schema) DDL() []string {
	return []string{
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name TEXT PRIMARY KEY,
			value JSONB NOT NULL
		)`, s.Setting),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			name TEXT PRIMARY KEY,
			value BIGINT NOT NULL
		)`, s.Counter),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			ino BIGINT PRIMARY KEY,
			kind SMALLINT NOT NULL,
			mode INT NOT NULL,
			uid INT NOT NULL DEFAULT 0,
			gid INT NOT NULL DEFAULT 0,
			size BIGINT NOT NULL DEFAULT 0,
			nlink INT NOT NULL DEFAULT 1,
			atime TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			mtime TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			ctime TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`, s.Inode),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			parent BIGINT NOT NULL REFERENCES %s(ino) ON DELETE CASCADE,
			name TEXT NOT NULL,
			child BIGINT NOT NULL REFERENCES %s(ino) ON DELETE CASCADE,
			PRIMARY KEY (parent, name)
		)`, s.Dentry, s.Inode, s.Inode),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s(child)`, s.Dentry+"_child_idx", s.Dentry),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			ino BIGINT NOT NULL REFERENCES %s(ino) ON DELETE CASCADE,
			chunk_idx INT NOT NULL,
			blob_key TEXT NOT NULL,
			size INT NOT NULL,
			PRIMARY KEY (ino, chunk_idx)
		)`, s.Chunk, s.Inode),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			ino BIGINT PRIMARY KEY REFERENCES %s(ino) ON DELETE CASCADE,
			target TEXT NOT NULL
		)`, s.Symlink, s.Inode),

		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
			id BIGSERIAL PRIMARY KEY,
			key BYTEA NOT NULL UNIQUE,
			size BIGINT NOT NULL,
			modified TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			data BYTEA
		)`, s.Blob),
	}
}

// CreateTables executes all DDL statements.
func (s *Schema) CreateTables(ctx context.Context, db *sql.DB) error {
	for _, stmt := range s.DDL() {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating table: %w", err)
		}
	}
	return nil
}
