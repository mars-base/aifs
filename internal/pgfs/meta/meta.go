package meta

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"syscall"
	"time"

	"github.com/mars-base/aifs/internal/pgfs/object"
)

// Inode kind constants.
const (
	KindDir = iota + 1
	KindFile
	KindSymlink
)

// RootIno is the inode number of the filesystem root.
const RootIno uint64 = 1

// DefaultChunkSize matches JuiceFS's default block size.
const DefaultChunkSize int = 4 << 20

// FormatInfo is stored in the setting table under name="format".
type FormatInfo struct {
	VolumeName string    `json:"volume_name"`
	CreatedAt  time.Time `json:"created_at"`
	Version    string    `json:"version"`
	RootIno    uint64    `json:"root_ino"`
}

// Attr represents a file/directory/symlink attribute.
type Attr struct {
	Ino   uint64
	Kind  uint8
	Mode  uint32
	Uid   uint32
	Gid   uint32
	Size  uint64
	Nlink uint32
	Atime time.Time
	Mtime time.Time
	Ctime time.Time
}

// SetAttrMask selects which fields to update.
type SetAttrMask struct {
	Mode  bool
	UID   bool
	GID   bool
	Size  bool
	Atime bool
	Mtime bool
}

// MetadataStore is the metadata backend for a PG filesystem.
type MetadataStore interface {
	Init(ctx context.Context, volName string, uid, gid uint32, force bool) (*FormatInfo, error)
	Load(ctx context.Context) (*FormatInfo, error)

	Lookup(ctx context.Context, parent uint64, name string) (*Attr, error)
	GetAttr(ctx context.Context, ino uint64) (*Attr, error)
	SetAttr(ctx context.Context, ino uint64, mask SetAttrMask, attr *Attr) error

	Mkdir(ctx context.Context, parent uint64, name string, mode uint32, uid, gid uint32) (*Attr, error)
	Create(ctx context.Context, parent uint64, name string, mode uint32, uid, gid uint32) (*Attr, error)
	Unlink(ctx context.Context, parent uint64, name string) error
	Rmdir(ctx context.Context, parent uint64, name string) error
	Rename(ctx context.Context, oldParent uint64, oldName string, newParent uint64, newName string) error
	ReadDir(ctx context.Context, ino uint64) ([]DirEntry, error)

	Open(ctx context.Context, ino uint64) error
	Read(ctx context.Context, ino uint64, off int64, size int) ([]byte, error)
	Write(ctx context.Context, ino uint64, off int64, data []byte) (uint32, error)
	Truncate(ctx context.Context, ino uint64, size uint64) error

	Symlink(ctx context.Context, parent uint64, name string, target string, uid, gid uint32) (*Attr, error)
	Readlink(ctx context.Context, ino uint64) (string, error)
}

// DirEntry represents a single directory entry.
type DirEntry struct {
	Name string
	Ino  uint64
	Kind uint8
	Mode uint32
}

// DB implements MetadataStore on PostgreSQL.
type DB struct {
	db        *sql.DB
	blob      object.BlobStore
	schema    *Schema
	chunkSize int
}

// NewDB creates a metadata store.
func NewDB(db *sql.DB, blob object.BlobStore, schema *Schema) *DB {
	chunkSize := DefaultChunkSize
	if chunkSize <= 0 {
		chunkSize = 4 << 20
	}
	return &DB{
		db:        db,
		blob:      blob,
		schema:    schema,
		chunkSize: chunkSize,
	}
}

