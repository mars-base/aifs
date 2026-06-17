//go:build windows

package pgfs

import (
	"context"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"github.com/mars-base/aifs/internal/pgfs/meta"
)

const sentinelIno uint64 = 0xA1F5

// winFS implements cgofuse's FileSystemInterface backed by meta.MetadataStore.
type winFS struct {
	fuse.FileSystemBase
	m        meta.MetadataStore
	dataPath string
	host     *fuse.FileSystemHost
}

// ctx returns a long-lived context for FUSE callbacks.
func (fs *winFS) ctx() context.Context {
	return context.Background()
}

// splitPath normalizes a FUSE path into its components.
// The root path returns an empty slice.
func splitPath(p string) []string {
	p = path.Clean("/" + p)
	if p == "/" {
		return nil
	}
	return strings.Split(p[1:], "/")
}

// lookup resolves a path to its attributes.
func (fs *winFS) lookup(p string) (*meta.Attr, error) {
	components := splitPath(p)
	ino := meta.RootIno
	for _, name := range components {
		attr, err := fs.m.Lookup(fs.ctx(), ino, name)
		if err != nil {
			return nil, err
		}
		ino = attr.Ino
	}
	return fs.m.GetAttr(fs.ctx(), ino)
}

// parentAndName returns the parent inode and the final component of a path.
func (fs *winFS) parentAndName(p string) (uint64, string, error) {
	components := splitPath(p)
	if len(components) == 0 {
		return 0, "", syscall.EINVAL
	}
	name := components[len(components)-1]
	parentPath := "/" + path.Join(components[:len(components)-1]...)
	attr, err := fs.lookup(parentPath)
	if err != nil {
		return 0, "", err
	}
	return attr.Ino, name, nil
}

// isSentinelPath reports whether p is the synthetic mount sentinel.
func isSentinelPath(p string) bool {
	return p == "/"+SentinelName
}

func (fs *winFS) sentinelAttr() *meta.Attr {
	now := time.Now().UTC()
	return &meta.Attr{
		Ino:   sentinelIno,
		Kind:  meta.KindFile,
		Mode:  syscall.S_IFREG | 0644,
		Uid:   0,
		Gid:   0,
		Size:  0,
		Nlink: 1,
		Atime: now,
		Mtime: now,
		Ctime: now,
	}
}

func (fs *winFS) fillStat(out *fuse.Stat_t, a *meta.Attr) {
	out.Ino = a.Ino
	out.Mode = a.Mode
	out.Nlink = a.Nlink
	out.Uid = a.Uid
	out.Gid = a.Gid
	out.Size = int64(a.Size)
	out.Birthtim = fuse.NewTimespec(a.Ctime)
	out.Atim = fuse.NewTimespec(a.Atime)
	out.Mtim = fuse.NewTimespec(a.Mtime)
	out.Ctim = fuse.NewTimespec(a.Ctime)
	out.Blksize = 4096
	if a.Size > 0 {
		out.Blocks = int64((a.Size + 511) / 512)
	}
}

// Getattr implements fuse.FileSystemInterface.
func (fs *winFS) Getattr(p string, stat *fuse.Stat_t, fh uint64) int {
	if isSentinelPath(p) {
		fs.fillStat(stat, fs.sentinelAttr())
		return 0
	}
	if fh != ^uint64(0) {
		attr, err := fs.m.GetAttr(fs.ctx(), fh)
		if err != nil {
			return mapErrno(err)
		}
		fs.fillStat(stat, attr)
		return 0
	}
	attr, err := fs.lookup(p)
	if err != nil {
		return mapErrno(err)
	}
	fs.fillStat(stat, attr)
	return 0
}

// Mkdir implements fuse.FileSystemInterface.
func (fs *winFS) Mkdir(p string, mode uint32) int {
	if isSentinelPath(p) {
		return -fuse.EACCES
	}
	if p == "/.UMOUNTIT" {
		if fs.host != nil {
			go fs.host.Unmount()
		}
		return -fuse.EACCES
	}
	parentIno, name, err := fs.parentAndName(p)
	if err != nil {
		return mapErrno(err)
	}
	uid, gid, _ := fuse.Getcontext()
	_, err = fs.m.Mkdir(fs.ctx(), parentIno, name, mode, uid, gid)
	return mapErrno(err)
}

// Unlink implements fuse.FileSystemInterface.
func (fs *winFS) Unlink(p string) int {
	if isSentinelPath(p) {
		return -fuse.EACCES
	}
	parentIno, name, err := fs.parentAndName(p)
	if err != nil {
		return mapErrno(err)
	}
	return mapErrno(fs.m.Unlink(fs.ctx(), parentIno, name))
}

