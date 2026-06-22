package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pgfs"
	"github.com/mars-base/aifs/internal/pitr"
)

var (
	restoreTime     string
	restoreDryRun   bool
	restoreForce    bool
	restoreTailLogs bool
)

func init() {
	rootCmd.AddCommand(restoreCmd)
	restoreCmd.Flags().StringVar(&restoreTime, "time", "", "Restore to specified time (e.g. '2026-06-14 15:04:05+00' or '2026-06-14 15:04:05')")
	restoreCmd.Flags().BoolVar(&restoreDryRun, "dry-run", false, "Only show what would be done, do not execute")
	restoreCmd.Flags().BoolVar(&restoreForce, "force", false, "Skip confirmation prompt")
	restoreCmd.Flags().BoolVar(&restoreTailLogs, "tail-logs", false, "Stream restore container logs to stdout during recovery")
}

var restoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "PITR point-in-time recovery",
	Long: `restore rolls back the entire PostgreSQL database to a specified point in time.

WARNING: Restore will overwrite ALL current database data!

Process:
  1. Stop PostgreSQL
  2. pgBackRest restore --type=time --target=<time>
  3. Start PostgreSQL

Examples:
  aifs restore --time "2026-06-14 15:04:05+00"
  aifs restore --time "2026-06-14 15:04:05+00" --dry-run
  aifs restore --time "2026-06-14 15:04:05+00" --tail-logs
  aifs restore --time "2026-06-14 15:04:05+00" --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		if restoreTime == "" {
			return fmt.Errorf("Please specify restore time: --time \"2026-06-14 15:04:05\"")
		}

		var targetTime time.Time
		var err error
		for _, layout := range []string{
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05-0700",
			"2006-01-02 15:04:05-07",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05Z0700",
			"2006-01-02 15:04:05",
		} {
			targetTime, err = time.Parse(layout, restoreTime)
			if err == nil {
				break
			}
		}
		if err != nil {
			return fmt.Errorf("Invalid time format: %w (use: YYYY-MM-DD HH:MM:SS+00 or YYYY-MM-DD HH:MM:SS)", err)
		}

		mounted, err := pgfs.IsInstanceMounted(cmd.Context(), cfg.GetPostgresURL(), cfg.EffectiveFilesystem().TablePrefix)
		if err != nil {
			return fmt.Errorf("checking active mounts: %w", err)
		}
		if mounted {
			return fmt.Errorf("instance %s has an active aifs mount; please run 'aifs umount <mountpoint>' before restore", cfg.Instance)
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		bm, err := newBackupManager()
		if err != nil {
			return err
		}

		// Re-authorize the backup SSH key on the PG container. The authorized_keys
		// file lives inside the container and is lost if the container is recreated,
		// so we ensure it is installed before every restore operation.
		if err := bm.AuthorizeKeyOnInstance(); err != nil {
			return fmt.Errorf("authorizing backup key: %w", err)
		}

		pt := pitr.New(cfg, pm, bm)

		// dry-run mode
		if restoreDryRun {
			return pt.Restore(targetTime, true, restoreTailLogs)
		}

		// Confirmation (unless --force)
		if !restoreForce {
			fmt.Printf("!  Confirm restore operation\n")
			fmt.Printf("  Instance:    %s\n", cfg.Instance)
			fmt.Printf("  Target time: %s\n", targetTime.Format("2006-01-02 15:04:05"))
			fmt.Printf("  This will restore the database to that time point. All changes after it will be permanently lost!\n")
			fmt.Printf("\nConfirm? [y/N]: ")

			var answer string
			fmt.Scanln(&answer)
			if answer != "y" && answer != "Y" && answer != "yes" {
				fmt.Println("Cancelled")
				return nil
			}
		}

		return pt.Restore(targetTime, false, restoreTailLogs)
	},
}
