package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pitr"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show aifs running status",
	Long:  `status shows PostgreSQL container status, PG health check, active mounts, and recent backup info.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}
		bm, err := newBackupManager()
		if err != nil {
			return err
		}

		fmt.Println("=== aifs status ===")
		fmt.Printf("Instance: %s\n", cfg.Instance)

		// Container status
		cs, err := pm.Status()
		if err != nil {
			return err
		}
		fmt.Printf("\nContainer: %s\n", cs.Name)
		fmt.Printf("  Status: %s\n", cs.Status)
		if cs.Ports != "" {
			fmt.Printf("  Ports: %s\n", cs.Ports)
		}

		// PG health check
		if cs.Running {
			ready, _ := pm.PGIsReady()
			if ready {
				fmt.Println("\nPostgreSQL: ✓ accepting connections")
				fmt.Printf("  Connection: %s\n", cfg.GetPostgresURL())
			} else {
				fmt.Println("\nPostgreSQL: ✗ not accepting connections")
			}
		}

		// Active FUSE mounts
		if mounts, err := activeAIFSMounts(); err == nil {
			if len(mounts) > 0 {
				fmt.Println("\nActive mounts:")
				for _, m := range mounts {
					fmt.Printf("  %s\n", m)
				}
			} else {
				fmt.Println("\nActive mounts: (none)")
			}
		}

		// Backup info (when PITR enabled)
		if cfg.PITR.Enabled && cs.Running {
			pt := pitr.New(cfg, pm, bm)

			type backupResult struct {
				snapshots []pitr.Snapshot
				err       error
			}
			ch := make(chan backupResult, 1)
			go func() {
				snapshots, err := pt.ListSnapshots(5)
				ch <- backupResult{snapshots, err}
			}()

			select {
			case r := <-ch:
				if r.err == nil && len(r.snapshots) > 0 {
					fmt.Println("\nRecent backups:")
					for _, s := range r.snapshots {
						fmt.Printf("  %s  %s  %s\n",
							s.Timestamp.Format("2006-01-02 15:04"),
							s.Name, s.Type)
					}
				} else {
					fmt.Println("\nRecent backups: (none)")
				}
			case <-time.After(5 * time.Second):
				fmt.Println("\nRecent backups: (pgbackrest not responding)")
			}
		}

		fmt.Println()
		return nil
	},
}

// activeAIFSMounts returns local FUSE mount points whose source is "aifs".
func activeAIFSMounts() ([]string, error) {
	f, err := os.Open("/proc/self/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mounts []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 4 {
			continue
		}
		if fields[0] == "aifs" && strings.HasPrefix(fields[2], "fuse") {
			mounts = append(mounts, fields[1])
		}
	}
	return mounts, s.Err()
}
