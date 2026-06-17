package pgfs

import (
	"context"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"golang.org/x/sys/unix"

	"github.com/mars-base/aifs/internal/pgfs/meta"
)

// FSNode is a node in the PG-backed filesystem.
type FSNode struct {
	fs.Inode
	m        meta.MetadataStore
	dataPath string
}

var (
	_ fs.NodeLookuper   = (*FSNode)(nil)
	_ fs.NodeGetattrer  = (*FSNode)(nil)
	_ fs.NodeSetattrer  = (*FSNode)(nil)
	_ fs.NodeOpener     = (*FSNode)(nil)
	_ fs.NodeCreater    = (*FSNode)(nil)
	_ fs.NodeMkdirer    = (*FSNode)(nil)
	_ fs.NodeUnlinker   = (*FSNode)(nil)
	_ fs.NodeRmdirer    = (*FSNode)(nil)
	_ fs.NodeRenamer    = (*FSNode)(nil)
	_ fs.NodeSymlinker  = (*FSNode)(nil)
	_ fs.NodeReadlinker = (*FSNode)(nil)
	_ fs.NodeReaddirer  = (*FSNode)(nil)
	_ fs.NodeStatfser   = (*FSNode)(nil)
)

// NewRootNode creates the root inode backed by the metadata store.
func NewRootNode(m meta.MetadataStore, dataPath string) (*FSNode, error) {
	return &FSNode{m: m, dataPath: dataPath}, nil
}

func (n *FSNode) ino() uint64 {
	return n.StableAttr().Ino
}

func (n *FSNode) caller(ctx context.Context) (uint32, uint32) {
	if c, ok := fuse.FromContext(ctx); ok {
		return c.Uid, c.Gid
	}
	return 0, 0
}

func (n *FSNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	attr, err := n.m.Lookup(ctx, n.ino(), name)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	child := n.NewInode(ctx, &FSNode{m: n.m}, fs.StableAttr{Ino: attr.Ino, Mode: attr.Mode})
	fillEntryOut(out, attr)
	return child, fs.OK
}

func (n *FSNode) Getattr(ctx context.Context, f fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	attr, err := n.m.GetAttr(ctx, n.ino())
	if err != nil {
		return fs.ToErrno(err)
	}
	fillAttrOut(out, attr)
	return fs.OK
}

func (n *FSNode) Setattr(ctx context.Context, f fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	mask := meta.SetAttrMask{}
	attr := &meta.Attr{}
	if in.Valid&fuse.FATTR_MODE != 0 {
		mask.Mode = true
		attr.Mode = in.Mode
	}
	if in.Valid&fuse.FATTR_UID != 0 {
		mask.UID = true
		attr.Uid = in.Uid
	}
	if in.Valid&fuse.FATTR_GID != 0 {
		mask.GID = true
		attr.Gid = in.Gid
	}
	if in.Valid&fuse.FATTR_SIZE != 0 {
		mask.Size = true
		attr.Size = in.Size
	}
	if in.Valid&fuse.FATTR_ATIME != 0 {
		mask.Atime = true
		attr.Atime = time.Unix(int64(in.Atime), int64(in.Atimensec))
	}
	if in.Valid&fuse.FATTR_MTIME != 0 {
		mask.Mtime = true
		attr.Mtime = time.Unix(int64(in.Mtime), int64(in.Mtimensec))
	}
	if err := n.m.SetAttr(ctx, n.ino(), mask, attr); err != nil {
		return fs.ToErrno(err)
	}
	return n.Getattr(ctx, f, out)
}

func (n *FSNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if err := n.m.Open(ctx, n.ino()); err != nil {
		return nil, 0, fs.ToErrno(err)
	}
	return &fileHandle{m: n.m, ino: n.ino()}, fuse.FOPEN_KEEP_CACHE, fs.OK
}

