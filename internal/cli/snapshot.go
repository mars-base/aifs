package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pitr"
)

func init() {
	rootCmd.AddCommand(snapshotCmd)
	snapshotCmd.AddCommand(snapshotCreateCmd)
	snapshotCmd.AddCommand(snapshotListCmd)
	snapshotCmd.AddCommand(snapshotDeleteCmd)
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Snapshot management (create/list/delete)",
	Long:  `snapshot manages pgBackRest backup snapshots. Supports full, incr, and diff backup types.`,
}

var (
	snapComment string
	snapType    string
	snapLimit   int
)

var snapshotCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new snapshot",
	Long: `Create a pgBackRest backup snapshot.

Backup types:
  full  - Full backup (default)
  incr  - Incremental backup (changes since last backup)
  diff  - Differential backup (changes since last full backup)`,
	Example: `  aifs snapshot create
  aifs snapshot create --comment "after-training"
  aifs snapshot create --type incr --comment "incremental backup"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		pt := pitr.New(cfg, pm)

		snap, err := pt.CreateSnapshot(snapComment, snapType)
		if err != nil {
			return err
		}

		fmt.Printf("\n✓ Snapshot created successfully\n")
		fmt.Printf("  Name: %s\n", snap.Name)
		fmt.Printf("  Type: %s\n", snap.Type)
		fmt.Printf("  Time: %s\n", snap.Timestamp.Format("2006-01-02 15:04:05"))
		return nil
	},
}

var snapshotListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all snapshots",
	Example: `  aifs snapshot list
  aifs snapshot list --limit 10`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		pt := pitr.New(cfg, pm)

		snapshots, err := pt.ListSnapshots(snapLimit)
		if err != nil {
			return err
		}

		if len(snapshots) == 0 {
			fmt.Println("(no snapshots)")
			return nil
		}

		fmt.Printf("%-25s  %-30s  %-10s\n", "Time", "Name", "Type")
		fmt.Println("-------------------------------------------------------------------")
		for _, s := range snapshots {
			fmt.Printf("%-25s  %-30s  %-10s\n",
				s.Timestamp.Format("2006-01-02 15:04:05"),
				s.Name, s.Type)
		}
		return nil
	},
}

var snapshotDeleteCmd = &cobra.Command{
	Use:     "delete <snapshot-name>",
	Aliases: []string{"rm"},
	Short:   "Delete a specific snapshot",
	Example: `  aifs snapshot delete 20260614-143005F`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		pt := pitr.New(cfg, pm)

		if err := pt.DeleteSnapshot(args[0]); err != nil {
			return err
		}

		fmt.Println("✓ Snapshot deleted")
		return nil
	},
}

func init() {
	snapshotCreateCmd.Flags().StringVar(&snapComment, "comment", "", "Snapshot comment")
	snapshotCreateCmd.Flags().StringVar(&snapType, "type", "full", "Backup type: full, incr, diff")

	snapshotListCmd.Flags().IntVar(&snapLimit, "limit", 0, "Limit display count (0=all)")
}
