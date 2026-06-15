package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pitr"
	"github.com/mars-base/aifs/internal/platform"
)

func init() {
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "One-click initialization of aifs environment",
	Long: `setup initializes the aifs runtime environment, including:
  1. Check dependencies (podman)
  2. podman machine init/start (macOS/Windows)
  3. Build PostgreSQL + pgBackRest image
  4. Create data directories
  5. Start PostgreSQL container
  6. Initialize pgBackRest stanza

Podman machine steps are skipped on Linux.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		fmt.Println("=== aifs setup ===")
		fmt.Printf("Platform: %s\n", platform.Detect())

		// 1. Checking dependencies
		fmt.Println("\n→ Checking dependencies...")
		missing := platform.MissingPrereqs()
		if len(missing) > 0 {
			for _, d := range missing {
				fmt.Printf("  ✗ %s: %s\n", d.Name, d.Hint)
			}
			return fmt.Errorf("missing dependencies, please install them first")
		}
		fmt.Println("  ✓ podman available")

		// 2. Initializing Podman manager
		pm, err := newPodman()
		if err != nil {
			return err
		}

		// 3. podman machine (macOS/Windows)
		if err := pm.EnsureMachine(); err != nil {
			return err
		}

		// 4. Building image
		if err := pm.EnsureImage(); err != nil {
			return err
		}

		// 5. Creating directories
		if err := pm.EnsureDirs(); err != nil {
			return err
		}

		// 6. Creating and starting container
		if err := pm.EnsureContainer(); err != nil {
			return err
		}

		// 7. Initializing pgBackRest stanza
		if cfg.PITR.Enabled {
			pt := pitr.New(cfg, pm)
			if err := pt.EnsureStanza(); err != nil {
				fmt.Printf("  ⚠ stanza create warning: %v\n", err)
			}
			if err := pt.CheckStanza(); err != nil {
				fmt.Printf("  ⚠ stanza check warning: %v\n", err)
			}
		}

		fmt.Println("\n✓ setup complete!")
		fmt.Printf("  PostgreSQL: postgres://%s:%s@localhost:%d/%s\n",
			cfg.Postgres.User, cfg.Postgres.Password,
			cfg.Postgres.Port, cfg.Postgres.Database)
		fmt.Println("  Next step: aifs start")
		return nil
	},
}
