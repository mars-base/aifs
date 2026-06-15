package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pitr"
)

var (
	restoreTime    string
	restoreDryRun  bool
	restoreForce   bool
)

func init() {
	rootCmd.AddCommand(restoreCmd)
	restoreCmd.Flags().StringVar(&restoreTime, "time", "", "Restore to specified time (ISO8601, e.g. '2026-06-14 15:04:05')")
	restoreCmd.Flags().BoolVar(&restoreDryRun, "dry-run", false, "Only show what would be done, do not execute")
	restoreCmd.Flags().BoolVar(&restoreForce, "force", false, "Skip confirmation prompt")
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
  4. JuiceFS auto-detects new data

Examples:
  aifs restore --time "2026-06-14 15:04:05"
  aifs restore --time "2026-06-14 15:04:05" --dry-run
  aifs restore --time "2026-06-14 15:04:05" --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		if restoreTime == "" {
			return fmt.Errorf("Please specify restore time: --time \"2026-06-14 15:04:05\"")
		}

		targetTime, err := time.Parse("2006-01-02 15:04:05", restoreTime)
		if err != nil {
			return fmt.Errorf("Invalid time format: %w (use: YYYY-MM-DD HH:MM:SS)", err)
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		pt := pitr.New(cfg, pm)

		// dry-run mode
		if restoreDryRun {
			return pt.Restore(targetTime, true)
		}

		// Confirmation (unless --force)
		if !restoreForce {
			fmt.Printf("⚠️  Confirm restore operation\n")
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

		return pt.Restore(targetTime, false)
	},
}
