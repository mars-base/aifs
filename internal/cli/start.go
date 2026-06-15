package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start aifs services (PostgreSQL + pgBackRest)",
	Long:  `start launches the PostgreSQL container. Run aifs setup first if the container does not exist.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		pm, err := newPodman()
		if err != nil {
			return err
		}

		fmt.Println("→ Starting aifs services...")
		if err := pm.EnsureContainer(); err != nil {
			return err
		}

		fmt.Println("✓ aifs started")
		return nil
	},
}
