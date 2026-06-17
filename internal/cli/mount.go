package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pgfs"
)

var (
	formatVolume string
	formatForce  bool
)

func init() {
	rootCmd.AddCommand(formatCmd)
	formatCmd.Flags().StringVar(&formatVolume, "volume", "", "filesystem volume name (default: instance name)")
	formatCmd.Flags().BoolVar(&formatForce, "force", false, "overwrite existing filesystem metadata")
}

var formatCmd = &cobra.Command{
	Use:   "format",
	Short: "Initialize a PG-backed filesystem in an instance",
	Long: `format creates the filesystem tables and root inode in the specified PostgreSQL instance.

This is analogous to 'mkfs' for the aifs PG-backed filesystem.`,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		fmt.Println("→ Waiting for PostgreSQL to be ready...")
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

		fmt.Println("✓ formatted PG-backed filesystem")
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
}

var (
	mountBackground bool
)

var mountCmd = &cobra.Command{
	Use:   "mount <mountpoint>",
	Short: "Mount a PG-backed filesystem",
	Long: `mount connects the FUSE filesystem backed by the specified PostgreSQL instance
onto a local directory. By default it runs in the foreground.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		mountPoint := args[0]

		pm, err := newPodman()
		if err != nil {
			return err
		}
		if err := pm.EnsureContainer(); err != nil {
			return err
		}

		fmt.Println("→ Waiting for PostgreSQL to be ready...")
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
		return pgfs.Mount(cmd.Context(), cfg.GetPostgresURL(), fsCfg.TablePrefix, cfg.Podman.DataDir, mountPoint)
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

func mountInBackground(mountPoint string) error {
	args := []string{os.Args[0]}
	if cfgPath != "" {
		args = append(args, "-c", cfgPath)
	}
	args = append(args, "-i", cfgInstance, "mount", mountPoint)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("opening /dev/null: %w", err)
	}
	defer devNull.Close()

	logFile, err := os.CreateTemp("", "aifs-mount-*.log")
	if err != nil {
		return fmt.Errorf("creating mount log: %w", err)
	}
	defer logFile.Close()

	attr := &os.ProcAttr{
		Files: []*os.File{devNull, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}
	p, err := os.StartProcess(os.Args[0], args, attr)
	if err != nil {
		return fmt.Errorf("starting background mount: %w", err)
	}

	// Wait for the mount to become visible in /proc/self/mounts.
	for i := 0; i < 30; i++ {
		time.Sleep(200 * time.Millisecond)
		if mountVisible(mountPoint) {
			fmt.Printf("background mount pid %d at %s\n", p.Pid, mountPoint)
			_ = os.Remove(logFile.Name())
			return nil
		}
		if err := p.Signal(syscall.Signal(0)); err != nil {
			break
		}
	}
	_ = p.Kill()
	_, _ = p.Wait()
	logOut, _ := os.ReadFile(logFile.Name())
	_ = os.Remove(logFile.Name())
	if len(logOut) > 0 {
		return fmt.Errorf("background mount did not become ready: %s", logOut)
	}
	return fmt.Errorf("background mount did not become ready")
}

// mountVisible reports whether mountPoint is currently mounted as an aifs FUSE filesystem.
func mountVisible(mountPoint string) bool {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return false
	}
	defer f.Close()

	mp, err := filepath.Abs(mountPoint)
	if err != nil {
		mp = mountPoint
	}

	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 4 {
			continue
		}
		if fields[0] == "aifs" && strings.HasPrefix(fields[2], "fuse") {
			m, _ := filepath.Abs(fields[1])
			if m == mp {
				return true
			}
		}
	}
	return false
}