// Init creates tables and initializes the root inode.
func (m *DB) Init(ctx context.Context, volName string, uid, gid uint32, force bool) (*FormatInfo, error) {
	if err := m.schema.CreateTables(ctx, m.db); err != nil {
		return nil, err
	}

	var existing string
	err := m.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT value FROM %s WHERE name = 'format'", m.schema.Setting)).Scan(&existing)
	if err == nil {
		if !force {
			return nil, fmt.Errorf("filesystem already formatted")
		}
		// With force, continue and overwrite format record / counters.
	} else if err != sql.ErrNoRows {
		return nil, err
	}

	info := &FormatInfo{
		VolumeName: volName,
		CreatedAt:  time.Now().UTC(),
		Version:    "aifs-pgfs-1",
		RootIno:    RootIno,
	}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (name, value) VALUES ('format', $1) ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value", m.schema.Setting),
		data); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (name, value) VALUES ('nextInode', 1) ON CONFLICT (name) DO UPDATE SET value = 1", m.schema.Counter)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (ino, kind, mode, uid, gid, size, nlink, atime, mtime, ctime)
		VALUES (1, $1, $2, $3, $4, 0, 2, $5, $5, $5)
		ON CONFLICT (ino) DO UPDATE SET kind = EXCLUDED.kind, mode = EXCLUDED.mode`, m.schema.Inode),
		KindDir, uint32(0755)|syscall.S_IFDIR, uid, gid, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return info, nil
}

// Load reads the format record.
func (m *DB) Load(ctx context.Context) (*FormatInfo, error) {
	var raw []byte
	err := m.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT value FROM %s WHERE name = 'format'", m.schema.Setting)).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("filesystem not formatted")
	}
	if err != nil {
		return nil, err
	}
	var info FormatInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetAttr returns inode attributes.
func (m *DB) GetAttr(ctx context.Context, ino uint64) (*Attr, error) {
	row := m.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT ino, kind, mode, uid, gid, size, nlink, atime, mtime, ctime FROM %s WHERE ino = $1", m.schema.Inode),
		ino)
	return scanAttr(row)
}

// Lookup returns the attributes of a directory entry.
func (m *DB) Lookup(ctx context.Context, parent uint64, name string) (*Attr, error) {
	row := m.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT i.ino, i.kind, i.mode, i.uid, i.gid, i.size, i.nlink, i.atime, i.mtime, i.ctime
		FROM %s i JOIN %s d ON i.ino = d.child
		WHERE d.parent = $1 AND d.name = $2`, m.schema.Inode, m.schema.Dentry),
		parent, name)
	attr, err := scanAttr(row)
	if err == sql.ErrNoRows {
		return nil, syscall.ENOENT
	}
	return attr, err
}

// SetAttr updates selected attributes.
func (m *DB) SetAttr(ctx context.Context, ino uint64, mask SetAttrMask, attr *Attr) error {
	if mask.Size {
		if err := m.Truncate(ctx, ino, attr.Size); err != nil {
			return err
		}
	}
	parts := []string{}
	args := []interface{}{}
	argIdx := 1
	if mask.Mode {
		parts = append(parts, fmt.Sprintf("mode = $%d", argIdx))
		args = append(args, attr.Mode)
		argIdx++
	}
	if mask.UID {
		parts = append(parts, fmt.Sprintf("uid = $%d", argIdx))
		args = append(args, attr.Uid)
		argIdx++
	}
	if mask.GID {
		parts = append(parts, fmt.Sprintf("gid = $%d", argIdx))
		args = append(args, attr.Gid)
		argIdx++
	}
	if mask.Atime {
		parts = append(parts, fmt.Sprintf("atime = $%d", argIdx))
		args = append(args, attr.Atime)
		argIdx++
	}
	if mask.Mtime {
		parts = append(parts, fmt.Sprintf("mtime = $%d", argIdx))
		args = append(args, attr.Mtime)
		argIdx++
	}
	if len(parts) == 0 {
		return nil
	}
	parts = append(parts, fmt.Sprintf("ctime = $%d", argIdx))
	args = append(args, time.Now().UTC())
	argIdx++
	args = append(args, ino)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE ino = $%d", m.schema.Inode, joinParts(parts, ", "), argIdx)
	_, err := m.db.ExecContext(ctx, query, args...)
	return err
}

