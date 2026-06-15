package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/pitr"
	"github.com/mars-base/aifs/internal/platform"
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start aifs services (PostgreSQL + pgBackRest)",
	Long: `start initializes the runtime environment and launches the PostgreSQL container.

Steps:
  1. Check dependencies
  2. podman machine init/start (macOS/Windows only)
  3. Build PostgreSQL + pgBackRest image (if missing)
  4. Create data directories (if missing)
  5. Start PostgreSQL container
  6. Initialize pgBackRest stanza (if PITR enabled)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadConfig(); err != nil {
			return err
		}

		fmt.Println("=== aifs start ===")
		fmt.Printf("Platform: %s\n", platform.Detect())

		// 1. Check dependencies
		fmt.Println("\n→ Checking dependencies...")
		missing := platform.MissingPrereqs()
		if len(missing) > 0 {
			for _, d := range missing {
				fmt.Printf("  ✗ %s: %s\n", d.Name, d.Hint)
			}
			return fmt.Errorf("missing dependencies, please install them first")
		}
		fmt.Println("  ✓ podman available")

		// 2. Initialize podman manager
		pm, err := newPodman()
		if err != nil {
			return err
		}

		// 3. podman machine (macOS/Windows, no-op on Linux)
		if err := pm.EnsureMachine(); err != nil {
			return err
		}

		// 4. Build image (if missing)
		if err := pm.EnsureImage(); err != nil {
			return err
		}

		// 5. Create directories (if missing)
		if err := pm.EnsureDirs(); err != nil {
			return err
		}

		// 6. Ensure shared network
		if err := pm.EnsureNetwork(); err != nil {
			return err
		}

		// 7. Create and start container
		if err := pm.EnsureContainer(); err != nil {
			return err
		}

		// 8. Initialize pgBackRest stanza
		if cfg.PITR.Enabled {
			pt := pitr.New(cfg, pm)
			if err := pt.EnsureStanza(); err != nil {
				fmt.Printf("  ⚠ stanza create warning: %v\n", err)
			}
			if err := pt.CheckStanza(); err != nil {
				fmt.Printf("  ⚠ stanza check warning: %v\n", err)
			}
		}

		fmt.Println("\n✓ started")
		fmt.Printf("  PostgreSQL: postgres://%s:%s@localhost:%d/%s\n",
			cfg.Postgres.User, cfg.Postgres.Password,
			cfg.Postgres.Port, cfg.Postgres.Database)
		return nil
	},
}