// Rmdir implements fuse.FileSystemInterface.
func (fs *winFS) Rmdir(p string) int {
	if isSentinelPath(p) {
		return -fuse.EACCES
	}
	parentIno, name, err := fs.parentAndName(p)
	if err != nil {
		return mapErrno(err)
	}
	return mapErrno(fs.m.Rmdir(fs.ctx(), parentIno, name))
}

// Rename implements fuse.FileSystemInterface.
func (fs *winFS) Rename(oldpath string, newpath string) int {
	oldParent, oldName, err := fs.parentAndName(oldpath)
	if err != nil {
		return mapErrno(err)
	}
	newParent, newName, err := fs.parentAndName(newpath)
	if err != nil {
		return mapErrno(err)
	}
	return mapErrno(fs.m.Rename(fs.ctx(), oldParent, oldName, newParent, newName))
}

// Symlink implements fuse.FileSystemInterface.
func (fs *winFS) Symlink(target string, newpath string) int {
	parentIno, name, err := fs.parentAndName(newpath)
	if err != nil {
		return mapErrno(err)
	}
	uid, gid, _ := fuse.Getcontext()
	_, err = fs.m.Symlink(fs.ctx(), parentIno, name, target, uid, gid)
	return mapErrno(err)
}

// Readlink implements fuse.FileSystemInterface.
func (fs *winFS) Readlink(p string) (int, string) {
	attr, err := fs.lookup(p)
	if err != nil {
		return mapErrno(err), ""
	}
	target, err := fs.m.Readlink(fs.ctx(), attr.Ino)
	if err != nil {
		return mapErrno(err), ""
	}
	return 0, target
}

// Chmod implements fuse.FileSystemInterface.
func (fs *winFS) Chmod(p string, mode uint32) int {
	ino, err := fs.resolveIno(p)
	if err != nil {
		return mapErrno(err)
	}
	return mapErrno(fs.m.SetAttr(fs.ctx(), ino, meta.SetAttrMask{Mode: true}, &meta.Attr{Mode: mode}))
}

// Chown implements fuse.FileSystemInterface.
func (fs *winFS) Chown(p string, uid uint32, gid uint32) int {
	ino, err := fs.resolveIno(p)
	if err != nil {
		return mapErrno(err)
	}
	mask := meta.SetAttrMask{}
	attr := &meta.Attr{}
	if uid != ^uint32(0) {
		mask.UID = true
		attr.Uid = uid
	}
	if gid != ^uint32(0) {
		mask.GID = true
		attr.Gid = gid
	}
	if !mask.UID && !mask.GID {
		return 0
	}
	return mapErrno(fs.m.SetAttr(fs.ctx(), ino, mask, attr))
}

// Utimens implements fuse.FileSystemInterface.
func (fs *winFS) Utimens(p string, tmsp []fuse.Timespec) int {
	ino, err := fs.resolveIno(p)
	if err != nil {
		return mapErrno(err)
	}
	now := time.Now().UTC()
	var atime, mtime time.Time
	if tmsp == nil {
		atime, mtime = now, now
	} else {
		atime = tmsp[0].Time()
		mtime = tmsp[1].Time()
	}
	return mapErrno(fs.m.SetAttr(fs.ctx(), ino, meta.SetAttrMask{Atime: true, Mtime: true}, &meta.Attr{Atime: atime, Mtime: mtime}))
}

// Truncate implements fuse.FileSystemInterface.
func (fs *winFS) Truncate(p string, size int64, fh uint64) int {
	var ino uint64
	if fh != ^uint64(0) {
		ino = fh
	} else {
		attr, err := fs.lookup(p)
		if err != nil {
			return mapErrno(err)
		}
		ino = attr.Ino
	}
	return mapErrno(fs.m.Truncate(fs.ctx(), ino, uint64(size)))
}

// Open implements fuse.FileSystemInterface.
func (fs *winFS) Open(p string, flags int) (int, uint64) {
	attr, err := fs.lookup(p)
	if err != nil {
		return mapErrno(err), ^uint64(0)
	}
	if err := fs.m.Open(fs.ctx(), attr.Ino); err != nil {
		return mapErrno(err), ^uint64(0)
	}
	return 0, attr.Ino
}

// Create implements fuse.FileSystemInterface.
func (fs *winFS) Create(p string, flags int, mode uint32) (int, uint64) {
	parentIno, name, err := fs.parentAndName(p)
	if err != nil {
		return mapErrno(err), ^uint64(0)
	}
	uid, gid, _ := fuse.Getcontext()
	attr, err := fs.m.Create(fs.ctx(), parentIno, name, mode, uid, gid)
	if err != nil {
		return mapErrno(err), ^uint64(0)
	}
	return 0, attr.Ino
}