// Mkdir creates a directory.
func (m *DB) Mkdir(ctx context.Context, parent uint64, name string, mode uint32, uid, gid uint32) (*Attr, error) {
	return m.createNode(ctx, parent, name, KindDir, mode|syscall.S_IFDIR, uid, gid, func(tx *sql.Tx, ino uint64) error {
		// New subdirectory adds a '..' link to parent.
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET nlink = nlink + 1, mtime = $1 WHERE ino = $2", m.schema.Inode),
			time.Now().UTC(), parent)
		return err
	})
}

// Create creates a regular file.
func (m *DB) Create(ctx context.Context, parent uint64, name string, mode uint32, uid, gid uint32) (*Attr, error) {
	return m.createNode(ctx, parent, name, KindFile, mode|syscall.S_IFREG, uid, gid, nil)
}

// Symlink creates a symbolic link.
func (m *DB) Symlink(ctx context.Context, parent uint64, name string, target string, uid, gid uint32) (*Attr, error) {
	attr, err := m.createNode(ctx, parent, name, KindSymlink, 0777|syscall.S_IFLNK, uid, gid, func(tx *sql.Tx, ino uint64) error {
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s (ino, target) VALUES ($1, $2) ON CONFLICT (ino) DO UPDATE SET target = EXCLUDED.target", m.schema.Symlink),
			ino, target)
		return err
	})
	if err != nil {
		return nil, err
	}
	attr.Size = uint64(len(target))
	if _, err := m.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET size = $1 WHERE ino = $2", m.schema.Inode),
		attr.Size, attr.Ino); err != nil {
		return nil, err
	}
	return attr, nil
}

// Readlink returns the target of a symlink.
func (m *DB) Readlink(ctx context.Context, ino uint64) (string, error) {
	var target string
	err := m.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT target FROM %s WHERE ino = $1", m.schema.Symlink), ino).Scan(&target)
	if err == sql.ErrNoRows {
		return "", syscall.ENOENT
	}
	return target, err
}

// Unlink removes a directory entry.
func (m *DB) Unlink(ctx context.Context, parent uint64, name string) error {
	return m.withTx(ctx, func(tx *sql.Tx) error {
		attr, err := m.lookupTx(ctx, tx, parent, name)
		if err != nil {
			return err
		}
		if attr.Kind == KindDir {
			return syscall.EISDIR
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE parent = $1 AND name = $2", m.schema.Dentry),
			parent, name); err != nil {
			return err
		}
		if attr.Nlink <= 1 {
			if err := m.deleteInodeDataTx(ctx, tx, attr.Ino); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("DELETE FROM %s WHERE ino = $1", m.schema.Inode), attr.Ino); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET nlink = nlink - 1, ctime = $1 WHERE ino = $2", m.schema.Inode),
				time.Now().UTC(), attr.Ino); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET mtime = $1 WHERE ino = $2", m.schema.Inode),
			time.Now().UTC(), parent); err != nil {
			return err
		}
		return nil
	})
}

// Rmdir removes an empty directory.
func (m *DB) Rmdir(ctx context.Context, parent uint64, name string) error {
	return m.withTx(ctx, func(tx *sql.Tx) error {
		attr, err := m.lookupTx(ctx, tx, parent, name)
		if err != nil {
			return err
		}
		if attr.Kind != KindDir {
			return syscall.ENOTDIR
		}
		var count int
		if err := tx.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE parent = $1", m.schema.Dentry),
			attr.Ino).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return syscall.ENOTEMPTY
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE parent = $1 AND name = $2", m.schema.Dentry),
			parent, name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE ino = $1", m.schema.Inode), attr.Ino); err != nil {
			return err
		}
		// Parent loses the '..' reference from the removed directory.
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET nlink = nlink - 1, mtime = $1 WHERE ino = $2", m.schema.Inode),
			time.Now().UTC(), parent); err != nil {
			return err
		}
		return nil
	})
}

