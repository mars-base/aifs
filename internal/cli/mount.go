package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pgfs"
)

var (
	formatVolume string
	formatForce  bool
	formatURL    string
	formatPrefix string
)

func init() {
	rootCmd.AddCommand(formatCmd)
	formatCmd.Flags().StringVar(&formatVolume, "volume", "", "filesystem volume name (default: instance name)")
	formatCmd.Flags().BoolVar(&formatForce, "force", false, "overwrite existing filesystem metadata")
	formatCmd.Flags().StringVar(&formatURL, "url", "", "PostgreSQL URL to format directly (skips instance config)")
	formatCmd.Flags().StringVar(&formatPrefix, "prefix", "aifs_", "table prefix to use with --url")
}

var formatCmd = &cobra.Command{
	Use:   "format",
	Short: "Initialize a PG-backed filesystem in an instance",
	Long: `format creates the filesystem tables and root inode in the specified PostgreSQL instance.

This is analogous to 'mkfs' for the aifs PG-backed filesystem.

Use --url to format a database directly without an aifs instance configuration:

  aifs format --url "postgresql://user:pass@host:5432/db"
  aifs format --url "postgresql://user:pass@host:5432/db" --prefix aifs_ --volume myfs`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// --url mode: skip instance config and podman entirely.
		if formatURL != "" {
			if err := checkPGURL(cmd.Context(), formatURL); err != nil {
				return err
			}
			vol := formatVolume
			if vol == "" {
				vol = "aifs"
			}
			info, err := pgfs.Format(cmd.Context(), formatURL, vol, formatPrefix, formatForce)
			if err != nil {
				return fmt.Errorf("format failed: %w", err)
			}
			fmt.Println("[OK] formatted PG-backed filesystem")
			fmt.Printf("  url:     %s\n", formatURL)
			fmt.Printf("  volume:  %s\n", info.VolumeName)
			fmt.Printf("  prefix:  %s\n", formatPrefix)
			fmt.Printf("  root ino: %d\n", info.RootIno)
			return nil
		}

		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		if err := pm.EnsureMachine(); err != nil {
			return err
		}
		if err := pm.EnsureImage(); err != nil {
			return err
		}
		if err := pm.EnsureDirs(); err != nil {
			return err
		}
		if err := pm.EnsureNetwork(); err != nil {
			return err
		}
		if err := pm.EnsureContainer(); err != nil {
			return err
		}

		fmt.Println("-> Waiting for PostgreSQL to be ready...")
		for i := 0; i < 60; i++ {
			if ready, _ := pm.PGIsReady(); ready {
				break
			}
			time.Sleep(time.Second)
		}

		fsCfg := cfg.EffectiveFilesystem()
		if formatVolume != "" {
			fsCfg.VolumeName = formatVolume
		}

		info, err := pgfs.Format(cmd.Context(), cfg.GetPostgresURL(), fsCfg.VolumeName, fsCfg.TablePrefix, formatForce)
		if err != nil {
			return fmt.Errorf("format failed: %w", err)
		}

		fmt.Println("[OK] formatted PG-backed filesystem")
		fmt.Printf("  instance: %s\n", cfg.Instance)
		fmt.Printf("  volume:   %s\n", info.VolumeName)
		fmt.Printf("  prefix:   %s\n", fsCfg.TablePrefix)
		fmt.Printf("  root ino: %d\n", info.RootIno)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(mountCmd)
	rootCmd.AddCommand(umountCmd)
	mountCmd.Flags().BoolVarP(&mountBackground, "background", "d", false, "run mount in the background")
	mountCmd.Flags().StringVar(&mountURL, "url", "", "PostgreSQL URL to mount directly (skips instance config)")
	mountCmd.Flags().StringVar(&mountPrefix, "prefix", "aifs_", "table prefix to use with --url")
	mountCmd.Flags().BoolVarP(&mountList, "list", "l", false, "list active aifs mount points")
}