// Read implements fuse.FileSystemInterface.
func (fs *winFS) Read(p string, buf []byte, off int64, fh uint64) int {
	if fh == ^uint64(0) {
		return -fuse.EBADF
	}
	data, err := fs.m.Read(fs.ctx(), fh, off, len(buf))
	if err != nil {
		return mapErrno(err)
	}
	return copy(buf, data)
}

// Write implements fuse.FileSystemInterface.
func (fs *winFS) Write(p string, buf []byte, off int64, fh uint64) int {
	if fh == ^uint64(0) {
		return -fuse.EBADF
	}
	n, err := fs.m.Write(fs.ctx(), fh, off, buf)
	if err != nil {
		return mapErrno(err)
	}
	return int(n)
}

// Release implements fuse.FileSystemInterface.
func (fs *winFS) Release(p string, fh uint64) int {
	return 0
}

// Opendir implements fuse.FileSystemInterface.
func (fs *winFS) Opendir(p string) (int, uint64) {
	attr, err := fs.lookup(p)
	if err != nil {
		return mapErrno(err), ^uint64(0)
	}
	return 0, attr.Ino
}

// Readdir implements fuse.FileSystemInterface.
func (fs *winFS) Readdir(p string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	var ino uint64
	if fh != ^uint64(0) {
		ino = fh
	} else {
		attr, err := fs.lookup(p)
		if err != nil {
			return mapErrno(err)
		}
		ino = attr.Ino
	}
	entries, err := fs.m.ReadDir(fs.ctx(), ino)
	if err != nil {
		return mapErrno(err)
	}
	fill(".", nil, 0)
	fill("..", nil, 0)
	for _, e := range entries {
		mode := e.Mode
		if mode == 0 {
			switch e.Kind {
			case meta.KindDir:
				mode = syscall.S_IFDIR | 0755
			case meta.KindSymlink:
				mode = syscall.S_IFLNK | 0777
			default:
				mode = syscall.S_IFREG | 0644
			}
		}
		st := &fuse.Stat_t{Ino: e.Ino, Mode: mode}
		if !fill(e.Name, st, 0) {
			break
		}
	}
	return 0
}

// Releasedir implements fuse.FileSystemInterface.
func (fs *winFS) Releasedir(p string, fh uint64) int {
	return 0
}

// Statfs implements fuse.FileSystemInterface.
func (fs *winFS) Statfs(p string, stat *fuse.Statfs_t) int {
	*stat = fuse.Statfs_t{
		Bsize:   4096,
		Frsize:  4096,
		Blocks:  1 << 30,
		Bfree:   1 << 30,
		Bavail:  1 << 30,
		Files:   1 << 20,
		Ffree:   1 << 20,
		Favail:  1 << 20,
		Namemax: 255,
	}
	return 0
}

// Access implements fuse.FileSystemInterface.
func (fs *winFS) Access(p string, mask uint32) int {
	return 0
}

// Mknod is not supported.
func (fs *winFS) Mknod(p string, mode uint32, dev uint64) int {
	return -fuse.ENOSYS
}

// Link is not supported.
func (fs *winFS) Link(oldpath string, newpath string) int {
	return -fuse.ENOSYS
}

// Flush implements fuse.FileSystemInterface.
func (fs *winFS) Flush(p string, fh uint64) int {
	return 0
}

// Fsync implements fuse.FileSystemInterface.
func (fs *winFS) Fsync(p string, datasync bool, fh uint64) int {
	return 0
}

// Fsyncdir implements fuse.FileSystemInterface.
func (fs *winFS) Fsyncdir(p string, datasync bool, fh uint64) int {
	return 0
}

// resolveIno resolves a path to its inode number.
func (fs *winFS) resolveIno(p string) (uint64, error) {
	attr, err := fs.lookup(p)
	if err != nil {
		return 0, err
	}
	return attr.Ino, nil
}

func mapErrno(err error) int {
	if err == nil {
		return 0
	}
	switch {
	case err == syscall.ENOENT:
		return -fuse.ENOENT
	case err == syscall.EEXIST:
		return -fuse.EEXIST
	case err == syscall.EINVAL:
		return -fuse.EINVAL
	case err == syscall.EACCES:
		return -fuse.EACCES
	case err == syscall.ENOTDIR:
		return -fuse.ENOTDIR
	case err == syscall.EISDIR:
		return -fuse.EISDIR
	case err == syscall.ENOTEMPTY:
		return -fuse.ENOTEMPTY
	default:
		return -fuse.EIO
	}
}