// Rename moves a directory entry.
func (m *DB) Rename(ctx context.Context, oldParent uint64, oldName string, newParent uint64, newName string) error {
	if oldParent == newParent && oldName == newName {
		return nil
	}
	return m.withTx(ctx, func(tx *sql.Tx) error {
		src, err := m.lookupTx(ctx, tx, oldParent, oldName)
		if err != nil {
			return err
		}
		dst, err := m.lookupTx(ctx, tx, newParent, newName)
		if err != nil && err != syscall.ENOENT {
			return err
		}
		if dst != nil {
			if dst.Kind == KindDir {
				var count int
				if err := tx.QueryRowContext(ctx,
					fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE parent = $1", m.schema.Dentry),
					dst.Ino).Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					return syscall.ENOTEMPTY
				}
			}
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("DELETE FROM %s WHERE parent = $1 AND name = $2", m.schema.Dentry),
				newParent, newName); err != nil {
				return err
			}
			if err := m.deleteInodeDataTx(ctx, tx, dst.Ino); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("DELETE FROM %s WHERE ino = $1", m.schema.Inode), dst.Ino); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET parent = $1, name = $2 WHERE parent = $3 AND name = $4", m.schema.Dentry),
			newParent, newName, oldParent, oldName); err != nil {
			return err
		}
		if src.Kind == KindDir && oldParent != newParent {
			now := time.Now().UTC()
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET nlink = nlink - 1, mtime = $1 WHERE ino = $2", m.schema.Inode),
				now, oldParent); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET nlink = nlink + 1, mtime = $1 WHERE ino = $2", m.schema.Inode),
				now, newParent); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReadDir lists the entries of a directory.
