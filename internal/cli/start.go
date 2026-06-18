package cli

import (
	"fmt"
	"strings"
	"time"

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

		// Wait for PostgreSQL to finish initialization (initdb + init scripts).
		fmt.Println("→ Waiting for PostgreSQL to be ready...")
		for i := 0; i < 60; i++ {
			if ready, _ := pm.PGIsReady(); ready {
				break
			}
			time.Sleep(time.Second)
		}

		// 8. Initialize pgBackRest stanza (via backup container)
		if cfg.PITR.Enabled {
			bm, err := newBackupManager()
			if err != nil {
				return fmt.Errorf("creating backup manager: %w", err)
			}

			// Ensure backup SSH key exists before authorizing it on the PG container.
			if _, err := bm.EnsureSSHKey(); err != nil {
				return fmt.Errorf("backup ssh key: %w", err)
			}

			// Install backup public key into the PG container so pgbackrest can SSH in.
			fmt.Println("→ Authorizing backup key on PostgreSQL container...")
			if err := bm.AuthorizeKeyOnInstance(); err != nil {
				return fmt.Errorf("authorizing backup key: %w", err)
			}

			// Ensure backup infrastructure is ready
			fmt.Println("→ Ensuring backup infrastructure is ready...")
			if err := bm.EnsureBackupInfra(); err != nil {
				return fmt.Errorf("backup infrastructure: %w", err)
			}

			pt := pitr.New(cfg, pm, bm)
			if err := pt.EnsureStanza(); err != nil {
				fmt.Printf("  ⚠ stanza create warning: %v\n", err)
			}
			if err := pt.CheckStanza(); err != nil {
				fmt.Printf("  ⚠ stanza check warning: %v\n", err)
			}

			// init.sh set archive_mode=on but did NOT set archive_command during
			// first-time initdb because the stanza doesn't exist yet.  Set it
			// now via ALTER SYSTEM and wait for the archiver to process any
			// pending WAL segments before the first backup can succeed.
			stanza := cfg.PITR.PgBackRestStanza
			archiveCmd := fmt.Sprintf("sudo -n -u root pgbackrest --stanza=%s archive-push %%p", stanza)
			setSQL := fmt.Sprintf("ALTER SYSTEM SET archive_command TO '%s'", archiveCmd)

			if _, err := pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-c", setSQL); err != nil {
				fmt.Printf("  ⚠ setting archive_command: %v\n", err)
			} else {
				fmt.Println("→ archive_command configured")
			}

			// Reload to make the archiver pick up the new command, then
			// switch WAL to give it a fresh segment.
			pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-c", "SELECT pg_reload_conf()")
			pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-c", "SELECT pg_switch_wal()")

			fmt.Println("→ Waiting for WAL archiver to catch up...")
			for i := 0; i < 30; i++ {
				time.Sleep(2 * time.Second)
				out, err := pm.Exec("psql", "-U", cfg.Postgres.User, "-d", cfg.Postgres.Database, "-t", "-c",
					"SELECT count(*) FROM pg_ls_dir('pg_wal/archive_status') AS f WHERE f LIKE '%.ready'")
				if err == nil && strings.TrimSpace(out) == "0" {
					fmt.Println("  ✓ WAL archiver caught up")
					break
				}
			}
		}

		// Persist auto-assigned ports so subsequent starts don't re-allocate.
		if err := saveConfig(); err != nil {
			fmt.Printf("  ⚠ failed to save config: %v\n", err)
		}

		fmt.Println("\n✓ started")
		fmt.Printf("  PostgreSQL: postgres://%s:%s@localhost:%d/%s\n",
			cfg.Postgres.User, cfg.Postgres.Password,
			cfg.Postgres.Port, cfg.Postgres.Database)
		return nil
	},
}
