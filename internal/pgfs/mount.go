package pgfs

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Mount mounts the PG-backed filesystem at mountPoint.
// This call blocks until the filesystem is unmounted.
func Mount(ctx context.Context, pgURL, tablePrefix, mountPoint string) error {
	_, m, _, err := Open(ctx, pgURL, tablePrefix)
	if err != nil {
		return err
	}

	root, err := NewRootNode(m)
	if err != nil {
		return fmt.Errorf("creating root node: %w", err)
	}

	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			Name:   "aifs",
			FsName: "aifs",
		},
		RootStableAttr: &fs.StableAttr{Ino: 1},
	}

	server, err := fs.Mount(mountPoint, root, opts)
	if err != nil {
		return fmt.Errorf("mounting %s: %w", mountPoint, err)
	}

	fmt.Printf("mounted aifs at %s\n", mountPoint)
	server.Wait()
	return nil
}

// Umount unmounts a FUSE mount point.
func Umount(mountPoint string) error {
	bin, err := exec.LookPath("fusermount3")
	if err != nil {
		bin, err = exec.LookPath("fusermount")
		if err != nil {
			return fmt.Errorf("fusermount not found")
		}
	}

	cmd := exec.Command(bin, "-u", mountPoint)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("umount %s: %w", mountPoint, err)
	}
	fmt.Printf("unmounted %s\n", mountPoint)
	return nil
}