func (m *DB) ReadDir(ctx context.Context, ino uint64) ([]DirEntry, error) {
	rows, err := m.db.QueryContext(ctx,
		fmt.Sprintf(`SELECT d.name, i.ino, i.kind, i.mode
		FROM %s d JOIN %s i ON d.child = i.ino
		WHERE d.parent = $1 ORDER BY d.name`, m.schema.Dentry, m.schema.Inode),
		ino)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DirEntry
	for rows.Next() {
		var e DirEntry
		if err := rows.Scan(&e.Name, &e.Ino, &e.Kind, &e.Mode); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Open is a no-op for this simple backend; it just verifies the inode exists.
func (m *DB) Open(ctx context.Context, ino uint64) error {
	_, err := m.GetAttr(ctx, ino)
	return err
}

// Read returns file data starting at off.
func (m *DB) Read(ctx context.Context, ino uint64, off int64, size int) ([]byte, error) {
	attr, err := m.GetAttr(ctx, ino)
	if err != nil {
		return nil, err
	}
	if off >= int64(attr.Size) || size <= 0 {
		return []byte{}, nil
	}
	end := off + int64(size)
	if end > int64(attr.Size) {
		end = int64(attr.Size)
	}
	startChunk := int(off / int64(m.chunkSize))
	endChunk := int((end - 1) / int64(m.chunkSize))
	out := make([]byte, 0, end-off)
	for ci := startChunk; ci <= endChunk; ci++ {
		chunkOff := int64(ci) * int64(m.chunkSize)
		data, err := m.readChunk(ctx, ino, ci)
		if err != nil {
			return nil, err
		}
		if data == nil {
			data = make([]byte, m.chunkSize)
		}
		srcStart := int64(0)
		if chunkOff < off {
			srcStart = off - chunkOff
		}
		srcEnd := int64(len(data))
		chunkEnd := chunkOff + int64(len(data))
		if chunkEnd > end {
			srcEnd = int64(len(data)) - (chunkEnd - end)
		}
		out = append(out, data[srcStart:srcEnd]...)
	}
	return out, nil
}

// Write writes data at off.
func (m *DB) Write(ctx context.Context, ino uint64, off int64, data []byte) (uint32, error) {
	if len(data) == 0 {
		return 0, nil
	}
	startChunk := int(off / int64(m.chunkSize))
	endChunk := int((off + int64(len(data)) - 1) / int64(m.chunkSize))
	newSize := uint64(off + int64(len(data)))

	for ci := startChunk; ci <= endChunk; ci++ {
		chunkOff := int64(ci) * int64(m.chunkSize)
		existing, err := m.readChunk(ctx, ino, ci)
		if err != nil {
			return 0, err
		}
		buf := make([]byte, m.chunkSize)
		if existing != nil {
			copy(buf, existing)
		}
		srcStart := int64(0)
		if chunkOff < off {
			srcStart = off - chunkOff
		}
		n := copy(buf[srcStart:], data)
		writtenLen := srcStart + int64(n)
		if writtenLen < int64(len(existing)) {
			writtenLen = int64(len(existing))
		}
		chunkData := buf[:writtenLen]
		key := chunkKey(ino, ci)
		if err := m.blob.Put(ctx, key, chunkData); err != nil {
			return 0, err
		}
		if _, err := m.db.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (ino, chunk_idx, blob_key, size)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (ino, chunk_idx) DO UPDATE SET blob_key = EXCLUDED.blob_key, size = EXCLUDED.size`, m.schema.Chunk),
			ino, ci, key, len(chunkData)); err != nil {
			return 0, err
		}
	}

	_, err := m.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET size = $1, mtime = $2 WHERE ino = $3", m.schema.Inode),
		newSize, time.Now().UTC(), ino)
	if err != nil {
		return 0, err
	}
	return uint32(len(data)), nil
}

// Truncate changes the size of a file.
func (m *DB) Truncate(ctx context.Context, ino uint64, size uint64) error {
	attr, err := m.GetAttr(ctx, ino)
	if err != nil {
		return err
	}
	if size == attr.Size {
		return nil
	}
	if size < attr.Size {
		// Delete fully-truncated chunks.
		lastChunk := -1
		if size > 0 {
			lastChunk = int((size - 1) / uint64(m.chunkSize))
		}
		rows, err := m.db.QueryContext(ctx,
			fmt.Sprintf("SELECT chunk_idx, blob_key FROM %s WHERE ino = $1 ORDER BY chunk_idx", m.schema.Chunk), ino)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var idx int
			var key string
			if err := rows.Scan(&idx, &key); err != nil {
				return err
			}
			if idx > lastChunk {
				if err := m.blob.Delete(ctx, key); err != nil {
					return err
				}
				if _, err := m.db.ExecContext(ctx,
					fmt.Sprintf("DELETE FROM %s WHERE ino = $1 AND chunk_idx = $2", m.schema.Chunk),
					ino, idx); err != nil {
					return err
				}
			} else if idx == lastChunk {
				newChunkLen := int(size - uint64(idx*m.chunkSize))
				if newChunkLen <= 0 {
					if err := m.blob.Delete(ctx, key); err != nil {
						return err
					}
					if _, err := m.db.ExecContext(ctx,
						fmt.Sprintf("DELETE FROM %s WHERE ino = $1 AND chunk_idx = $2", m.schema.Chunk),
						ino, idx); err != nil {
						return err
					}
				} else {
					data, err := m.readChunk(ctx, ino, idx)
					if err != nil {
						return err
					}
					data = data[:newChunkLen]
					if err := m.blob.Put(ctx, key, data); err != nil {
						return err
					}
					if _, err := m.db.ExecContext(ctx,
						fmt.Sprintf("UPDATE %s SET size = $1 WHERE ino = $2 AND chunk_idx = $3", m.schema.Chunk),
						newChunkLen, ino, idx); err != nil {
						return err
					}
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
	}
	_, err = m.db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET size = $1, mtime = $2 WHERE ino = $3", m.schema.Inode),
		size, time.Now().UTC(), ino)
	return err
}

// --- helpers ---

func (m *DB) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *DB) nextInode(ctx context.Context, tx *sql.Tx) (uint64, error) {
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (name, value) VALUES ('nextInode', 1) ON CONFLICT (name) DO NOTHING", m.schema.Counter))
	if err != nil {
		return 0, err
	}
	var ino uint64
	if err := tx.QueryRowContext(ctx,
		fmt.Sprintf("UPDATE %s SET value = value + 1 WHERE name = 'nextInode' RETURNING value", m.schema.Counter)).Scan(&ino); err != nil {
		return 0, err
	}
	return ino, nil
}

func (m *DB) createNode(ctx context.Context, parent uint64, name string, kind uint8, mode uint32, uid, gid uint32, afterInsert func(*sql.Tx, uint64) error) (*Attr, error) {
	var attr *Attr
	err := m.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := m.lookupTx(ctx, tx, parent, name); err == nil {
			return syscall.EEXIST
		} else if err != syscall.ENOENT {
			return err
		}
		ino, err := m.nextInode(ctx, tx)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		nlink := 1
		if kind == KindDir {
			nlink = 2
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`INSERT INTO %s (ino, kind, mode, uid, gid, size, nlink, atime, mtime, ctime)
			VALUES ($1, $2, $3, $4, $5, 0, $6, $7, $7, $7)`, m.schema.Inode),
			ino, kind, mode, uid, gid, nlink, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("INSERT INTO %s (parent, name, child) VALUES ($1, $2, $3)", m.schema.Dentry),
			parent, name, ino); err != nil {
			return err
		}
		if afterInsert != nil {
			if err := afterInsert(tx, ino); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("UPDATE %s SET mtime = $1 WHERE ino = $2", m.schema.Inode),
			now, parent); err != nil {
			return err
		}
		attr = &Attr{
			Ino:   ino,
			Kind:  kind,
			Mode:  mode,
			Uid:   uid,
			Gid:   gid,
			Size:  0,
			Nlink: uint32(nlink),
			Atime: now,
			Mtime: now,
			Ctime: now,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return attr, nil
}

func (m *DB) lookupTx(ctx context.Context, tx *sql.Tx, parent uint64, name string) (*Attr, error) {
	row := tx.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT i.ino, i.kind, i.mode, i.uid, i.gid, i.size, i.nlink, i.atime, i.mtime, i.ctime
		FROM %s i JOIN %s d ON i.ino = d.child
		WHERE d.parent = $1 AND d.name = $2`, m.schema.Inode, m.schema.Dentry),
		parent, name)
	attr, err := scanAttr(row)
	if err == sql.ErrNoRows {
		return nil, syscall.ENOENT
	}
	return attr, err
}

func (m *DB) readChunk(ctx context.Context, ino uint64, chunkIdx int) ([]byte, error) {
	var key string
	err := m.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT blob_key FROM %s WHERE ino = $1 AND chunk_idx = $2", m.schema.Chunk),
		ino, chunkIdx).Scan(&key)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return m.blob.Get(ctx, key)
}

func (m *DB) deleteInodeDataTx(ctx context.Context, tx *sql.Tx, ino uint64) error {
	rows, err := tx.QueryContext(ctx,
		fmt.Sprintf("SELECT chunk_idx, blob_key FROM %s WHERE ino = $1", m.schema.Chunk), ino)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var idx int
		var key string
		if err := rows.Scan(&idx, &key); err != nil {
			return err
		}
		if err := m.blob.Delete(ctx, key); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE ino = $1", m.schema.Chunk), ino); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE ino = $1", m.schema.Symlink), ino); err != nil {
		return err
	}
	return nil
}

func scanAttr(row *sql.Row) (*Attr, error) {
	var a Attr
	err := row.Scan(&a.Ino, &a.Kind, &a.Mode, &a.Uid, &a.Gid, &a.Size, &a.Nlink, &a.Atime, &a.Mtime, &a.Ctime)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func chunkKey(ino uint64, idx int) string {
	return fmt.Sprintf("aifs://%d/%d", ino, idx)
}

func joinParts(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}
