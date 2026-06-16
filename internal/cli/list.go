package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mars-base/aifs/internal/config"
	"github.com/mars-base/aifs/internal/platform"
	"github.com/mars-base/aifs/internal/podman"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all aifs instances",
	Long:  `list shows all configured instances, their container status, and database name.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := cfgPath
		if path == "" {
			path = platform.DefaultConfigPath()
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("config file not found: %s", path)
		}

		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if len(cfg.Instances) == 0 {
			fmt.Println("No instances configured.")
			fmt.Printf("Run: aifs config init --add <name>\n")
			return nil
		}

		fmt.Printf("%-12s %-12s %-20s %-10s %s\n", "NAME", "DATABASE", "CONTAINER", "STATUS", "PORT")
		fmt.Println(strings.Repeat("-", 70))

		for name := range cfg.Instances {
			// Work on a per-instance view of the config.
			if err := cfg.SetInstance(name); err != nil {
				fmt.Printf("%-12s %-12s %-20s %-10s %s\n", name, "-", "-", "error", err)
				continue
			}

			dbName := cfg.Postgres.Database
			if dbName == "" {
				dbName = name + "_db"
			}

			pm, err := podman.New(cfg)
			if err != nil {
				fmt.Printf("%-12s %-12s %-20s %-10s %s\n", name, dbName, cfg.Podman.ContainerName, "error", err)
				continue
			}

			cs, err := pm.Status()
			status := cs.Status
			if err != nil {
				status = "error"
			}

			fmt.Printf("%-12s %-12s %-20s %-10s %d\n",
				name, dbName, cfg.Podman.ContainerName, status, cfg.Postgres.Port)
		}

		return nil
	},
}