func (n *FSNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (inode *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	uid, gid := n.caller(ctx)
	attr, err := n.m.Create(ctx, n.ino(), name, mode, uid, gid)
	if err != nil {
		return nil, nil, 0, fs.ToErrno(err)
	}
	child := n.NewInode(ctx, &FSNode{m: n.m}, fs.StableAttr{Ino: attr.Ino, Mode: attr.Mode})
	fillEntryOut(out, attr)
	return child, &fileHandle{m: n.m, ino: attr.Ino}, 0, fs.OK
}

func (n *FSNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	uid, gid := n.caller(ctx)
	attr, err := n.m.Mkdir(ctx, n.ino(), name, mode, uid, gid)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	child := n.NewInode(ctx, &FSNode{m: n.m}, fs.StableAttr{Ino: attr.Ino, Mode: attr.Mode})
	fillEntryOut(out, attr)
	return child, fs.OK
}

func (n *FSNode) Unlink(ctx context.Context, name string) syscall.Errno {
	return fs.ToErrno(n.m.Unlink(ctx, n.ino(), name))
}

func (n *FSNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	return fs.ToErrno(n.m.Rmdir(ctx, n.ino(), name))
}

func (n *FSNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	np, ok := newParent.(*FSNode)
	if !ok {
		return syscall.EINVAL
	}
	return fs.ToErrno(n.m.Rename(ctx, n.ino(), name, np.ino(), newName))
}

func (n *FSNode) Symlink(ctx context.Context, target, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	uid, gid := n.caller(ctx)
	attr, err := n.m.Symlink(ctx, n.ino(), name, target, uid, gid)
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	child := n.NewInode(ctx, &FSNode{m: n.m}, fs.StableAttr{Ino: attr.Ino, Mode: attr.Mode})
	fillEntryOut(out, attr)
	return child, fs.OK
}

func (n *FSNode) Readlink(ctx context.Context) ([]byte, syscall.Errno) {
	target, err := n.m.Readlink(ctx, n.ino())
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	return []byte(target), fs.OK
}

func (n *FSNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := n.m.ReadDir(ctx, n.ino())
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	var list []fuse.DirEntry
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
		list = append(list, fuse.DirEntry{Name: e.Name, Ino: e.Ino, Mode: mode})
	}
	return fs.NewListDirStream(list), fs.OK
}

func (n *FSNode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	var st unix.Statfs_t
	if err := unix.Statfs(n.dataPath, &st); err == nil && st.Blocks > 0 {
		out.Bsize = uint32(st.Bsize)
		out.Frsize = uint32(st.Bsize)
		out.Blocks = st.Blocks
		out.Bfree = st.Bfree
		out.Bavail = st.Bavail
		out.Files = st.Files
		out.Ffree = st.Ffree
		return fs.OK
	}

	// Fallback to a fixed-size virtual disk if the data path cannot be stat'd.
	*out = fuse.StatfsOut{
		Bsize:  4096,
		Blocks: 1 << 30,
		Bfree:  1 << 30,
		Bavail: 1 << 30,
		Files:  1 << 20,
		Ffree:  1 << 20,
	}
	return fs.OK
}

// fileHandle is a handle for an open file.
type fileHandle struct {
	m   meta.MetadataStore
	ino uint64
}

var (
	_ fs.FileReader  = (*fileHandle)(nil)
	_ fs.FileWriter  = (*fileHandle)(nil)
	_ fs.FileFlusher = (*fileHandle)(nil)
	_ fs.FileReleaser = (*fileHandle)(nil)
)

func (h *fileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	data, err := h.m.Read(ctx, h.ino, off, len(dest))
	if err != nil {
		return nil, fs.ToErrno(err)
	}
	return fuse.ReadResultData(data), fs.OK
}

func (h *fileHandle) Write(ctx context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	n, err := h.m.Write(ctx, h.ino, off, data)
	if err != nil {
		return 0, fs.ToErrno(err)
	}
	return n, fs.OK
}

func (h *fileHandle) Flush(ctx context.Context) syscall.Errno {
	return fs.OK
}

func (h *fileHandle) Release(ctx context.Context) syscall.Errno {
	return fs.OK
}

func fillAttr(out *fuse.Attr, a *meta.Attr) {
	out.Ino = a.Ino
	out.Size = a.Size
	out.Blocks = (a.Size + 511) / 512
	if out.Blocks == 0 && a.Size > 0 {
		out.Blocks = 1
	}
	out.Atime = uint64(a.Atime.Unix())
	out.Atimensec = uint32(a.Atime.Nanosecond())
	out.Mtime = uint64(a.Mtime.Unix())
	out.Mtimensec = uint32(a.Mtime.Nanosecond())
	out.Ctime = uint64(a.Ctime.Unix())
	out.Ctimensec = uint32(a.Ctime.Nanosecond())
	out.Mode = a.Mode
	out.Nlink = a.Nlink
	out.Uid = a.Uid
	out.Gid = a.Gid
}

func fillAttrOut(out *fuse.AttrOut, a *meta.Attr) {
	fillAttr(&out.Attr, a)
	out.SetTimeout(1 * time.Second)
}

func fillEntryOut(out *fuse.EntryOut, a *meta.Attr) {
	fillAttr(&out.Attr, a)
	out.SetEntryTimeout(1 * time.Second)
	out.SetAttrTimeout(1 * time.Second)
}