var (
	mountBackground bool
	mountURL        string
	mountPrefix     string
	mountList       bool
)

var mountCmd = &cobra.Command{
	Use:   "mount <mountpoint>",
	Short: "Mount a PG-backed filesystem",
	Long: `mount connects the FUSE filesystem backed by the specified PostgreSQL instance
onto a local directory. By default it runs in the foreground.

Use --url to mount directly from a PostgreSQL connection string without
requiring an aifs instance configuration:

  aifs mount /mnt/aifs --url "postgresql://user:pass@host:5432/db"
  aifs mount Z: -d --url "postgresql://user:pass@host:5432/db" --prefix aifs_`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// -l / --list: scan system mount table for active aifs mounts.
		if mountList {
			mounts, err := activeAIFSMounts(cmd.Context(), "", "", "")
			if err != nil {
				return fmt.Errorf("listing mounts: %w", err)
			}
			if len(mounts) == 0 {
				fmt.Println("no active aifs mounts")
				return nil
			}
			for _, m := range mounts {
				fmt.Println(m)
			}
			return nil
		}

		if len(args) == 0 {
			return fmt.Errorf("mountpoint argument required (or use -l to list active mounts)")
		}
		mountPoint := args[0]

		// --url mode: skip instance config and podman entirely.
		if mountURL != "" {
			if err := checkPGURL(cmd.Context(), mountURL); err != nil {
				return err
			}
			if mountBackground {
				return mountInBackground(mountPoint)
			}
			return pgfs.Mount(cmd.Context(), mountURL, mountPrefix, "", mountPoint, nil)
		}

		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}
		// EnsureMachine also runs EnsurePodmanService on Windows, which refreshes
		// the portproxy rule for port 2375 after a WSL restart (IP change).
		if err := pm.EnsureMachine(); err != nil {
			return err
		}
		if err := pm.EnsureContainer(); err != nil {
			return err
		}
		pm.EnsurePGPortProxy()

		fmt.Println("-> Waiting for PostgreSQL to be ready...")
		for i := 0; i < 60; i++ {
			if ready, _ := pm.PGIsReady(); ready {
				break
			}
			time.Sleep(time.Second)
		}

		fsCfg := cfg.EffectiveFilesystem()

		if mountBackground {
			return mountInBackground(mountPoint)
		}

		// Record mount state so `status` can show the active mount point.
		// AddMountState is called from the onMounted callback, which fires only
		// after the FUSE server is confirmed ready — so a failed mount never
		// leaves a stale entry in the state file.
		rec := pgfs.MountRecord{
			MountPoint: mountPoint,
			Instance:   cfg.Instance,
			PID:        os.Getpid(),
		}
		onMounted := func() {
			if err := pgfs.AddMountState(rec); err != nil {
				fmt.Fprintf(os.Stderr, "warning: recording mount state: %v\n", err)
			}
		}
		mountErr := pgfs.Mount(cmd.Context(), cfg.GetPostgresURL(), fsCfg.TablePrefix, cfg.Podman.DataDir, mountPoint, onMounted)
		if rmErr := pgfs.RemoveMountState(mountPoint); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: removing mount state: %v\n", rmErr)
		}
		return mountErr
	},
}

var umountCmd = &cobra.Command{
	Use:   "umount <mountpoint>",
	Short: "Unmount a PG-backed filesystem",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return pgfs.Umount(args[0])
	},
}

// checkPGURL verifies that the given PostgreSQL URL is reachable by opening a
// connection and pinging the server. Returns a user-friendly error on failure.
func checkPGURL(ctx context.Context, pgURL string) error {
	fmt.Print("-> Checking PostgreSQL connection... ")
	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		fmt.Println("failed")
		return fmt.Errorf("invalid PostgreSQL URL: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		fmt.Println("failed")
		return fmt.Errorf("cannot connect to PostgreSQL (%s): %w", pgURL, err)
	}
	fmt.Println("OK")
	return nil
}
